package convert

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// AnthropicToInternal converts Anthropic Messages API request to InternalRequest.
func AnthropicToInternal(body []byte) (*InternalRequest, error) {
	var req schema.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Anthropic request: %w", err)
	}

	ir := &InternalRequest{
		Model:        req.Model,
		Stream:       req.Stream,
		SourceFormat: FormatAnthropic,
		Extra:        make(map[string]json.RawMessage),
		MaxTokens:    &req.MaxTokens, // Anthropic requires max_tokens
	}

	// Copy Extra fields
	for k, v := range req.Extra {
		ir.Extra[k] = v
	}

	// Extract system message to Instructions
	if req.System != nil {
		var sysStr string
		if err := json.Unmarshal(req.System, &sysStr); err == nil {
			ir.Instructions = &sysStr
		} else {
			// Try as array of text blocks
			var blocks []map[string]string
			if err := json.Unmarshal(req.System, &blocks); err == nil {
				var texts []string
				for _, b := range blocks {
					if t, ok := b["text"]; ok {
						texts = append(texts, t)
					}
				}
				if len(texts) > 0 {
					instr := joinNonEmpty(texts, "\n\n")
					ir.Instructions = &instr
				}
			}
		}
	}

	// Convert messages
	for _, msg := range req.Messages {
		internalMsg := InternalMessage{
			Role: msg.Role,
		}

		// Convert content blocks
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				internalMsg.Content = append(internalMsg.Content, schema.ContentPart{
					Type: "text",
					Text: block.Text,
				})
			case "image":
				if block.Source != nil {
					dataURI := fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data)
					internalMsg.Content = append(internalMsg.Content, schema.ContentPart{
						Type:     "image_url",
						ImageURL: &dataURI,
					})
				}
			case "tool_use":
				var args string
				json.Unmarshal(block.Input, &args)
				internalMsg.ToolCalls = append(internalMsg.ToolCalls, schema.ToolCall{
					ID:   block.ID,
					Type: "function",
					Name: block.Name,
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      block.Name,
						Arguments: args,
					},
				})
			case "tool_result":
				internalMsg.ToolResult = &schema.ToolResult{
					ToolCallID: block.ToolUseID,
					Content:    block.ContentStr,
					IsError:    block.IsError,
				}
			case "thinking":
				internalMsg.ReasoningContent = append(internalMsg.ReasoningContent, schema.ContentPart{
					Type: "thinking",
					Text: block.Thinking,
				})
			}
		}

		ir.Messages = append(ir.Messages, internalMsg)
	}

	// Generation parameters
	if req.Temperature != nil {
		ir.Temperature = req.Temperature
	}
	if req.TopP != nil {
		ir.TopP = req.TopP
	}
	if req.TopK != nil {
		ir.TopK = req.TopK
	}
	if len(req.StopSequences) > 0 {
		ir.StopWords = req.StopSequences
	}
	if req.Thinking != nil {
		ir.Thinking = req.Thinking
	}
	if req.Tools != nil {
		var tools []schema.Tool
		if json.Unmarshal(req.Tools, &tools) == nil {
			ir.Tools = tools
		}
	}
	if req.ToolChoice != nil {
		ir.ToolChoice = req.ToolChoice
	}

	return ir, nil
}

// InternalToAnthropic converts InternalRequest to Anthropic Messages API request.
func InternalToAnthropic(ir *InternalRequest) ([]byte, error) {
	req := make(map[string]interface{})
	req["model"] = ir.Model
	req["max_tokens"] = 4096 // default if not set
	if ir.MaxTokens != nil {
		req["max_tokens"] = *ir.MaxTokens
	}
	req["stream"] = ir.Stream

	// Add Extra fields
	for k, v := range ir.Extra {
		req[k] = v
	}

	// Add system message from Instructions
	if ir.Instructions != nil {
		req["system"] = *ir.Instructions
	}

	// Convert messages
	messages := make([]map[string]interface{}, 0)
	for _, msg := range ir.Messages {
		msgMap := make(map[string]interface{})
		msgMap["role"] = msg.Role

		// Convert content
		if len(msg.Content) > 0 {
			blocks := make([]map[string]interface{}, 0)
			for _, c := range msg.Content {
				block := map[string]interface{}{"type": c.Type}
				if c.Text != "" {
					block["text"] = c.Text
				}
				if c.ImageURL != nil {
					// Parse data URI to extract media type and data
					dataURI := *c.ImageURL
					mediaType := "image/png" // default
					data := dataURI
					if len(dataURI) > 11 && dataURI[:11] == "data:image/" {
						endIdx := len(dataURI)
						for i := 11; i < len(dataURI); i++ {
							if dataURI[i] == ';' || dataURI[i] == ',' {
								endIdx = i
								break
							}
						}
						mediaType = dataURI[11:endIdx]
						if endIdx < len(dataURI) && dataURI[endIdx] == ';' {
							for i := endIdx + 1; i < len(dataURI); i++ {
								if dataURI[i] == ',' {
									data = dataURI[i+1:]
									break
								}
							}
						}
					}
					block["source"] = map[string]string{
						"type":       "base64",
						"media_type": mediaType,
						"data":       data,
					}
				}
				blocks = append(blocks, block)
			}
			msgMap["content"] = blocks
		}

		// Add tool calls
		if len(msg.ToolCalls) > 0 {
			toolUseBlocks := make([]map[string]interface{}, 0)
			for _, tc := range msg.ToolCalls {
				toolUseBlocks = append(toolUseBlocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": tc.Function.Arguments,
				})
			}
			if msgMap["content"] == nil {
				msgMap["content"] = []map[string]interface{}{}
			}
			msgMap["content"] = append(msgMap["content"].([]map[string]interface{}), toolUseBlocks...)
		}

		// Add tool result
		if msg.ToolResult != nil {
			resultBlock := map[string]interface{}{
				"type":         "tool_result",
				"tool_use_id":  msg.ToolResult.ToolCallID,
				"content":      msg.ToolResult.Content,
			}
			if msg.ToolResult.IsError {
				resultBlock["is_error"] = true
			}
			msgMap["content"] = append(msgMap["content"].([]map[string]interface{}), resultBlock)
		}

		messages = append(messages, msgMap)
	}
	req["messages"] = messages

	// Generation parameters
	if ir.Temperature != nil {
		req["temperature"] = *ir.Temperature
	}
	if ir.TopP != nil {
		req["top_p"] = *ir.TopP
	}
	if ir.TopK != nil {
		req["top_k"] = *ir.TopK
	}
	if len(ir.StopWords) > 0 {
		req["stop_sequences"] = ir.StopWords
	}
	if ir.Thinking != nil {
		req["thinking"] = ir.Thinking
	}
	if ir.Tools != nil {
		tools, _ := json.Marshal(ir.Tools)
		req["tools"] = json.RawMessage(tools)
	}
	if ir.ToolChoice != nil {
		tc, _ := json.Marshal(ir.ToolChoice)
		req["tool_choice"] = json.RawMessage(tc)
	}

	return json.Marshal(req)
}

func init() {
	RegisterToInternal(FormatAnthropic, AnthropicToInternal)
	RegisterFromInternal(FormatAnthropic, InternalToAnthropic)
}