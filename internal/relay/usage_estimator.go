package relay

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

const estimatedImageTokens = 1024

func estimateMissingUsage(promptTokens, completionTokens *int, requestBody, responseBody []byte, streamCompletionEstimate int) {
	if promptTokens == nil || completionTokens == nil {
		return
	}
	if *promptTokens > 0 && *completionTokens > 0 {
		return
	}
	if *promptTokens <= 0 {
		*promptTokens = estimatePromptTokensFromRequest(requestBody)
	}
	if *completionTokens <= 0 {
		if streamCompletionEstimate > 0 {
			*completionTokens = streamCompletionEstimate
		} else {
			*completionTokens = estimateCompletionTokensFromResponse(responseBody)
		}
	}
	if *promptTokens <= 0 && *completionTokens > 0 {
		*promptTokens = 1
	}
}

func estimatePromptTokensFromRequest(body []byte) int {
	return estimateTokensFromJSON(body, true)
}

func estimateCompletionTokensFromResponse(body []byte) int {
	return estimateTokensFromJSON(body, false)
}

func estimateTokensFromJSON(body []byte, includeAllStrings bool) int {
	var root interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return estimateTextTokens(string(body))
	}
	tokens := estimateJSONValueTokens(root, includeAllStrings, "")
	if tokens <= 0 {
		return 0
	}
	return tokens
}

func estimateJSONValueTokens(value interface{}, includeAllStrings bool, key string) int {
	switch v := value.(type) {
	case map[string]interface{}:
		total := 0
		for k, child := range v {
			total += estimateJSONValueTokens(child, includeAllStrings, k)
		}
		return total
	case []interface{}:
		total := 0
		for _, child := range v {
			total += estimateJSONValueTokens(child, includeAllStrings, key)
		}
		return total
	case string:
		if v == "" {
			return 0
		}
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "image") || strings.HasPrefix(v, "data:image/") {
			return estimatedImageTokens
		}
		if looksLikeLargeBase64(v) {
			return estimatedImageTokens
		}
		if includeAllStrings || isLikelyGeneratedTextField(lowerKey) {
			return estimateTextTokens(v)
		}
	}
	return 0
}

func isLikelyGeneratedTextField(key string) bool {
	switch key {
	case "content", "text", "delta", "arguments", "output", "reasoning", "reasoning_content", "input":
		return true
	default:
		return strings.Contains(key, "text") || strings.Contains(key, "content")
	}
}

func looksLikeLargeBase64(s string) bool {
	if len(s) < 512 {
		return false
	}
	if i := strings.Index(s, ","); strings.HasPrefix(s, "data:") && i >= 0 {
		s = s[i+1:]
	}
	if len(s)%4 != 0 {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s[:min(len(s), 2048)])
	return err == nil
}

func estimateTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	asciiChars := 0
	nonASCII := 0
	for _, r := range text {
		if r == utf8.RuneError {
			continue
		}
		if r <= unicode.MaxASCII {
			asciiChars++
		} else if !unicode.IsSpace(r) {
			nonASCII++
		}
	}
	tokens := int(math.Ceil(float64(asciiChars)/4.0)) + nonASCII
	if tokens <= 0 {
		return 1
	}
	return tokens
}
