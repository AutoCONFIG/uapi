package convert

import (
	"encoding/json"
	"strings"
)

func jsonArgumentValue(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]interface{}{}
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err == nil && value != nil {
		return value
	}
	return map[string]interface{}{"value": raw}
}

func rawJSONArgumentString(raw json.RawMessage) string {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return "{}"
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if strings.TrimSpace(asString) == "" {
			return "{}"
		}
		return asString
	}
	return string(raw)
}
