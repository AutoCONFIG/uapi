package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/google/uuid"
)

type chatGPTReverseStreamConverter struct {
	id       string
	model    string
	created  int64
	started  bool
	finished bool
	text     string
}

func newChatGPTReverseInputConverter(model string) func([]byte) []byte {
	c := &chatGPTReverseStreamConverter{
		id:      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		model:   model,
		created: time.Now().Unix(),
	}
	return c.convert
}

func (c *chatGPTReverseStreamConverter) convert(event []byte) []byte {
	if c.finished {
		return nil
	}
	payloads := sseDataPayloads(event)
	if len(payloads) == 0 {
		return nil
	}
	var out []byte
	for _, payload := range payloads {
		if payload == "[DONE]" {
			out = append(out, c.finish()...)
			continue
		}
		delta, replaced, done := c.extractDelta(payload)
		if done {
			out = append(out, c.finish()...)
			continue
		}
		if replaced {
			if strings.HasPrefix(delta, c.text) {
				delta = delta[len(c.text):]
			}
			c.text += delta
		} else if delta != "" {
			c.text += delta
		}
		if delta != "" || !c.started {
			out = append(out, c.chunk(delta, "")...)
		}
	}
	return out
}

func (c *chatGPTReverseStreamConverter) extractDelta(payload string) (string, bool, bool) {
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		return payload, false, false
	}
	if typ, _ := root["type"].(string); typ == "conversation.done" || typ == "done" {
		return "", false, true
	}
	if text, ok := chatGPTPatchText(root); ok {
		return sanitizeChatGPTOutput(text), false, false
	}
	if messageText, ok := chatGPTMessageText(root); ok {
		return sanitizeChatGPTOutput(messageText), true, false
	}
	return "", false, false
}

func (c *chatGPTReverseStreamConverter) chunk(delta, finishReason string) []byte {
	deltaObj := map[string]interface{}{}
	if !c.started {
		deltaObj["role"] = "assistant"
		c.started = true
	}
	if delta != "" {
		deltaObj["content"] = delta
	}
	choice := map[string]interface{}{
		"index": 0,
		"delta": deltaObj,
	}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}
	body := map[string]interface{}{
		"id":      c.id,
		"object":  "chat.completion.chunk",
		"created": c.created,
		"model":   c.model,
		"choices": []interface{}{choice},
	}
	b, _ := json.Marshal(body)
	return []byte("data: " + string(b) + "\n\n")
}

func (c *chatGPTReverseStreamConverter) finish() []byte {
	if c.finished {
		return nil
	}
	c.finished = true
	return append(c.chunk("", "stop"), []byte("data: [DONE]\n\n")...)
}

func chatGPTPatchText(root map[string]interface{}) (string, bool) {
	op, _ := root["o"].(string)
	path, _ := root["p"].(string)
	if op == "append" && path == "/message/content/parts/0" {
		if text, ok := root["v"].(string); ok {
			return text, true
		}
	}
	if op == "patch" {
		if patches, ok := root["v"].([]interface{}); ok {
			var b strings.Builder
			for _, raw := range patches {
				patch, _ := raw.(map[string]interface{})
				if patch == nil {
					continue
				}
				if text, ok := chatGPTPatchText(patch); ok {
					b.WriteString(text)
				}
			}
			if b.Len() > 0 {
				return b.String(), true
			}
		}
	}
	if op == "" && path == "" {
		if text, ok := root["v"].(string); ok {
			return text, true
		}
	}
	return "", false
}

func chatGPTMessageText(root map[string]interface{}) (string, bool) {
	value, ok := root["v"].(map[string]interface{})
	if !ok {
		return "", false
	}
	message, ok := value["message"].(map[string]interface{})
	if !ok {
		return "", false
	}
	author, _ := message["author"].(map[string]interface{})
	if role, _ := author["role"].(string); role != "assistant" {
		return "", false
	}
	content, _ := message["content"].(map[string]interface{})
	parts, _ := content["parts"].([]interface{})
	var b strings.Builder
	for _, part := range parts {
		switch v := part.(type) {
		case string:
			b.WriteString(v)
		case map[string]interface{}:
			if text, _ := v["text"].(string); text != "" {
				b.WriteString(text)
			}
		default:
			b.WriteString(fmt.Sprint(v))
		}
	}
	return b.String(), b.Len() > 0
}

func sanitizeChatGPTOutput(text string) string {
	for {
		start := strings.IndexRune(text, '\ue200')
		if start < 0 {
			return text
		}
		endRel := strings.IndexRune(text[start:], '\ue201')
		if endRel < 0 {
			return text[:start]
		}
		text = text[:start] + text[start+endRel+len(string('\ue201')):]
	}
}

func chatGPTReverseOutputConverter(upstreamFormat, clientFormat provider.Format, routedModel string) (func([]byte) []byte, func([]byte) []byte) {
	if upstreamFormat != provider.FormatChatGPTReverse {
		return nil, newStreamConverterFunc(upstreamFormat, clientFormat)
	}
	return newChatGPTReverseInputConverter(routedModel), newStreamConverterFunc(provider.FormatOpenAIChatCompletions, clientFormat)
}
