package provider

import (
	"encoding/json"
	"strings"
)

// ExtractCredentialKey parses the decrypted credentials string and returns
// the actual key/token to use in API requests.
//
// Supported formats:
//   - Plain string: "sk-xxx" → "sk-xxx"
//   - JSON with api_key: {"api_key":"sk-xxx"} → "sk-xxx"
//   - JSON with access_token: {"access_token":"eyJ..."} → "eyJ..."
func ExtractCredentialKey(creds string) string {
	creds = strings.TrimSpace(creds)
	if creds == "" {
		return ""
	}

	// Fast path: not JSON
	if creds[0] != '{' {
		return creds
	}

	// Try JSON parse
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(creds), &m); err != nil {
		return creds // not valid JSON, use as-is
	}

	// Priority: access_token > api_key > token > key
	for _, key := range []string{"access_token", "api_key", "token", "key"} {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}

	// Fallback: return raw string
	return creds
}
