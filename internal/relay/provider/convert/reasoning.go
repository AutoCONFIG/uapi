package convert

import (
	"encoding/json"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

const (
	reasoningExtraSignature        = "signature"
	reasoningExtraThoughtSignature = "thoughtSignature"
	reasoningExtraEncryptedContent = "encrypted_content"
	reasoningExtraData             = "data"
	reasoningExtraID               = "id"
	reasoningExtraType             = "reasoning_type"

	reasoningDetailTypeText      = "reasoning.text"
	reasoningDetailTypeSummary   = "reasoning.summary"
	reasoningDetailTypeEncrypted = "reasoning.encrypted"
)

func reasoningPart(text string) schema.ContentPart {
	return schema.ContentPart{Type: "thinking", Text: text}
}

func reasoningPartWithExtra(text string, extra map[string]json.RawMessage) schema.ContentPart {
	return schema.ContentPart{Type: "thinking", Text: text, Extra: extra}
}

func reasoningTextFromExtra(extra map[string]json.RawMessage) string {
	for _, key := range []string{"reasoning_content", "reasoning"} {
		if raw, ok := extra[key]; ok {
			if text := rawString(raw); text != "" {
				return text
			}
		}
	}
	return ""
}

func reasoningPartsFromOpenAIChatExtra(extra map[string]json.RawMessage) []schema.ContentPart {
	var parts []schema.ContentPart
	if raw, ok := extra["reasoning_details"]; ok {
		var details []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &details); err == nil {
			for _, detail := range details {
				text := firstRawString(detail, "text", "reasoning", "summary")
				partExtra := map[string]json.RawMessage{}
				if typ := firstRawString(detail, "type"); typ != "" {
					partExtra[reasoningExtraType], _ = json.Marshal(typ)
				}
				for _, key := range []string{reasoningExtraID, reasoningExtraSignature, reasoningExtraThoughtSignature, reasoningExtraEncryptedContent, reasoningExtraData} {
					if rawValue, ok := detail[key]; ok && rawString(rawValue) != "" {
						partExtra[key] = rawValue
					}
				}
				if _, hasEncrypted := partExtra[reasoningExtraEncryptedContent]; !hasEncrypted {
					if rawValue, ok := partExtra[reasoningExtraData]; ok && rawString(rawValue) != "" {
						partExtra[reasoningExtraEncryptedContent] = rawValue
					}
				}
				if text != "" || len(partExtra) > 0 {
					parts = append(parts, reasoningPartWithExtra(text, partExtra))
				}
			}
		}
	}
	if len(parts) == 0 {
		if reasoning := reasoningTextFromExtra(extra); reasoning != "" {
			parts = append(parts, reasoningPart(reasoning))
		}
	}
	return parts
}

func firstRawString(values map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			if text := rawString(raw); text != "" {
				return text
			}
		}
	}
	return ""
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return ""
}

func setRawString(extra map[string]json.RawMessage, key, value string) map[string]json.RawMessage {
	if value == "" {
		return extra
	}
	if extra == nil {
		extra = make(map[string]json.RawMessage)
	}
	raw, _ := json.Marshal(value)
	extra[key] = raw
	return extra
}

func reasoningPartsFromResponsesExtra(extra map[string]json.RawMessage) []schema.ContentPart {
	if len(extra) == 0 {
		return nil
	}
	var parts []schema.ContentPart
	seenText := map[string]bool{}
	if raw, ok := extra["content"]; ok {
		var content []struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Signature string `json:"signature"`
			Data      string `json:"data"`
		}
		if err := json.Unmarshal(raw, &content); err == nil {
			for _, item := range content {
				if item.Text == "" && item.Signature == "" && item.Data == "" {
					continue
				}
				partExtra := map[string]json.RawMessage{}
				if item.Type != "" {
					partExtra[reasoningExtraType], _ = json.Marshal(reasoningDetailTypeText)
				}
				if item.Signature != "" {
					partExtra = setRawString(partExtra, reasoningExtraSignature, item.Signature)
				}
				if item.Data != "" {
					partExtra = setRawString(partExtra, reasoningExtraEncryptedContent, item.Data)
					partExtra = setRawString(partExtra, reasoningExtraData, item.Data)
				}
				parts = append(parts, reasoningPartWithExtra(item.Text, partExtra))
				if item.Text != "" {
					seenText[item.Text] = true
				}
			}
		}
	}
	if raw, ok := extra["summary"]; ok {
		var summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &summary); err == nil {
			for _, item := range summary {
				if item.Text == "" {
					continue
				}
				if seenText[item.Text] {
					continue
				}
				parts = append(parts, reasoningPartWithExtra(item.Text, map[string]json.RawMessage{
					reasoningExtraType: json.RawMessage(`"reasoning.summary"`),
				}))
			}
		}
	}
	if raw, ok := extra[reasoningExtraEncryptedContent]; ok {
		if encrypted := rawString(raw); encrypted != "" {
			parts = append(parts, reasoningPartWithExtra("", map[string]json.RawMessage{
				reasoningExtraEncryptedContent: raw,
				reasoningExtraData:             raw,
				reasoningExtraType:             json.RawMessage(`"reasoning.encrypted"`),
			}))
		}
	}
	return parts
}

func reasoningDetailsFromParts(parts []schema.ContentPart) []map[string]interface{} {
	var details []map[string]interface{}
	for idx, part := range parts {
		typ := reasoningPartExtraString(part, reasoningExtraType)
		if typ == "" {
			typ = reasoningDetailTypeText
		}
		if part.Text == "" && reasoningPartEncryptedData(part) != "" {
			typ = reasoningDetailTypeEncrypted
		}
		detail := map[string]interface{}{
			"index": idx,
			"type":  typ,
		}
		if part.Text != "" {
			if typ == reasoningDetailTypeSummary {
				detail["summary"] = part.Text
			} else {
				detail["text"] = part.Text
			}
		}
		if id := reasoningPartExtraString(part, reasoningExtraID); id != "" {
			detail["id"] = id
		}
		if part.Extra != nil {
			if sig := reasoningSignature([]schema.ContentPart{part}); sig != "" {
				detail["signature"] = sig
			}
			if encrypted := reasoningPartEncryptedData(part); encrypted != "" {
				detail["data"] = encrypted
				detail["encrypted_content"] = encrypted
			}
		}
		if _, hasText := detail["text"]; hasText || len(detail) > 2 {
			details = append(details, detail)
		}
	}
	return details
}

func responsesReasoningSummary(parts []schema.ContentPart) []map[string]interface{} {
	var summary []map[string]interface{}
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		summary = append(summary, map[string]interface{}{
			"type": "summary_text",
			"text": part.Text,
		})
	}
	return summary
}

func reasoningEncryptedContent(parts []schema.ContentPart) string {
	for _, part := range parts {
		if encrypted := reasoningPartEncryptedData(part); encrypted != "" {
			return encrypted
		}
	}
	return ""
}

func reasoningPartEncryptedData(part schema.ContentPart) string {
	if part.Extra == nil {
		return ""
	}
	keys := []string{reasoningExtraEncryptedContent, reasoningExtraData, reasoningExtraThoughtSignature}
	if reasoningPartExtraString(part, reasoningExtraType) == reasoningDetailTypeEncrypted {
		keys = append(keys, reasoningExtraSignature)
	}
	for _, key := range keys {
		if raw, ok := part.Extra[key]; ok {
			if text := rawString(raw); text != "" {
				return text
			}
		}
	}
	return ""
}

func reasoningPartExtraString(part schema.ContentPart, key string) string {
	if part.Extra == nil {
		return ""
	}
	if raw, ok := part.Extra[key]; ok {
		return rawString(raw)
	}
	return ""
}

func reasoningSignature(parts []schema.ContentPart) string {
	for _, part := range parts {
		if part.Extra == nil {
			continue
		}
		if reasoningPartExtraString(part, reasoningExtraType) == reasoningDetailTypeEncrypted {
			continue
		}
		for _, key := range []string{reasoningExtraSignature, reasoningExtraThoughtSignature} {
			if raw, ok := part.Extra[key]; ok {
				if text := rawString(raw); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func reasoningOpaqueSignature(parts []schema.ContentPart) string {
	if sig := reasoningSignature(parts); sig != "" {
		return sig
	}
	return reasoningEncryptedContent(parts)
}

func normalizeAnyGeminiThinkingConfig(raw json.RawMessage) interface{} {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg) == 0 {
		return normalizeGeminiThinkingConfig(raw)
	}
	if typeValue := strings.ToLower(strings.TrimSpace(stringFromMap(cfg, "type"))); typeValue != "" {
		switch typeValue {
		case "disabled", "none":
			return map[string]interface{}{"thinkingBudget": 0, "includeThoughts": false}
		case "adaptive", "auto":
			return map[string]interface{}{"thinkingBudget": -1, "includeThoughts": true}
		case "enabled":
			out := map[string]interface{}{"includeThoughts": true}
			if budget, ok := intFromMap(cfg, "budget_tokens", "budgetTokens", "max_tokens", "maxTokens"); ok {
				out["thinkingBudget"] = budget
				out["includeThoughts"] = budget != 0
			} else {
				out["thinkingBudget"] = -1
			}
			return out
		}
	}
	return normalizeGeminiThinkingConfig(raw)
}

func geminiThinkingFromReasoning(raw json.RawMessage) interface{} {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg) == 0 {
		return nil
	}
	out := map[string]interface{}{}
	if budget, ok := intFromMap(cfg, "max_tokens", "maxTokens", "thinkingBudget", "budget_tokens", "budgetTokens"); ok {
		out["thinkingBudget"] = budget
		out["includeThoughts"] = budget != 0
		return out
	}
	if effort := strings.ToLower(strings.TrimSpace(stringFromMap(cfg, "effort", "reasoning_effort"))); effort != "" {
		switch effort {
		case "none", "disabled":
			out["thinkingBudget"] = 0
			out["includeThoughts"] = false
		case "auto":
			out["thinkingBudget"] = -1
			out["includeThoughts"] = true
		default:
			out["thinkingLevel"] = strings.ToUpper(effort)
			out["includeThoughts"] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func anthropicThinkingFromRawThinking(raw json.RawMessage) interface{} {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg) == 0 {
		return raw
	}
	if _, ok := cfg["budget_tokens"]; ok {
		return raw
	}
	if _, ok := cfg["type"]; ok {
		if budget, hasBudget := intFromMap(cfg, "budgetTokens"); hasBudget {
			cfg["budget_tokens"] = budget
			delete(cfg, "budgetTokens")
		}
		return cfg
	}
	return nil
}

func anthropicThinkingFromReasoning(raw json.RawMessage) interface{} {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg) == 0 {
		return nil
	}
	if budget, ok := intFromMap(cfg, "max_tokens", "maxTokens"); ok {
		if budget == 0 {
			return map[string]interface{}{"type": "disabled"}
		}
		return map[string]interface{}{"type": "enabled", "budget_tokens": budget}
	}
	if effort := strings.ToLower(strings.TrimSpace(stringFromMap(cfg, "effort", "reasoning_effort"))); effort != "" {
		if effort == "none" || effort == "disabled" {
			return map[string]interface{}{"type": "disabled"}
		}
		return map[string]interface{}{"type": "enabled"}
	}
	return nil
}

func anthropicThinkingFromGeminiThinking(raw json.RawMessage) interface{} {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg) == 0 {
		return nil
	}
	if budget, ok := intFromMap(cfg, "thinkingBudget", "thinking_budget"); ok {
		if budget == 0 {
			return map[string]interface{}{"type": "disabled"}
		}
		if budget > 0 {
			return map[string]interface{}{"type": "enabled", "budget_tokens": budget}
		}
		return map[string]interface{}{"type": "enabled"}
	}
	if effort := stringFromMap(cfg, "thinkingLevel", "thinking_level"); effort != "" {
		if strings.EqualFold(effort, "none") {
			return map[string]interface{}{"type": "disabled"}
		}
		return map[string]interface{}{"type": "enabled"}
	}
	return nil
}

func stringFromMap(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			switch typed := value.(type) {
			case string:
				return typed
			case json.Number:
				return typed.String()
			}
		}
	}
	return ""
}

func intFromMap(m map[string]interface{}, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed, true
		case int64:
			return int(typed), true
		case float64:
			return int(typed), true
		case json.Number:
			n, err := typed.Int64()
			if err == nil {
				return int(n), true
			}
		}
	}
	return 0, false
}
