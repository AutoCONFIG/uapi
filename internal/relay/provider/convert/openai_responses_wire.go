package convert

import (
	"encoding/json"
	"strings"
)

func normalizeOpenAIResponsesSameProtocolWire(format Format, body []byte) []byte {
	if format != FormatOpenAIResponses {
		return body
	}
	var root map[string]interface{}
	if err := decodeJSONUseNumber(body, &root); err != nil {
		return body
	}
	changed := false
	if input, ok := root["input"].([]interface{}); ok {
		for _, rawItem := range input {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			if normalizeResponsesMessageWireItem(item) {
				changed = true
			}
			if normalizeResponsesFunctionCallOutputWireItem(item) {
				changed = true
			}
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

func normalizeResponsesMessageWireItem(item map[string]interface{}) bool {
	if typ, _ := item["type"].(string); typ != "message" {
		return false
	}
	content, ok := item["content"].([]interface{})
	if !ok {
		return false
	}
	role, _ := item["role"].(string)
	targetType := "input_text"
	if role == "assistant" {
		targetType = "output_text"
	}
	changed := false
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		if typ, _ := part["type"].(string); typ == "text" {
			part["type"] = targetType
			changed = true
		}
	}
	return changed
}

func normalizeResponsesFunctionCallOutputWireItem(item map[string]interface{}) bool {
	if typ, _ := item["type"].(string); typ != "function_call_output" {
		return false
	}
	blocks, ok := item["output"].([]interface{})
	if !ok || len(blocks) == 0 {
		return false
	}
	texts := make([]string, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]interface{})
		if !ok {
			return false
		}
		typ, _ := block["type"].(string)
		if typ != "text" && typ != "input_text" && typ != "output_text" {
			return false
		}
		text, ok := block["text"].(string)
		if !ok {
			return false
		}
		texts = append(texts, text)
	}
	item["output"] = strings.Join(texts, "\n")
	return true
}
