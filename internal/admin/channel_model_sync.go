package admin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const channelModelSyncTimeout = 20 * time.Second

var channelModelSyncClient = &fasthttp.Client{
	ReadTimeout:     channelModelSyncTimeout,
	WriteTimeout:    channelModelSyncTimeout,
	MaxConnDuration: 30 * time.Second,
}

type ChannelModelSyncResponse struct {
	Channel db.Channel `json:"channel"`
	Models  []string   `json:"models"`
	Count   int        `json:"count"`
}

// HandleChannelModelSync fetches available models from the selected channel's upstream accounts
// and writes the discovered model IDs back to the channel model list.
func (h *Handler) HandleChannelModelSync(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var ch db.Channel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&ch).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "channel not found")
		return
	}
	var accounts []db.Account
	if err := h.db.Where("channel_id = ? AND enabled = true AND deleted_at IS NULL", ch.ID).Order("weight DESC, created_at ASC").Find(&accounts).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "load accounts failed")
		return
	}
	if len(accounts) == 0 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel has no enabled account")
		return
	}

	models, err := h.fetchModelsForChannel(ch, accounts)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadGateway, err.Error())
		return
	}
	if len(models) == 0 {
		h.jsonError(ctx, fasthttp.StatusBadGateway, "upstream returned no models")
		return
	}
	modelCSV := strings.Join(models, ",")
	updates := map[string]interface{}{"models": modelCSV, "updated_at": time.Now()}
	if err := h.db.Model(&ch).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update channel models failed")
		return
	}
	if err := h.db.Where("id = ? AND deleted_at IS NULL", ch.ID).First(&ch).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "reload channel failed")
		return
	}
	if h.RefreshPool != nil {
		h.RefreshPool(ch.ID.String())
	}
	auditUpdateCtx(h.db, "channel", ch.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"models": modelCSV, "source": "upstream"})
	h.jsonResponse(ctx, fasthttp.StatusOK, ChannelModelSyncResponse{Channel: ch, Models: models, Count: len(models)})
}

func (h *Handler) fetchModelsForChannel(ch db.Channel, accounts []db.Account) ([]string, error) {
	var lastErr error
	for _, acc := range accounts {
		models, err := fetchModelsForAccount(ch, acc)
		if err == nil && len(models) > 0 {
			return normalizeModelIDs(models), nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("upstream returned no models")
}

func fetchModelsForAccount(ch db.Channel, acc db.Account) ([]string, error) {
	credential, err := crypto.Decrypt(acc.Credentials)
	if err != nil {
		return nil, fmt.Errorf("decrypt account credential: %w", err)
	}
	key := strings.TrimSpace(provider.ExtractCredentialKey(credential))
	if key == "" {
		return nil, fmt.Errorf("account credential is empty")
	}
	endpoint := upstreamconfig.AccountEndpoint(&ch, &acc)
	switch ch.APIFormat {
	case "antigravity":
		models, err := fetchAntigravityAvailableModels(endpoint, key, antigravityProjectIDFromMetadata(acc.Metadata))
		if err != nil {
			return nil, err
		}
		return antigravity.NormalizeAvailableModels(models), nil
	case "", "standard", "responses":
		switch ch.Type {
		case "openai":
			return fetchOpenAIModelIDs(endpoint, key)
		case "gemini":
			return fetchGeminiModelIDs(endpoint, key)
		case "anthropic":
			return fetchAnthropicModelIDs(endpoint, key)
		}
	}
	return nil, fmt.Errorf("channel api_format %q does not support upstream model sync", ch.APIFormat)
}

func fetchAntigravityAvailableModels(endpoint, accessToken, projectID string) ([]string, error) {
	base := strings.TrimRight(endpoint, "/")
	if base == "" {
		base = antigravity.APIEndpoint
	}
	body := []byte(`{}`)
	if strings.TrimSpace(projectID) != "" {
		body, _ = json.Marshal(map[string]string{"project": strings.TrimSpace(projectID)})
	}
	status, respBody, err := doModelSyncRequest(fasthttp.MethodPost, base+"/v1internal:fetchAvailableModels", accessToken, body, func(req *fasthttp.Request) {
		req.Header.Set("User-Agent", antigravity.RequestUserAgent())
	})
	if err != nil {
		return nil, fmt.Errorf("antigravity models request: %w", err)
	}
	if status >= 300 {
		return nil, fmt.Errorf("antigravity models status %d: %s", status, compactSyncBody(respBody))
	}
	return parseAntigravityModelIDs(respBody)
}

func fetchOpenAIModelIDs(endpoint, key string) ([]string, error) {
	status, body, err := doModelSyncRequest(fasthttp.MethodGet, strings.TrimRight(endpoint, "/")+"/models", key, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("openai models request: %w", err)
	}
	if status >= 300 {
		return nil, fmt.Errorf("openai models status %d", status)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse openai models: %w", err)
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func fetchGeminiModelIDs(endpoint, key string) ([]string, error) {
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(strings.TrimRight(endpoint, "/") + "/models")
	req.Header.SetMethod(fasthttp.MethodGet)
	req.URI().QueryArgs().Set("key", key)
	if err := channelModelSyncClient.DoTimeout(req, resp, channelModelSyncTimeout); err != nil {
		return nil, fmt.Errorf("gemini models request: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("gemini models status %d", resp.StatusCode())
	}
	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Body(), &parsed); err != nil {
		return nil, fmt.Errorf("parse gemini models: %w", err)
	}
	ids := make([]string, 0, len(parsed.Models))
	for _, item := range parsed.Models {
		ids = append(ids, strings.TrimPrefix(item.Name, "models/"))
	}
	return ids, nil
}

func fetchAnthropicModelIDs(endpoint, key string) ([]string, error) {
	status, body, err := doModelSyncRequest(fasthttp.MethodGet, strings.TrimRight(endpoint, "/")+"/models", key, nil, func(req *fasthttp.Request) {
		req.Header.Set("x-api-key", key)
		req.Header.Del("Authorization")
		req.Header.Set("anthropic-version", "2023-06-01")
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic models request: %w", err)
	}
	if status >= 300 {
		return nil, fmt.Errorf("anthropic models status %d", status)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse anthropic models: %w", err)
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func doModelSyncRequest(method, uri, bearer string, body []byte, customize func(*fasthttp.Request)) (int, []byte, error) {
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(uri)
	req.Header.SetMethod(method)
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	}
	if len(body) > 0 {
		req.SetBody(body)
	}
	if customize != nil {
		customize(req)
	}
	if err := channelModelSyncClient.DoTimeout(req, resp, channelModelSyncTimeout); err != nil {
		return 0, nil, err
	}
	return resp.StatusCode(), append([]byte(nil), resp.Body()...), nil
}

func parseAntigravityModelIDs(body []byte) ([]string, error) {
	var parsed struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && len(parsed.Models) > 0 {
		ids := make([]string, 0, len(parsed.Models))
		for id := range parsed.Models {
			ids = append(ids, id)
		}
		return ids, nil
	}
	var arrayParsed struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &arrayParsed); err != nil {
		return nil, fmt.Errorf("parse antigravity models: %w", err)
	}
	ids := make([]string, 0, len(arrayParsed.Models))
	for _, item := range arrayParsed.Models {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func antigravityProjectIDFromMetadata(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	if project, ok := metadata["project_id"].(string); ok {
		return strings.TrimSpace(project)
	}
	if loadRes, ok := metadata["load_code_assist"].(map[string]interface{}); ok {
		if project, ok := loadRes["cloudaicompanionProject"].(string); ok {
			return strings.TrimSpace(project)
		}
		if project, ok := loadRes["cloudaicompanionProject"].(map[string]interface{}); ok {
			if id, ok := project["id"].(string); ok {
				return strings.TrimSpace(id)
			}
		}
	}
	return ""
}

func normalizeModelIDs(models []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		model = strings.TrimPrefix(model, "models/")
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func compactSyncBody(body []byte) string {
	value := strings.TrimSpace(string(body))
	if len(value) > 240 {
		return value[:240]
	}
	return value
}
