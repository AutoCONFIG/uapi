package chatgptreverse

import (
	"bytes"
	"crypto/rand"
	"crypto/sha3"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	mathrand "math/rand"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const (
	defaultBaseURL   = "https://chatgpt.com"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"
	defaultPowScript = "https://chatgpt.com/backend-api/sentinel/sdk.js"
	// conversationContinuationMaxAge is the maximum age for a conversation_id to be considered valid for continuation
	conversationContinuationMaxAge = 1 * time.Hour
)

type Adaptor struct {
	channel      *db.Channel
	account      *db.Account
	model        string
	stream       bool
	deviceID     string
	sessionID    string
	scriptSource []string
	dataBuild    string
}

func (a *Adaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
	a.deviceID = metadataString(account, "oai_device_id")
	if a.deviceID == "" {
		a.deviceID = metadataString(account, "oai-device-id")
	}
	if a.deviceID == "" {
		a.deviceID = uuid.NewString()
	}
	a.sessionID = metadataString(account, "oai_session_id")
	if a.sessionID == "" {
		a.sessionID = metadataString(account, "oai-session-id")
	}
	if a.sessionID == "" {
		a.sessionID = uuid.NewString()
	}
}

func (a *Adaptor) SetRequestParams(model string, stream bool) {
	a.model = model
	a.stream = stream
}

func (a *Adaptor) GetRequestURL(path string) (string, error) {
	return strings.TrimRight(a.baseURL(), "/") + "/backend-api/conversation", nil
}

func (a *Adaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	accessToken := provider.ExtractCredentialKey(credentials)
	if accessToken == "" {
		return fmt.Errorf("chatgpt reverse access token is empty")
	}
	if err := a.bootstrap(); err != nil {
		return err
	}
	requirements, err := a.chatRequirements(accessToken)
	if err != nil {
		return err
	}
	setBrowserHeaders(&req.Header, a.deviceID, a.sessionID)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenAI-Target-Path", "/backend-api/conversation")
	req.Header.Set("X-OpenAI-Target-Route", "/backend-api/conversation")
	req.Header.Set("OpenAI-Sentinel-Chat-Requirements-Token", requirements.Token)
	if requirements.ProofToken != "" {
		req.Header.Set("OpenAI-Sentinel-Proof-Token", requirements.ProofToken)
	}
	if requirements.SOToken != "" {
		req.Header.Set("OpenAI-Sentinel-SO-Token", requirements.SOToken)
	}
	if requirements.TurnstileToken != "" {
		req.Header.Set("OpenAI-Sentinel-Turnstile-Token", requirements.TurnstileToken)
	}
	return nil
}

func (a *Adaptor) FromIR(req *ir.Request) ([]byte, error) {
	messages, err := conversationMessages(req)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = a.model
	}
	if model == "" {
		model = "auto"
	}
	body := map[string]interface{}{
		"action":                        "next",
		"messages":                      messages,
		"model":                         model,
		"parent_message_id":             uuid.NewString(),
		"conversation_mode":             map[string]interface{}{"kind": "primary_assistant"},
		"conversation_origin":           nil,
		"force_paragen":                 false,
		"force_paragen_model_slug":      "",
		"force_rate_limit":              false,
		"force_use_sse":                 true,
		"history_and_training_disabled": true,
		"reset_rate_limits":             false,
		"suggestions":                   []interface{}{},
		"supported_encodings":           []interface{}{},
		"system_hints":                  []interface{}{},
		"timezone":                      "Asia/Shanghai",
		"timezone_offset_min":           -480,
		"variant_purpose":               "comparison_implicit",
		"websocket_request_id":          uuid.NewString(),
		"client_contextual_info": map[string]interface{}{
			"is_dark_mode":      false,
			"time_since_loaded": 120,
			"page_height":       900,
			"page_width":        1400,
			"pixel_ratio":       2,
			"screen_height":     1440,
			"screen_width":      2560,
		},
	}

	// Check for conversation continuation (cache hit)
	if a.account != nil && a.account.Metadata != nil {
		conversationID, _ := a.account.Metadata["last_conversation_id"].(string)
		conversationTimestamp, _ := a.account.Metadata["last_conversation_timestamp"].(string)
		if conversationID != "" && conversationTimestamp != "" {
			if ts, err := time.Parse(time.RFC3339, conversationTimestamp); err == nil {
				if time.Since(ts) < conversationContinuationMaxAge {
					// Valid conversation_id, continue the conversation
					body["conversation_id"] = conversationID
				}
			}
		}
	}

	return json.Marshal(body)
}

func (a *Adaptor) ParseUsage(respBody []byte) (int, int, error) {
	return parseOpenAIUsage(respBody)
}

func (a *Adaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	return parseOpenAIUsage(lastChunk)
}

func (a *Adaptor) ParseUsageFull(respBody []byte) (provider.InternalUsage, error) {
	pt, ct, err := a.ParseUsage(respBody)
	if err != nil {
		return provider.InternalUsage{}, err
	}
	return provider.InternalUsage{PromptTokens: pt, CompletionTokens: ct}, nil
}

func (a *Adaptor) GetChannelType() string { return "openai" }

type ImageGenerationRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	N              int    `json:"n"`
	ResponseFormat string `json:"response_format"`
}

func (a *Adaptor) GenerateImage(body []byte, credentials string) ([]byte, int, error) {
	var req ImageGenerationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fasthttp.StatusBadRequest, fmt.Errorf("invalid image generation request")
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return nil, fasthttp.StatusBadRequest, fmt.Errorf("prompt is required")
	}
	if req.Model == "" {
		req.Model = a.model
	}
	if req.Model == "" {
		req.Model = "gpt-5.5"
	}
	if req.ResponseFormat == "" {
		req.ResponseFormat = "url"
	}
	accessToken := provider.ExtractCredentialKey(credentials)
	if accessToken == "" {
		return nil, fasthttp.StatusBadRequest, fmt.Errorf("chatgpt reverse access token is empty")
	}
	if err := a.bootstrap(); err != nil {
		return nil, fasthttp.StatusBadGateway, err
	}
	requirements, err := a.chatRequirements(accessToken)
	if err != nil {
		return nil, fasthttp.StatusBadGateway, err
	}
	conduitToken, err := a.prepareImageConversation(req, accessToken, requirements)
	if err != nil {
		return nil, fasthttp.StatusBadGateway, err
	}
	conversationID, fileIDs, sedimentIDs, err := a.startImageConversation(req, accessToken, requirements, conduitToken)
	if err != nil {
		return nil, fasthttp.StatusBadGateway, err
	}
	if conversationID != "" && len(fileIDs) == 0 && len(sedimentIDs) == 0 {
		polledFiles, polledSediments := a.pollImageConversation(conversationID, accessToken)
		fileIDs = appendUnique(fileIDs, polledFiles...)
		sedimentIDs = appendUnique(sedimentIDs, polledSediments...)
	}
	urls := make([]string, 0, len(fileIDs)+len(sedimentIDs))
	for _, fileID := range fileIDs {
		if url := a.resolveFileDownloadURL(fileID, accessToken); url != "" {
			urls = appendUnique(urls, url)
		}
	}
	for _, sedimentID := range sedimentIDs {
		if conversationID == "" {
			continue
		}
		if url := a.resolveAttachmentDownloadURL(conversationID, sedimentID, accessToken); url != "" {
			urls = appendUnique(urls, url)
		}
	}
	if len(urls) == 0 {
		return nil, fasthttp.StatusBadGateway, fmt.Errorf("chatgpt reverse image generation returned no image")
	}
	if req.N > 0 && len(urls) > req.N {
		urls = urls[:req.N]
	}
	data := make([]map[string]interface{}, 0, len(urls))
	for _, imageURL := range urls {
		item := map[string]interface{}{}
		if req.ResponseFormat == "b64_json" {
			if b64 := downloadImageBase64(imageURL); b64 != "" {
				item["b64_json"] = b64
			} else {
				item["url"] = imageURL
			}
		} else {
			item["url"] = imageURL
		}
		data = append(data, item)
	}
	out := map[string]interface{}{
		"created": time.Now().Unix(),
		"data":    data,
	}
	resp, _ := json.Marshal(out)
	return resp, fasthttp.StatusOK, nil
}

func (a *Adaptor) prepareImageConversation(req ImageGenerationRequest, accessToken string, requirements requirements) (string, error) {
	path := "/backend-api/f/conversation/prepare"
	parentID := uuid.NewString()
	body, _ := json.Marshal(map[string]interface{}{
		"action":                 "next",
		"fork_from_shared_post":  false,
		"parent_message_id":      parentID,
		"model":                  req.Model,
		"client_prepare_state":   "success",
		"timezone_offset_min":    -480,
		"timezone":               "Asia/Shanghai",
		"conversation_mode":      map[string]interface{}{"kind": "primary_assistant"},
		"system_hints":           []string{"picture_v2"},
		"supports_buffering":     true,
		"supported_encodings":    []string{"v1"},
		"client_contextual_info": map[string]interface{}{"app_name": "chatgpt.com"},
		"partial_query": map[string]interface{}{
			"id":      uuid.NewString(),
			"author":  map[string]interface{}{"role": "user"},
			"content": map[string]interface{}{"content_type": "text", "parts": []string{req.Prompt}},
		},
	})
	respBody, status, err := a.doJSON(path, body, accessToken, requirements, "*/*", "")
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("chatgpt reverse image prepare status %d: %s", status, truncateBody(respBody))
	}
	var resp struct {
		ConduitToken string `json:"conduit_token"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("decode image prepare response: %w", err)
	}
	if resp.ConduitToken == "" {
		resp.ConduitToken = "no-token"
	}
	return resp.ConduitToken, nil
}

func (a *Adaptor) startImageConversation(req ImageGenerationRequest, accessToken string, requirements requirements, conduitToken string) (string, []string, []string, error) {
	path := "/backend-api/f/conversation"
	body, _ := json.Marshal(map[string]interface{}{
		"action":            "next",
		"parent_message_id": uuid.NewString(),
		"model":             req.Model,
		"messages": []interface{}{
			map[string]interface{}{
				"id":          uuid.NewString(),
				"author":      map[string]interface{}{"role": "user"},
				"create_time": float64(time.Now().UnixNano()) / 1e9,
				"content":     map[string]interface{}{"content_type": "text", "parts": []string{req.Prompt}},
				"metadata": map[string]interface{}{
					"developer_mode_connector_ids": []interface{}{},
					"selected_github_repos":        []interface{}{},
					"selected_all_github_repos":    false,
					"system_hints":                 []string{"picture_v2"},
					"serialization_metadata":       map[string]interface{}{"custom_symbol_offsets": []interface{}{}},
				},
			},
		},
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  -480,
		"timezone":                             "Asia/Shanghai",
		"conversation_mode":                    map[string]interface{}{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []string{"picture_v2"},
		"supports_buffering":                   true,
		"supported_encodings":                  []string{"v1"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"client_contextual_info": map[string]interface{}{
			"is_dark_mode":      false,
			"time_since_loaded": 1200,
			"page_height":       1072,
			"page_width":        1724,
			"pixel_ratio":       1.2,
			"screen_height":     1440,
			"screen_width":      2560,
			"app_name":          "chatgpt.com",
		},
	})
	respBody, status, err := a.doJSON(path, body, accessToken, requirements, "text/event-stream", conduitToken)
	if err != nil {
		return "", nil, nil, err
	}
	if status >= 400 {
		return "", nil, nil, fmt.Errorf("chatgpt reverse image conversation status %d: %s", status, truncateBody(respBody))
	}
	return extractImageIDsFromSSE(respBody)
}

func (a *Adaptor) doJSON(path string, body []byte, accessToken string, requirements requirements, accept string, conduitToken string) ([]byte, int, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(strings.TrimRight(a.baseURL(), "/") + path)
	req.Header.SetMethod("POST")
	req.SetBody(body)
	setBrowserHeaders(&req.Header, a.deviceID, a.sessionID)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", accept)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenAI-Target-Path", path)
	req.Header.Set("X-OpenAI-Target-Route", path)
	req.Header.Set("OpenAI-Sentinel-Chat-Requirements-Token", requirements.Token)
	if requirements.ProofToken != "" {
		req.Header.Set("OpenAI-Sentinel-Proof-Token", requirements.ProofToken)
	}
	if requirements.SOToken != "" {
		req.Header.Set("OpenAI-Sentinel-SO-Token", requirements.SOToken)
	}
	if requirements.TurnstileToken != "" {
		req.Header.Set("OpenAI-Sentinel-Turnstile-Token", requirements.TurnstileToken)
	}
	if conduitToken != "" {
		req.Header.Set("X-Conduit-Token", conduitToken)
		req.Header.Set("X-Oai-Turn-Trace-Id", uuid.NewString())
	}
	if err := fasthttp.DoTimeout(req, resp, 300*time.Second); err != nil {
		return nil, 0, err
	}
	out := append([]byte(nil), resp.Body()...)
	return out, resp.StatusCode(), nil
}

func conversationMessages(req *ir.Request) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(req.Instructions)+len(req.Turns))
	for _, ins := range req.Instructions {
		text := instructionText(ins)
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, webTextMessage(roleString(ins.Role), text))
	}
	for _, turn := range req.Turns {
		text, err := itemsText(turn.Items)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, webTextMessage(roleString(turn.Role), text))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("chatgpt reverse requires at least one text message")
	}
	return out, nil
}

func instructionText(ins ir.Instruction) string {
	if strings.TrimSpace(ins.Text) != "" {
		return ins.Text
	}
	text, _ := itemsText(ins.Items)
	return text
}

func itemsText(items []ir.Item) (string, error) {
	var b strings.Builder
	for _, item := range items {
		switch item.Kind {
		case ir.ItemText:
			if item.Text != nil {
				b.WriteString(item.Text.Text)
			}
		case ir.ItemToolResult, ir.ItemFunctionCallOutput:
			if item.ToolResult != nil {
				b.WriteString(item.ToolResult.OutputText)
			}
		default:
			return "", fmt.Errorf("chatgpt reverse currently supports text-only chat; unsupported item kind %q", item.Kind)
		}
	}
	return b.String(), nil
}

func webTextMessage(role, text string) map[string]interface{} {
	return map[string]interface{}{
		"id":     uuid.NewString(),
		"author": map[string]interface{}{"role": role},
		"content": map[string]interface{}{
			"content_type": "text",
			"parts":        []string{text},
		},
	}
}

func roleString(role ir.Role) string {
	switch role {
	case ir.RoleAssistant:
		return "assistant"
	case ir.RoleSystem:
		return "system"
	case ir.RoleDeveloper:
		return "system"
	case ir.RoleTool, ir.RoleFunction:
		return "tool"
	default:
		return "user"
	}
}

type requirements struct {
	Token           string
	ProofToken      string
	SOToken         string
	TurnstileToken  string
}

func (a *Adaptor) bootstrap() error {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(strings.TrimRight(a.baseURL(), "/") + "/")
	req.Header.SetMethod("GET")
	setBrowserHeaders(&req.Header, a.deviceID, a.sessionID)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	// Use a larger read buffer to handle chatgpt.com's large response headers
	client := &fasthttp.Client{
		ReadBufferSize: 32768, // 32KB to handle CSP and other security headers
	}
	if err := client.DoTimeout(req, resp, 30*time.Second); err != nil {
		return fmt.Errorf("chatgpt reverse bootstrap failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return fmt.Errorf("chatgpt reverse bootstrap status %d", resp.StatusCode())
	}
	a.scriptSource, a.dataBuild = parsePowResources(string(resp.Body()))
	return nil
}

func (a *Adaptor) chatRequirements(accessToken string) (requirements, error) {
	p := buildLegacyRequirementsToken(defaultUserAgent, a.scriptSource, a.dataBuild)
	path := "/backend-api/sentinel/chat-requirements"
	body, _ := json.Marshal(map[string]string{"p": p})
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(strings.TrimRight(a.baseURL(), "/") + path)
	req.Header.SetMethod("POST")
	req.SetBody(body)
	setBrowserHeaders(&req.Header, a.deviceID, a.sessionID)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenAI-Target-Path", path)
	req.Header.Set("X-OpenAI-Target-Route", path)
	if err := fasthttp.DoTimeout(req, resp, 30*time.Second); err != nil {
		return requirements{}, fmt.Errorf("chatgpt reverse requirements failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return requirements{}, fmt.Errorf("chatgpt reverse requirements status %d: %s", resp.StatusCode(), truncateBody(resp.Body()))
	}
	var raw struct {
		Token   string `json:"token"`
		SOToken string `json:"so_token"`
		Arkose  struct {
			Required bool `json:"required"`
		} `json:"arkose"`
		Turnstile struct {
			Required bool   `json:"required"`
			DX       string `json:"dx"`
		} `json:"turnstile"`
		ProofOfWork struct {
			Required   bool   `json:"required"`
			Seed       string `json:"seed"`
			Difficulty string `json:"difficulty"`
		} `json:"proofofwork"`
	}
	if err := json.Unmarshal(resp.Body(), &raw); err != nil {
		return requirements{}, fmt.Errorf("decode chatgpt reverse requirements: %w", err)
	}
	if raw.Arkose.Required {
		return requirements{}, fmt.Errorf("chatgpt reverse requirements requires arkose token")
	}
	if raw.Turnstile.Required {
		// Turnstile is optional - only required if dx field is present
		// If required but no dx, log warning but continue
		if raw.Turnstile.DX == "" {
			// No dx field, skip turnstile
		}
	}
	if raw.Token == "" {
		return requirements{}, fmt.Errorf("chatgpt reverse requirements missing token")
	}
	out := requirements{Token: raw.Token, SOToken: raw.SOToken}
	if raw.ProofOfWork.Required {
		token, err := buildProofToken(raw.ProofOfWork.Seed, raw.ProofOfWork.Difficulty, defaultUserAgent, a.scriptSource, a.dataBuild)
		if err != nil {
			return requirements{}, err
		}
		out.ProofToken = token
	}
	return out, nil
}

func (a *Adaptor) baseURL() string {
	base := upstreamconfig.AccountEndpoint(a.channel, a.account)
	if strings.TrimSpace(base) == "" {
		return defaultBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return defaultBaseURL
	}
	return parsed.Scheme + "://" + parsed.Host
}

func setBrowserHeaders(h *fasthttp.RequestHeader, deviceID, sessionID string) {
	h.Set("User-Agent", defaultUserAgent)
	h.Set("Origin", "https://chatgpt.com")
	h.Set("Referer", "https://chatgpt.com/")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7")
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
	h.Set("Priority", "u=1, i")
	h.Set("Sec-Ch-Ua", `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`)
	h.Set("Sec-Ch-Ua-Arch", `"x86"`)
	h.Set("Sec-Ch-Ua-Bitness", `"64"`)
	h.Set("Sec-Ch-Ua-Full-Version", `"143.0.3650.96"`)
	h.Set("Sec-Ch-Ua-Full-Version-List", `"Microsoft Edge";v="143.0.3650.96", "Chromium";v="143.0.7499.147", "Not A(Brand";v="24.0.0.0"`)
	h.Set("Sec-Ch-Ua-Mobile", "?0")
	h.Set("Sec-Ch-Ua-Model", `""`)
	h.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	h.Set("Sec-Ch-Ua-Platform-Version", `"19.0.0"`)
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("OAI-Device-Id", deviceID)
	h.Set("OAI-Session-Id", sessionID)
	h.Set("OAI-Language", "zh-CN")
	h.Set("OAI-Client-Version", "prod-a194cd50d4416d3c0b47c740f206b12ce60f5887")
	h.Set("OAI-Client-Build-Number", "6708908")
}

func parsePowResources(html string) ([]string, string) {
	reScript := regexp.MustCompile(`<script[^>]+src=["']([^"']+)["']`)
	matches := reScript.FindAllStringSubmatch(html, -1)
	sources := make([]string, 0, len(matches))
	dataBuild := ""
	reBuildPath := regexp.MustCompile(`c/[^/]*/_`)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		src := match[1]
		sources = append(sources, src)
		if dataBuild == "" {
			dataBuild = reBuildPath.FindString(src)
		}
	}
	if dataBuild == "" {
		reDataBuild := regexp.MustCompile(`<html[^>]*data-build=["']([^"']*)["']`)
		if match := reDataBuild.FindStringSubmatch(html); len(match) > 1 {
			dataBuild = match[1]
		}
	}
	if len(sources) == 0 {
		sources = []string{defaultPowScript}
	}
	return sources, dataBuild
}

func buildLegacyRequirementsToken(userAgent string, scripts []string, dataBuild string) string {
	seed := fmt.Sprintf("%.16f", mathrand.Float64())
	config := buildPowConfig(userAgent, scripts, dataBuild)
	answer, _ := powGenerate(seed, "0fffff", config, 500000)
	return "gAAAAAC" + answer
}

func buildProofToken(seed, difficulty, userAgent string, scripts []string, dataBuild string) (string, error) {
	answer, solved := powGenerate(seed, difficulty, buildPowConfig(userAgent, scripts, dataBuild), 500000)
	if !solved {
		return "", fmt.Errorf("failed to solve chatgpt reverse proof token difficulty=%s", difficulty)
	}
	return "gAAAAAB" + answer, nil
}

func buildPowConfig(userAgent string, scripts []string, dataBuild string) []interface{} {
	if len(scripts) == 0 {
		scripts = []string{defaultPowScript}
	}
	now := time.Now().In(time.FixedZone("EST", -5*3600))
	return []interface{}{
		[]int{3000, 4000, 5000}[mathrand.Intn(3)],
		now.Format("Mon Jan 02 2006 15:04:05") + " GMT-0500 (Eastern Standard Time)",
		4294705152,
		0,
		userAgent,
		scripts[mathrand.Intn(len(scripts))],
		dataBuild,
		"en-US",
		"en-US,es-US,en,es",
		0,
		"hardwareConcurrency−32",
		"location",
		"window",
		float64(time.Now().UnixNano()) / float64(time.Millisecond),
		uuid.NewString(),
		"",
		[]int{8, 16, 24, 32}[mathrand.Intn(4)],
		float64(time.Now().UnixNano())/float64(time.Millisecond) - math.Floor(float64(time.Now().UnixNano())/float64(time.Millisecond)),
	}
}

func powGenerate(seed, difficulty string, config []interface{}, limit int) (string, bool) {
	target, err := hex.DecodeString(difficulty)
	if err != nil || len(target) == 0 {
		return "", false
	}
	diffLen := len(difficulty) / 2
	static1 := mustCompactJSON(config[:3])
	static1 = static1[:len(static1)-1] + ","
	static2 := "," + strings.TrimSuffix(strings.TrimPrefix(mustCompactJSON(config[4:9]), "["), "]") + ","
	static3 := "," + strings.TrimPrefix(mustCompactJSON(config[10:]), "[")
	seedBytes := []byte(seed)
	for i := 0; i < limit; i++ {
		finalJSON := []byte(static1 + fmt.Sprintf("%d", i) + static2 + fmt.Sprintf("%d", i>>1) + static3)
		encoded := []byte(base64.StdEncoding.EncodeToString(finalJSON))
		digest := sha3.Sum512(append(seedBytes, encoded...))
		if bytes.Compare(digest[:diffLen], target) <= 0 {
			return string(encoded), true
		}
	}
	fallback := "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%q", seed)))
	return fallback, false
}

func mustCompactJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func metadataString(account *db.Account, key string) string {
	if account == nil || account.Metadata == nil {
		return ""
	}
	value, _ := account.Metadata[key].(string)
	return strings.TrimSpace(value)
}

func parseOpenAIUsage(body []byte) (int, int, error) {
	var resp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, err
	}
	pt, ct := resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	if pt == 0 && ct == 0 {
		pt, ct = resp.Usage.InputTokens, resp.Usage.OutputTokens
	}
	return pt, ct, nil
}

func extractImageIDsFromSSE(body []byte) (string, []string, []string, error) {
	conversationID := ""
	var fileIDs []string
	var sedimentIDs []string
	for _, event := range strings.Split(string(body), "\n\n") {
		for _, line := range strings.Split(event, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			if id := conversationIDFromPayload(payload); id != "" {
				conversationID = id
			}
			files, sediments := imageIDsFromPayload(payload)
			fileIDs = appendUnique(fileIDs, files...)
			sedimentIDs = appendUnique(sedimentIDs, sediments...)
		}
	}
	return conversationID, fileIDs, sedimentIDs, nil
}

func conversationIDFromPayload(payload string) string {
	var root interface{}
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		return ""
	}
	return findStringKey(root, "conversation_id")
}

func imageIDsFromPayload(payload string) ([]string, []string) {
	fileRE := regexp.MustCompile(`file-service://([A-Za-z0-9_-]+)|\b(file_[A-Za-z0-9_-]+)\b`)
	sedimentRE := regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`)
	fileMatches := fileRE.FindAllStringSubmatch(payload, -1)
	sedimentMatches := sedimentRE.FindAllStringSubmatch(payload, -1)
	var files []string
	var sediments []string
	for _, match := range fileMatches {
		id := ""
		if len(match) > 1 && match[1] != "" {
			id = match[1]
		} else if len(match) > 2 {
			id = match[2]
		}
		if id != "" && id != "file_upload" {
			files = appendUnique(files, id)
		}
	}
	for _, match := range sedimentMatches {
		if len(match) > 1 && match[1] != "" {
			sediments = appendUnique(sediments, match[1])
		}
	}
	return files, sediments
}

func findStringKey(value interface{}, key string) string {
	switch v := value.(type) {
	case map[string]interface{}:
		if s, _ := v[key].(string); s != "" {
			return s
		}
		for _, child := range v {
			if s := findStringKey(child, key); s != "" {
				return s
			}
		}
	case []interface{}:
		for _, child := range v {
			if s := findStringKey(child, key); s != "" {
				return s
			}
		}
	}
	return ""
}

func (a *Adaptor) pollImageConversation(conversationID, accessToken string) ([]string, []string) {
	path := "/backend-api/conversation/" + url.PathEscape(conversationID)
	route := "/backend-api/conversation/{conversation_id}"
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		req.SetRequestURI(strings.TrimRight(a.baseURL(), "/") + path)
		req.Header.SetMethod("GET")
		setBrowserHeaders(&req.Header, a.deviceID, a.sessionID)
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Referer", strings.TrimRight(a.baseURL(), "/")+"/c/"+conversationID)
		req.Header.Set("X-OpenAI-Target-Path", path)
		req.Header.Set("X-OpenAI-Target-Route", route)
		err := fasthttp.DoTimeout(req, resp, 30*time.Second)
		body := append([]byte(nil), resp.Body()...)
		status := resp.StatusCode()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		if err == nil && status < 400 {
			files, sediments := imageIDsFromPayload(string(body))
			if len(files) > 0 || len(sediments) > 0 {
				return files, sediments
			}
		}
		time.Sleep(2 * time.Second)
	}
	return nil, nil
}

func (a *Adaptor) resolveFileDownloadURL(fileID, accessToken string) string {
	path := "/backend-api/files/" + url.PathEscape(fileID) + "/download"
	return a.resolveDownloadURL(path, path, accessToken)
}

func (a *Adaptor) resolveAttachmentDownloadURL(conversationID, attachmentID, accessToken string) string {
	path := "/backend-api/conversation/" + url.PathEscape(conversationID) + "/attachment/" + url.PathEscape(attachmentID) + "/download"
	return a.resolveDownloadURL(path, "/backend-api/conversation/{conversation_id}/attachment/{attachment_id}/download", accessToken)
}

func (a *Adaptor) resolveDownloadURL(path, route, accessToken string) string {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(strings.TrimRight(a.baseURL(), "/") + path)
	req.Header.SetMethod("GET")
	setBrowserHeaders(&req.Header, a.deviceID, a.sessionID)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-OpenAI-Target-Path", path)
	req.Header.Set("X-OpenAI-Target-Route", route)
	if err := fasthttp.DoTimeout(req, resp, 30*time.Second); err != nil || resp.StatusCode() >= 400 {
		return ""
	}
	var out map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		return ""
	}
	for _, key := range []string{"download_url", "url"} {
		if s, _ := out[key].(string); s != "" {
			return s
		}
	}
	return ""
}

func downloadImageBase64(imageURL string) string {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(imageURL)
	req.Header.SetMethod("GET")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	req.Header.Set("User-Agent", defaultUserAgent)
	if err := fasthttp.DoTimeout(req, resp, 120*time.Second); err != nil || resp.StatusCode() >= 400 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(resp.Body())
}

func appendUnique[T comparable](values []T, extra ...T) []T {
	seen := make(map[T]struct{}, len(values)+len(extra))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range extra {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func truncateBody(body []byte) string {
	if len(body) > 512 {
		body = body[:512]
	}
	return string(body)
}

func init() {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		mathrand.Seed(int64(binaryLittleEndian(b[:])))
	}
}

func binaryLittleEndian(b []byte) uint64 {
	var out uint64
	for i := len(b) - 1; i >= 0; i-- {
		out <<= 8
		out |= uint64(b[i])
	}
	return out
}

var _ provider.Adaptor = (*Adaptor)(nil)
