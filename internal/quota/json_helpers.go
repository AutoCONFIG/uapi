package quota

func firstString(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func firstFloat(m map[string]interface{}, keys ...string) *float64 {
	for _, key := range keys {
		switch value := m[key].(type) {
		case float64:
			return &value
		case int:
			floatValue := float64(value)
			return &floatValue
		}
	}
	return nil
}
