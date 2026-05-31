package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/google/uuid"
)

func internalToGeminiCodeAssistWithAccount(req *ir.Request, account *db.Account) ([]byte, error) {
	model := resolveCodeAssistModel(req.Model)
	reqCopy := *req
	reqCopy.Model = model
	reqCopy.TargetProtocol = ir.ProtocolGemini
	gemBody, err := convert.FromIR(&reqCopy, convert.FormatGemini)
	if err != nil {
		return nil, err
	}
	var vertexReq map[string]interface{}
	if err := provider.DecodeJSONUseNumber(gemBody, &vertexReq); err != nil {
		return nil, err
	}
	if _, ok := vertexReq["session_id"]; !ok {
		vertexReq["session_id"] = codeAssistSessionID(req, account)
	}
	body := map[string]interface{}{
		"model":          model,
		"user_prompt_id": provider.RandomHex(16),
		"request":        vertexReq,
	}
	if projectID := codeAssistProjectID(account); projectID != "" {
		body["project"] = projectID
	}
	if shouldUseGoogleOneCredits(account, model) {
		body["enabled_credit_types"] = []string{"GOOGLE_ONE_AI"}
	}
	return json.Marshal(body)
}

func resolveCodeAssistModel(model string) string {
	switch model {
	case "", "auto", "auto-gemini-2.5", "pro":
		return "gemini-2.5-pro"
	case "flash":
		return "gemini-2.5-flash"
	case "flash-lite":
		return "gemini-2.5-flash-lite"
	case "auto-gemini-3":
		return "gemini-3-pro-preview"
	default:
		return model
	}
}

func shouldUseGoogleOneCredits(account *db.Account, model string) bool {
	if !isOverageEligibleModel(model) || account == nil || account.Metadata == nil {
		return false
	}
	paidTier, ok := account.Metadata["paid_tier"].(map[string]interface{})
	if !ok {
		return false
	}
	credits, ok := paidTier["availableCredits"].([]interface{})
	if !ok {
		return false
	}
	for _, item := range credits {
		credit, ok := item.(map[string]interface{})
		if !ok || credit["creditType"] != "GOOGLE_ONE_AI" {
			continue
		}
		if amount, ok := credit["creditAmount"].(string); ok {
			parsed, err := strconv.Atoi(amount)
			if err == nil && parsed >= 50 {
				return true
			}
		}
	}
	return false
}

func codeAssistSessionID(req *ir.Request, account *db.Account) string {
	if req != nil {
		for _, key := range []string{"session_id", "sessionId"} {
			if value := strings.TrimSpace(rawStringFromMaps(key, req.Metadata, req.Native.Fields, req.Native.Unknown)); value != "" {
				return value
			}
		}
	}
	if account != nil && account.Metadata != nil {
		for _, key := range []string{"session_id", "sessionId"} {
			if value := strings.TrimSpace(stringFromAny(account.Metadata[key])); value != "" {
				return value
			}
		}
	}
	seed := codeAssistSessionSeed(req, account)
	if seed == "" {
		return "uapi-" + provider.RandomHex(8)
	}
	sum := sha256.Sum256([]byte(seed))
	return "uapi-" + hex.EncodeToString(sum[:8])
}

func codeAssistSessionSeed(req *ir.Request, account *db.Account) string {
	var parts []string
	if account != nil {
		if account.ID != uuid.Nil {
			parts = append(parts, account.ID.String())
		}
		if account.Name != "" {
			parts = append(parts, account.Name)
		}
	}
	if req != nil {
		for _, inst := range req.Instructions {
			if inst.Text != "" {
				parts = append(parts, inst.Text)
				break
			}
		}
		for _, turn := range req.Turns {
			if !strings.EqualFold(string(turn.Role), "user") {
				continue
			}
			if text := firstTurnText(turn); text != "" {
				parts = append(parts, text)
				return strings.Join(parts, "\n")
			}
		}
	}
	return strings.Join(parts, "\n")
}

func firstTurnText(turn ir.Turn) string {
	for _, item := range turn.Items {
		if item.Text != nil && item.Text.Text != "" {
			return item.Text.Text
		}
	}
	return ""
}

func rawStringFromMaps(key string, maps ...map[string]json.RawMessage) string {
	for _, values := range maps {
		if value := stringFromAny(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringFromAny(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case json.RawMessage:
		var out string
		if err := json.Unmarshal(v, &out); err == nil {
			return out
		}
		return strings.Trim(string(v), `"`)
	default:
		return ""
	}
}

func isOverageEligibleModel(model string) bool {
	switch model {
	case "gemini-3-pro-preview", "gemini-3.1-pro-preview", "gemini-3-flash-preview":
		return true
	default:
		return false
	}
}

func codeAssistProjectID(account *db.Account) string {
	if account == nil || account.Metadata == nil {
		return ""
	}
	if project, ok := account.Metadata["project_id"].(string); ok {
		return project
	}
	if loadRes, ok := account.Metadata["load_code_assist"].(map[string]interface{}); ok {
		if project, ok := loadRes["cloudaicompanionProject"].(string); ok {
			return project
		}
		if project, ok := loadRes["cloudaicompanionProject"].(map[string]interface{}); ok {
			if id, ok := project["id"].(string); ok {
				return id
			}
		}
	}
	return ""
}
