package convert

import (
	"bytes"
	"encoding/json"
	"strings"
)

func cleanJSONUndefinedPlaceholders(body []byte) []byte {
	var root interface{}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return body
	}
	cleaned, changed := cleanUndefinedValue(root, 0)
	if !changed {
		return body
	}
	result, err := json.Marshal(cleaned)
	if err != nil {
		return body
	}
	return result
}

func cleanUndefinedValue(value interface{}, depth int) (interface{}, bool) {
	if depth > 32 {
		return value, false
	}
	changed := false
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if s, ok := child.(string); ok && isUndefinedPlaceholder(s) {
				delete(v, key)
				changed = true
				continue
			}
			cleaned, childChanged := cleanUndefinedValue(child, depth+1)
			if childChanged {
				v[key] = cleaned
				changed = true
			}
		}
		return v, changed
	case []interface{}:
		kept := make([]interface{}, 0, len(v))
		for _, child := range v {
			if s, ok := child.(string); ok && isUndefinedPlaceholder(s) {
				changed = true
				continue
			}
			cleaned, childChanged := cleanUndefinedValue(child, depth+1)
			if childChanged {
				changed = true
			}
			kept = append(kept, cleaned)
		}
		return kept, changed
	default:
		return value, changed
	}
}

func isUndefinedPlaceholder(value string) bool {
	return strings.TrimSpace(value) == "[undefined]"
}
