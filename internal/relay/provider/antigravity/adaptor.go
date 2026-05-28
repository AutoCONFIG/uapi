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
	model := UpstreamModelID(clientModel)
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
		if _, ok := request["toolConfig"]; ok && strings.Contains(model, "claude") {
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
		return
	}
	fcc, _ := toolConfig["functionCallingConfig"].(map[string]interface{})
	if fcc == nil {
		fcc = map[string]interface{}{}
		toolConfig["functionCallingConfig"] = fcc
	}
	fcc["mode"] = "VALIDATED"
}
