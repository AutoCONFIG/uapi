package stream

import (
	"encoding/json"
	"strings"
)

const (
	streamReasoningTypeText      = "reasoning.text"
	streamReasoningTypeSummary   = "reasoning.summary"
	streamReasoningTypeEncrypted = "reasoning.encrypted"
)

type chatReasoningDetail struct {
	ID               string `json:"id,omitempty"`
	Index            int    `json:"index,omitempty"`
	Type             string `json:"type,omitempty"`
	Text             string `json:"text,omitempty"`
	Summary          string `json:"summary,omitempty"`
	Signature        string `json:"signature,omitempty"`
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
	Data             string `json:"data,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

func reasoningTextDelta(text string, index int, signature string) map[string]interface{} {
	detail := map[string]interface{}{
		"index": index,
		"type":  streamReasoningTypeText,
		"text":  text,
	}
	if signature != "" {
		detail["signature"] = signature
	}
	return map[string]interface{}{
		"reasoning_content": text,
		"reasoning_details": []interface{}{detail},
	}
}

func reasoningEncryptedDelta(index int, data string) map[string]interface{} {
	if data == "" {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"reasoning_details": []interface{}{
			map[string]interface{}{
				"index":             index,
				"type":              streamReasoningTypeEncrypted,
				"data":              data,
				"encrypted_content": data,
				"signature":         data,
			},
		},
	}
}

func parseChatReasoningDetails(raw json.RawMessage) []chatReasoningDetail {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var details []chatReasoningDetail
	if err := json.Unmarshal(raw, &details); err == nil {
		return details
	}
	return nil
}

func reasoningDetailText(d chatReasoningDetail) string {
	if d.Text != "" {
		return d.Text
	}
	return d.Summary
}

func reasoningDetailSignature(d chatReasoningDetail) string {
	if d.Type == streamReasoningTypeEncrypted {
		return ""
	}
	if d.Signature != "" {
		return d.Signature
	}
	return d.ThoughtSignature
}

func reasoningDetailEncrypted(d chatReasoningDetail) string {
	values := []string{d.EncryptedContent, d.Data, d.ThoughtSignature}
	if d.Type == streamReasoningTypeEncrypted {
		values = append(values, d.Signature)
	}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func rawJSONField(raw json.RawMessage, key string) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj[key]
}

func splitStreamEvents(events []byte) [][]byte {
	text := strings.TrimSpace(string(events))
	if text == "" {
		return nil
	}
	chunks := strings.Split(text, "\n\n")
	out := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		out = append(out, []byte(chunk+"\n\n"))
	}
	return out
}
