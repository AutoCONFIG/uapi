package anthropic

import "fmt"

func validateAnthropicContentBlockKeys(block map[string]interface{}) error {
	blockType, _ := block["type"].(string)
	allowedByType := map[string]map[string]struct{}{
		"text": {
			"type": {},
			"text": {},
		},
		"tool_use": {
			"type":  {},
			"id":    {},
			"name":  {},
			"input": {},
		},
		"tool_result": {
			"type":        {},
			"tool_use_id": {},
			"content":     {},
			"is_error":    {},
		},
		"image": {
			"type":   {},
			"source": {},
		},
	}
	allowed, ok := allowedByType[blockType]
	if !ok {
		return nil
	}
	for key := range block {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("anthropic content block field %q cannot be converted to non-anthropic upstream formats", key)
		}
	}
	return nil
}

func validateAllowedKeys(m map[string]interface{}, label string, keys ...string) error {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	for key := range m {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s field %q cannot be converted to non-anthropic upstream formats", label, key)
		}
	}
	return nil
}
