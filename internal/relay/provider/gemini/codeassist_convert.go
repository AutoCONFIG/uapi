package gemini

import (
	"encoding/json"
	"strconv"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

func internalToGeminiCodeAssistWithAccount(req *provider.InternalRequest, account *db.Account) ([]byte, error) {
	model := resolveCodeAssistModel(req.Model)
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
