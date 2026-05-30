package antigravity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

type AntigravityAdaptor struct {
	channel  *db.Channel
	account  *db.Account
	model    string
	isStream bool
}

type ChannelSettings struct {
	ThinkingRouting      bool        `json:"thinking_routing"`
	TierFallback         bool        `json:"tier_fallback"`
	MediumTokenThreshold int         `json:"medium_token_threshold"`
	LongTokenThreshold   int         `json:"long_token_threshold"`
	TierGroups           []TierGroup `json:"tier_groups"`
}

type TierGroup struct {
	PublicModel   string   `json:"public_model"`
	Aliases       []string `json:"aliases,omitempty"`
	High          string   `json:"high,omitempty"`
	Medium        string   `json:"medium,omitempty"`
	Low           string   `json:"low,omitempty"`
	FallbackOrder []string `json:"fallback_order,omitempty"`
}

func DefaultChannelSettings() ChannelSettings {
	return ChannelSettings{
		ThinkingRouting:      false,
		TierFallback:         false,
		MediumTokenThreshold: 8000,
		LongTokenThreshold:   32000,
		TierGroups:           DefaultTierGroups(),
	}
}

func ParseChannelSettings(raw string) ChannelSettings {
	settings := DefaultChannelSettings()
	if strings.TrimSpace(raw) == "" {
		return settings
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return settings
	}
	if value, ok := decoded["thinking_routing"].(bool); ok {
		settings.ThinkingRouting = value
	}
	if value, ok := decoded["tier_fallback"].(bool); ok {
		settings.TierFallback = value
	}
	if value, ok := intSetting(decoded["medium_token_threshold"]); ok && value > 0 {
		settings.MediumTokenThreshold = value
	}
	if value, ok := intSetting(decoded["long_token_threshold"]); ok && value > 0 {
		settings.LongTokenThreshold = value
	}
	if groups, ok := tierGroupsSetting(decoded["tier_groups"]); ok {
		settings.TierGroups = groups
	}
	if settings.LongTokenThreshold <= settings.MediumTokenThreshold {
		settings.LongTokenThreshold = settings.MediumTokenThreshold + 1
	}
	return settings
}

func (a *AntigravityAdaptor) Init(channel *db.Channel, account *db.Account) {
	a.channel = channel
	a.account = account
}

func (a *AntigravityAdaptor) SetRequestParams(model string, stream bool) {
	a.model = model
	a.isStream = stream
}

func (a *AntigravityAdaptor) GetRequestURL(path string) (string, error) {
	base := strings.TrimRight(upstreamconfig.AccountEndpoint(a.channel, a.account), "/")
	if base == "" {
		base = APIEndpoint
	}
	if a.isStream {
		return base + "/" + APIVersion + ":streamGenerateContent?alt=sse", nil
	}
	return base + "/" + APIVersion + ":generateContent", nil
}

func (a *AntigravityAdaptor) SetupRequestHeader(req *fasthttp.Request, credentials string) error {
	credential := provider.ExtractCredentialKey(credentials)
	if credential == "" {
		return fmt.Errorf("missing antigravity access token")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("User-Agent", RequestUserAgent())
	return nil
}

func (a *AntigravityAdaptor) ToInternal(body []byte) (*provider.InternalRequest, error) {
	ir, err := convert.ToInternalOnly(convert.FormatGeminiCLI, body)
	if err != nil {
		return nil, err
	}
	return provider.ToProviderInternal(ir), nil
}

func (a *AntigravityAdaptor) FromInternal(req *provider.InternalRequest) ([]byte, error) {
	clientModel := req.Model
	if clientModel == "" {
		clientModel = a.model
	}
	settings := DefaultChannelSettings()
	if a.channel != nil {
		settings = ParseChannelSettings(a.channel.Settings)
	}
	effort := antigravityReasoningEffort(req)
	model := ResolveRequestModelWithSettings(clientModel, effort, antigravityRequestSize(req, settings), settings, channelModels(a.channel))
	reqCopy := *req
	reqCopy.Model = model
	gemBody, err := convert.InternalToGemini(provider.FromProviderInternal(&reqCopy))
	if err != nil {
		return nil, err
	}
	var vertexReq map[string]interface{}
	if err := provider.DecodeJSONUseNumber(gemBody, &vertexReq); err != nil {
		return nil, err
	}
	delete(vertexReq, "safetySettings")
	body := map[string]interface{}{
		"model":       model,
		"userAgent":   "antigravity",
		"requestType": antigravityRequestType(model),
		"requestId":   "agent-" + uuid.NewString(),
		"request":     vertexReq,
	}
	if projectID := antigravityProjectID(a.account); projectID != "" {
		body["project"] = projectID
	}
	request, _ := body["request"].(map[string]interface{})
	if request != nil {
		request["sessionId"] = stableSessionID(vertexReq)
		hasFunctionDeclarations := normalizeAntigravityTools(request)
		if hasFunctionDeclarations || (strings.Contains(model, "claude") && request["toolConfig"] != nil) {
			ensureFunctionCallingValidated(request)
		}
		if !strings.Contains(model, "claude") {
			if gc, ok := request["generationConfig"].(map[string]interface{}); ok {
				delete(gc, "maxOutputTokens")
			}
		}
	}
	return json.Marshal(body)
}

func channelModels(ch *db.Channel) []string {
	if ch == nil {
		return nil
	}
	return strings.Split(ch.Models, ",")
}

func antigravityReasoningEffort(req *provider.InternalRequest) string {
	if req == nil {
		return ""
	}
	if effort := stringFromAnyPath(req.Reasoning, "effort"); effort != "" {
		return effort
	}
	if effort := stringFromAnyPath(req.Thinking, "thinkingLevel"); effort != "" {
		return effort
	}
	if effort := stringFromAnyPath(req.Thinking, "effort"); effort != "" {
		return effort
	}
	if req.ExtraParams != nil {
		if effort := stringFromAnyPath(req.ExtraParams["reasoning_effort"]); effort != "" {
			return effort
		}
		if effort := stringFromAnyPath(req.ExtraParams["reasoning"], "effort"); effort != "" {
			return effort
		}
	}
	return ""
}

func stringFromAnyPath(value interface{}, path ...string) string {
	if value == nil {
		return ""
	}
	if raw, ok := value.(json.RawMessage); ok {
		var decoded interface{}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return strings.TrimSpace(string(raw))
		}
		value = decoded
	}
	if len(path) == 0 {
		if s, ok := value.(string); ok {
			return strings.ToLower(strings.TrimSpace(s))
		}
		return ""
	}
	current := value
	for _, key := range path {
		m, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current = m[key]
	}
	if s, ok := current.(string); ok {
		return strings.ToLower(strings.TrimSpace(s))
	}
	return ""
}

func intSetting(value interface{}) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		i, err := strconv.Atoi(v.String())
		return i, err == nil
	default:
		return 0, false
	}
}

func tierGroupsSetting(value interface{}) ([]TierGroup, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var groups []TierGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, false
	}
	out := make([]TierGroup, 0, len(groups))
	for _, group := range groups {
		group.PublicModel = strings.TrimSpace(group.PublicModel)
		group.High = strings.TrimSpace(group.High)
		group.Medium = strings.TrimSpace(group.Medium)
		group.Low = strings.TrimSpace(group.Low)
		group.Aliases = cleanStrings(group.Aliases)
		group.FallbackOrder = cleanStrings(group.FallbackOrder)
		if group.PublicModel == "" || (group.High == "" && group.Medium == "" && group.Low == "") {
			continue
		}
		out = append(out, group)
	}
	return out, true
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func antigravityRequestSize(req *provider.InternalRequest, settings ChannelSettings) string {
	tokens := estimateAntigravityTokens(req)
	switch {
	case tokens > settings.LongTokenThreshold:
		return "long"
	case tokens > settings.MediumTokenThreshold:
		return "medium"
	default:
		return "short"
	}
}

func estimateAntigravityTokens(req *provider.InternalRequest) int {
	if req == nil {
		return 0
	}
	chars := 0
	if req.Instructions != nil {
		chars += len(*req.Instructions)
	}
	for _, msg := range req.Messages {
		for _, part := range msg.Content {
			switch {
			case part.Text != "":
				chars += len(part.Text)
			case part.ImageURL != nil:
				chars += 4096
			default:
				chars += len(part.Type)
			}
		}
		for _, call := range msg.ToolCalls {
			chars += len(call.Name) + len(call.Arguments)
		}
		if msg.ToolResult != nil {
			chars += len(msg.ToolResult.Content)
		}
	}
	tokens := chars / 4
	if req.MaxTokens != nil {
		tokens += *req.MaxTokens
	}
	return tokens
}

func normalizeAntigravityTools(request map[string]interface{}) bool {
	tools, ok := request["tools"].([]interface{})
	if !ok {
		return false
	}
	hasFunctionDeclarations := false
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]interface{})
		if !ok {
			continue
		}
		if snakeDecls, ok := tool["function_declarations"]; ok {
			if _, exists := tool["functionDeclarations"]; !exists {
				tool["functionDeclarations"] = snakeDecls
			}
			delete(tool, "function_declarations")
		}
		rawDecls, ok := tool["functionDeclarations"].([]interface{})
		if !ok {
			continue
		}
		decls := make([]interface{}, 0, len(rawDecls))
		for _, rawDecl := range rawDecls {
			decl, ok := rawDecl.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := decl["name"].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			normalizeAntigravityFunctionDeclaration(decl)
			decls = append(decls, decl)
		}
		if len(decls) == 0 {
			delete(tool, "functionDeclarations")
			continue
		}
		tool["functionDeclarations"] = decls
		hasFunctionDeclarations = true
	}
	return hasFunctionDeclarations
}

func normalizeAntigravityFunctionDeclaration(decl map[string]interface{}) {
	for _, key := range []string{"format", "strict", "additionalProperties", "type", "external_web_access"} {
		delete(decl, key)
	}
	params, hasParams := decl["parameters"]
	if !hasParams || params == nil {
		if jsonSchema, ok := decl["parametersJsonSchema"]; ok && jsonSchema != nil {
			params = jsonSchema
			hasParams = true
		}
	}
	delete(decl, "parametersJsonSchema")
	if !hasParams || params == nil {
		params = map[string]interface{}{
			"type":       "OBJECT",
			"properties": map[string]interface{}{},
		}
	}
	params = normalizeAntigravitySchemaValue(params, true)
	if paramsMap, ok := params.(map[string]interface{}); ok {
		if _, ok := paramsMap["type"]; !ok {
			paramsMap["type"] = "OBJECT"
		}
		if _, ok := paramsMap["properties"]; !ok {
			paramsMap["properties"] = map[string]interface{}{}
		}
	}
	decl["parameters"] = params
}

func normalizeAntigravitySchemaValue(value interface{}, root bool) interface{} {
	switch v := value.(type) {
	case json.RawMessage:
		var decoded interface{}
		if err := provider.DecodeJSONUseNumber(v, &decoded); err == nil {
			return normalizeAntigravitySchemaValue(decoded, root)
		}
		return map[string]interface{}{"type": "OBJECT", "properties": map[string]interface{}{}}
	case map[string]interface{}:
		for _, key := range []string{
			"$schema",
			"$id",
			"$defs",
			"definitions",
			"title",
			"default",
			"examples",
			"format",
			"additionalProperties",
			"external_web_access",
		} {
			delete(v, key)
		}
		if rawType, ok := v["type"]; ok {
			if normalized, keep := normalizeAntigravitySchemaType(rawType); keep {
				v["type"] = normalized
			} else {
				delete(v, "type")
			}
		}
		for _, key := range []string{"properties", "patternProperties"} {
			rawProperties, ok := v[key].(map[string]interface{})
			if !ok {
				continue
			}
			for prop, rawValue := range rawProperties {
				rawProperties[prop] = normalizeAntigravitySchemaValue(rawValue, false)
			}
			if key == "patternProperties" {
				delete(v, key)
			}
		}
		if rawItems, ok := v["items"]; ok {
			v["items"] = normalizeAntigravitySchemaValue(rawItems, false)
		}
		for _, key := range []string{"anyOf", "oneOf", "allOf"} {
			rawList, ok := v[key].([]interface{})
			if !ok {
				continue
			}
			for i, rawItem := range rawList {
				rawList[i] = normalizeAntigravitySchemaValue(rawItem, false)
			}
		}
		if root {
			if _, ok := v["type"]; !ok {
				v["type"] = "OBJECT"
			}
		}
		return v
	case []interface{}:
		for i, rawItem := range v {
			v[i] = normalizeAntigravitySchemaValue(rawItem, false)
		}
		return v
	default:
		return value
	}
}

func normalizeAntigravitySchemaType(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" || strings.EqualFold(v, "null") {
			return "", false
		}
		return strings.ToUpper(v), true
	case []interface{}:
		for _, raw := range v {
			s, ok := raw.(string)
			if !ok || strings.EqualFold(strings.TrimSpace(s), "null") {
				continue
			}
			return strings.ToUpper(strings.TrimSpace(s)), true
		}
		return "", false
	default:
		return "", false
	}
}

func (a *AntigravityAdaptor) ParseUsage(respBody []byte) (int, int, error) {
	usage, err := a.ParseUsageFull(respBody)
	return usage.PromptTokens, usage.CompletionTokens, err
}

func (a *AntigravityAdaptor) ParseStreamUsage(lastChunk []byte) (int, int, error) {
	var resp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(lastChunk, &resp); err == nil && resp.Usage.PromptTokens > 0 {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, nil
	}
	return 0, 0, nil
}

func (a *AntigravityAdaptor) ParseUsageFull(respBody []byte) (provider.InternalUsage, error) {
	internal, err := convert.GeminiResponseToInternal(respBody)
	if err != nil {
		return provider.InternalUsage{}, err
	}
	return provider.InternalUsage{
		PromptTokens:             internal.Usage.PromptTokens,
		CompletionTokens:         internal.Usage.CompletionTokens,
		CacheCreationInputTokens: internal.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     internal.Usage.CacheReadInputTokens,
	}, nil
}

func (a *AntigravityAdaptor) GetChannelType() string { return "antigravity" }

func antigravityRequestType(model string) string {
	if strings.Contains(strings.ToLower(model), "image") {
		return "image_gen"
	}
	return "agent"
}

func antigravityProjectID(account *db.Account) string {
	if account == nil || account.Metadata == nil {
		return ""
	}
	if project, ok := account.Metadata["project_id"].(string); ok {
		return strings.TrimSpace(project)
	}
	if loadRes, ok := account.Metadata["load_code_assist"].(map[string]interface{}); ok {
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

func ProjectID(account *db.Account) string {
	return antigravityProjectID(account)
}

func stableSessionID(req map[string]interface{}) string {
	contents, _ := req["contents"].([]interface{})
	for _, raw := range contents {
		content, _ := raw.(map[string]interface{})
		if role, _ := content["role"].(string); role != "user" {
			continue
		}
		parts, _ := content["parts"].([]interface{})
		if len(parts) == 0 {
			continue
		}
		part, _ := parts[0].(map[string]interface{})
		text, _ := part["text"].(string)
		if text == "" {
			continue
		}
		h := sha256.Sum256([]byte(text))
		n := int64(binary.BigEndian.Uint64(h[:8])) & 0x7fffffffffffffff
		return "-" + strconv.FormatInt(n, 10)
	}
	return "-" + strconv.FormatInt(time.Now().UnixNano()&0x7fffffffffffffff, 10)
}

func ensureFunctionCallingValidated(request map[string]interface{}) {
	toolConfig, _ := request["toolConfig"].(map[string]interface{})
	if toolConfig == nil {
		toolConfig = map[string]interface{}{}
		request["toolConfig"] = toolConfig
	}
	fcc, _ := toolConfig["functionCallingConfig"].(map[string]interface{})
	if fcc == nil {
		fcc = map[string]interface{}{}
		toolConfig["functionCallingConfig"] = fcc
	}
	mode, _ := fcc["mode"].(string)
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "", "AUTO", "MODE_UNSPECIFIED":
	default:
		return
	}
	fcc["mode"] = "VALIDATED"
}
