package gemini

import "fmt"

func validateAllowedKeys(m map[string]interface{}, label string, keys ...string) error {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	for key := range m {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s field %q cannot be converted to non-gemini upstream formats", label, key)
		}
	}
	return nil
}
