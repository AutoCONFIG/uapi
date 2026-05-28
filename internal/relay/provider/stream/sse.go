package stream

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

func sseData(line []byte) (string, bool) {
	text := strings.TrimSpace(string(line))
	if text == "" {
		return "", false
	}
	var data []string
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimRight(raw, "\r")
		if strings.HasPrefix(raw, "data:") {
			part := strings.TrimPrefix(raw, "data:")
			if strings.HasPrefix(part, " ") {
				part = strings.TrimPrefix(part, " ")
			}
			data = append(data, part)
		}
	}
	if len(data) > 0 {
		return strings.Join(data, "\n"), true
	}
	return text, true
}

func sseJSON(v interface{}) []byte {
	body, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return []byte("data: " + string(body) + "\n\n")
}

func randomID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return prefix + hex.EncodeToString(b[:])
	}
	return prefix + hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
}

func chatChunk(id, model string, delta map[string]interface{}, finishReason interface{}, usage map[string]interface{}) []byte {
	if id == "" {
		id = randomID("chatcmpl-")
	}
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{
			map[string]interface{}{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	return sseJSON(chunk)
}
