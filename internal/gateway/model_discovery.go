package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const (
	modelDiscoveryTTL     = 5 * time.Minute
	modelDiscoveryTimeout = 12 * time.Second
)

var modelDiscoveryClient = &fasthttp.Client{
	ReadTimeout:     modelDiscoveryTimeout,
	WriteTimeout:    modelDiscoveryTimeout,
	MaxConnDuration: 30 * time.Second,
}

type modelDiscoveryItem struct {
	ID                         string
	OwnedBy                    string
	Created                    int64
	DisplayName                string
	Version                    string
	SupportedGenerationMethods []string
}

type modelDiscoveryCacheEntry struct {
	models    []modelDiscoveryItem
	expiresAt time.Time
}

type standardModelAccount struct {
	AccountID   uuid.UUID
	ChannelID   uuid.UUID
	Provider    string
	APIFormat   string
	Endpoint    string
	Credentials string
}

func (g *Gateway) discoverStandardModels() []modelDiscoveryItem {
	var rows []standardModelAccount
	if err := g.db.Table("accounts").
		Select(`accounts.id AS account_id, accounts.channel_id, channels.type AS provider,
			channels.api_format, COALESCE(NULLIF(accounts.endpoint, ''), channels.endpoint) AS endpoint, accounts.credentials`).
		Joins("JOIN channels ON channels.id = accounts.channel_id AND channels.enabled = true AND channels.deleted_at IS NULL").
		Where("accounts.enabled = true AND accounts.deleted_at IS NULL AND accounts.cred_type = ?", "api_key").
		Where("channels.api_format IN ?", []string{"", "standard", "responses"}).
		Order("channels.priority DESC, accounts.weight DESC, accounts.created_at ASC").
		Scan(&rows).Error; err != nil {
		logger.Warnf("gateway.models", "standard account lookup failed", logger.Err(err))
		return nil
	}

	seen := map[string]modelDiscoveryItem{}
	for _, row := range rows {
		models, err := g.modelsForStandardAccount(row)
		if err != nil {
			logger.Warnf("gateway.models", "upstream model discovery failed",
				logger.F("channel_id", row.ChannelID.String()),
				logger.F("account_id", row.AccountID.String()),
				logger.F("provider", row.Provider),
				logger.Err(err),
			)
			continue
		}
		for _, model := range models {
			if strings.TrimSpace(model.ID) == "" {
				continue
			}
			if _, ok := seen[model.ID]; !ok {
				seen[model.ID] = model
			}
		}
	}
	result := make([]modelDiscoveryItem, 0, len(seen))
	for _, model := range seen {
		result = append(result, model)
	}
	return result
}

func (g *Gateway) modelsForStandardAccount(row standardModelAccount) ([]modelDiscoveryItem, error) {
	cacheKey := row.AccountID.String()
	now := time.Now()
	g.modelMu.Lock()
	if entry, ok := g.modelCache[cacheKey]; ok && now.Before(entry.expiresAt) {
		models := cloneModelItems(entry.models)
		g.modelMu.Unlock()
		return models, nil
	}
	g.modelMu.Unlock()

	decrypted, err := crypto.Decrypt(row.Credentials)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	key := provider.ExtractCredentialKey(decrypted)
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("empty credential")
	}

	models, err := fetchNativeModels(row.Provider, row.Endpoint, key)
	if err != nil {
		models, err = fetchOpenAICompatibleModels(row.Endpoint, key)
		if err != nil {
			return nil, err
		}
	}

	g.modelMu.Lock()
	g.modelCache[cacheKey] = modelDiscoveryCacheEntry{models: cloneModelItems(models), expiresAt: now.Add(modelDiscoveryTTL)}
	g.modelMu.Unlock()
	return models, nil
}

func fetchNativeModels(providerName, endpoint, key string) ([]modelDiscoveryItem, error) {
	switch providerName {
	case "openai":
		return fetchOpenAICompatibleModels(endpoint, key)
	case "gemini":
		return fetchGeminiModels(endpoint, key)
	case "anthropic":
		return fetchAnthropicModels(endpoint, key)
	default:
		return nil, fmt.Errorf("no native model endpoint for provider %q", providerName)
	}
}

func fetchOpenAICompatibleModels(endpoint, key string) ([]modelDiscoveryItem, error) {
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(strings.TrimRight(endpoint, "/") + "/models")
	req.Header.SetMethod(fasthttp.MethodGet)
	req.Header.Set("Authorization", "Bearer "+key)
	if err := modelDiscoveryClient.DoTimeout(req, resp, modelDiscoveryTimeout); err != nil {
		return nil, fmt.Errorf("openai-compatible models request: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("openai-compatible models status %d", resp.StatusCode())
	}
	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body(), &parsed); err != nil {
		return nil, fmt.Errorf("parse openai-compatible models: %w", err)
	}
	models := make([]modelDiscoveryItem, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		if item.ID == "" {
			continue
		}
		models = append(models, modelDiscoveryItem{
			ID:      item.ID,
			OwnedBy: firstNonEmpty(item.OwnedBy, "openai-compatible"),
			Created: item.Created,
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("openai-compatible models response was empty")
	}
	return models, nil
}

func fetchGeminiModels(endpoint, key string) ([]modelDiscoveryItem, error) {
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	base := strings.TrimRight(endpoint, "/")
	req.SetRequestURI(base + "/models")
	req.Header.SetMethod(fasthttp.MethodGet)
	req.URI().QueryArgs().Set("key", key)
	if err := modelDiscoveryClient.DoTimeout(req, resp, modelDiscoveryTimeout); err != nil {
		return nil, fmt.Errorf("gemini models request: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("gemini models status %d", resp.StatusCode())
	}
	var parsed struct {
		Models []struct {
			Name                       string   `json:"name"`
			Version                    string   `json:"version"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Body(), &parsed); err != nil {
		return nil, fmt.Errorf("parse gemini models: %w", err)
	}
	models := make([]modelDiscoveryItem, 0, len(parsed.Models))
	for _, item := range parsed.Models {
		id := strings.TrimPrefix(item.Name, "models/")
		if id == "" {
			continue
		}
		models = append(models, modelDiscoveryItem{
			ID:                         id,
			OwnedBy:                    "google",
			DisplayName:                item.DisplayName,
			Version:                    item.Version,
			SupportedGenerationMethods: item.SupportedGenerationMethods,
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("gemini models response was empty")
	}
	return models, nil
}

func fetchAnthropicModels(endpoint, key string) ([]modelDiscoveryItem, error) {
	req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(strings.TrimRight(endpoint, "/") + "/models")
	req.Header.SetMethod(fasthttp.MethodGet)
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	if err := modelDiscoveryClient.DoTimeout(req, resp, modelDiscoveryTimeout); err != nil {
		return nil, fmt.Errorf("anthropic models request: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("anthropic models status %d", resp.StatusCode())
	}
	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body(), &parsed); err != nil {
		return nil, fmt.Errorf("parse anthropic models: %w", err)
	}
	models := make([]modelDiscoveryItem, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		if item.ID == "" {
			continue
		}
		models = append(models, modelDiscoveryItem{
			ID:          item.ID,
			OwnedBy:     "anthropic",
			DisplayName: item.DisplayName,
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("anthropic models response was empty")
	}
	return models, nil
}

func (g *Gateway) writeAnthropicModels(ctx *fasthttp.RequestCtx, models []modelDiscoveryItem, tokenID string) {
	data := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		item := map[string]interface{}{
			"id":           model.ID,
			"type":         "model",
			"display_name": firstNonEmpty(model.DisplayName, model.ID),
			"created_at":   "",
		}
		data = append(data, item)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"data":     data,
		"has_more": false,
		"first_id": firstModelID(models),
		"last_id":  lastModelID(models),
	})
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(body)
	logger.Debugf("gateway.models", "listed anthropic models", logger.F("token_id", tokenID), logger.F("count", len(models)))
}

func (g *Gateway) writeGeminiModels(ctx *fasthttp.RequestCtx, models []modelDiscoveryItem, tokenID string) {
	data := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		methods := model.SupportedGenerationMethods
		if len(methods) == 0 {
			methods = []string{"generateContent", "streamGenerateContent"}
		}
		item := map[string]interface{}{
			"name":                       "models/" + model.ID,
			"version":                    model.Version,
			"displayName":                firstNonEmpty(model.DisplayName, model.ID),
			"supportedGenerationMethods": methods,
		}
		data = append(data, item)
	}
	body, _ := json.Marshal(map[string]interface{}{"models": data})
	ctx.SetContentType("application/json")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(body)
	logger.Debugf("gateway.models", "listed gemini models", logger.F("token_id", tokenID), logger.F("count", len(models)))
}

func cloneModelItems(models []modelDiscoveryItem) []modelDiscoveryItem {
	out := make([]modelDiscoveryItem, len(models))
	copy(out, models)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstModelID(models []modelDiscoveryItem) string {
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
}

func lastModelID(models []modelDiscoveryItem) string {
	if len(models) == 0 {
		return ""
	}
	return models[len(models)-1].ID
}
