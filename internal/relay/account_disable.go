package relay

import (
	"encoding/json"
	"strings"

	"github.com/valyala/fasthttp"
)

func terminalAccountDisableReason(statusCode int, body []byte) (string, bool) {
	fields := collectErrorFields(body)
	if boolField(fields, "is_forbidden") || boolField(fields, "_forbidden") || boolField(fields, "quota.is_forbidden") {
		reason := stringField(fields, "_forbidden_reason")
		if reason == "" {
			reason = stringField(fields, "quota._forbidden_reason")
		}
		if reason == "" {
			reason = "account_forbidden"
		}
		return reason, true
	}

	status := strings.ToUpper(firstNonEmptyDisableString(
		stringField(fields, "error.status"),
		stringField(fields, "status"),
	))
	code := strings.ToLower(firstNonEmptyDisableString(
		stringField(fields, "error.code"),
		stringField(fields, "code"),
		stringField(fields, "error.type"),
		stringField(fields, "type"),
	))
	message := strings.ToLower(firstNonEmptyDisableString(
		stringField(fields, "error.message"),
		stringField(fields, "message"),
		stringField(fields, "detail"),
	))

	switch {
	case status == "PERMISSION_DENIED":
		return "permission_denied", true
	case status == "UNAUTHENTICATED":
		return "unauthenticated", true
	case strings.Contains(code, "invalid_api_key"), strings.Contains(code, "invalid_key"):
		return "invalid_api_key", true
	case strings.Contains(code, "permission"), strings.Contains(code, "forbidden"), strings.Contains(code, "unauthorized"):
		return normalizedDisableReason(code), true
	}

	if statusCode == fasthttp.StatusUnauthorized && hasTerminalAccountKeyword(message) {
		return normalizedDisableReason(message), true
	}
	if statusCode == fasthttp.StatusForbidden && hasTerminalAccountKeyword(message) {
		return normalizedDisableReason(message), true
	}
	return "", false
}

func collectErrorFields(body []byte) map[string]interface{} {
	fields := make(map[string]interface{})
	var root interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return fields
	}
	flattenJSONFields("", root, fields, 0)
	return fields
}

func flattenJSONFields(prefix string, value interface{}, out map[string]interface{}, depth int) {
	if depth > 6 {
		return
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			out[next] = child
			flattenJSONFields(next, child, out, depth+1)
		}
	case []interface{}:
		for _, child := range typed {
			flattenJSONFields(prefix, child, out, depth+1)
		}
	}
}

func boolField(fields map[string]interface{}, key string) bool {
	value, ok := fields[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func stringField(fields map[string]interface{}, key string) string {
	value, ok := fields[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func firstNonEmptyDisableString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func hasTerminalAccountKeyword(message string) bool {
	for _, keyword := range []string{
		"account is not authorized",
		"account has no access",
		"account forbidden",
		"organization has been disabled",
		"permission denied",
		"operation not allowed",
		"security token included in the request is invalid",
		"api key is invalid",
		"invalid api key",
		"invalid token",
		"apikey不存在",
		"api key不存在",
		"api_key不存在",
		"api key 不存在",
		"apikey 不存在",
		"api key配置错误",
		"apikey配置错误",
		"api_key配置错误",
		"凭据无效",
		"认证过期",
		"认证已过期",
		"认证失败",
	} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func normalizedDisableReason(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "upstream_account_error"
	}
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, value)
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	value = strings.Trim(value, "_")
	if len(value) > 80 {
		value = value[:80]
	}
	if value == "" {
		return "upstream_account_error"
	}
	return value
}
