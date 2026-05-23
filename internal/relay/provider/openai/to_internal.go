package openai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
)

// warnSkippedFields logs a warning when fields cannot be converted between protocols.
func warnSkippedFields(source, target string, fields []string) {
	if len(fields) == 0 {
		return
	}
	logger.Component("provider.openai").Warn("cross-protocol conversion: fields skipped as no equivalent in target",
		logger.F("source_format", source),
		logger.F("target_format", target),
		logger.F("skipped_fields", strings.Join(fields, ",")),
		logger.F("reason", "no equivalent field in target protocol"))
}

// openaiChatToInternal converts an OpenAI Chat Completions API request body
// into the intermediate InternalRequest format.
func openaiChatToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse openai chat request: %w", err)
	}

	ir := &provider.InternalRequest{
		Metadata:    make(map[string]interface{}),
		ExtraParams: make(map[string]interface{}),
	}

	// Extract known fields first
	if v, ok := req["frequency_penalty"].(float64); ok {
		ir.FrequencyPenalty = &v
	}
	if v, ok := req["presence_penalty"].(float64); ok {
		ir.PresencePenalty = &v
	}
	if v, ok := req["n"].(float64); ok && v > 0 {
		n := int(v)
		ir.N = &n
	}
	if v, ok := req["seed"].(float64); ok {
		seed := int64(v)
		ir.Seed = &seed
	}
	if _, ok := req["logprobs"]; ok {
		ir.LogProbs = true
	}
	if v, ok := req["top_logprobs"].(float64); ok && v >= 0 {
		tlp := int(v)
		ir.TopLogProbs = &tlp
	}
	if v, ok := req["response_format"]; ok {
		ir.ResponseFormat = v
	}
	if v, ok := req["logit_bias"]; ok {
		ir.LogitBias = v
	}
	if v, ok := req["parallel_tool_calls"].(bool); ok {
		ir.ParallelToolCalls = &v
	}
	if v, ok := req["service_tier"].(string); ok {
		ir.ServiceTier = v
	}
	if v, ok := req["store"].(bool); ok {
		ir.Store = &v
	}

	// Store known extra fields in metadata for passthrough
	ir.Metadata["openai_chat_extra"] = copyFields(req, []string{
		"response_format", "seed", "logprobs", "top_logprobs",
		"presence_penalty", "frequency_penalty", "n", "user",
		"stream_options", "parallel_tool_calls", "service_tier",
		"logit_bias", "metadata", "max_completion_tokens",
		"modalities", "audio", "prediction", "store",
	})

	// Extract any unrecognized top-level fields into ExtraParams
	knownChatFields := map[string]bool{
		"model": true, "messages": true, "tools": true, "tool_choice": true,
		"stream": true, "max_completion_tokens": true, "max_tokens": true,
		"temperature": true, "top_p": true, "stop": true, "user": true,
		"frequency_penalty": true, "presence_penalty": true, "n": true,
		"seed": true, "logprobs": true, "top_logprobs": true,
		"response_format": true, "logit_bias": true, "parallel_tool_calls": true,
		"service_tier": true, "store": true, "metadata": true,
		"stream_options": true, "modalities": true, "audio": true,
		"prediction": true,
	}
	for k, v := range req {
		if !knownChatFields[k] {
			ir.ExtraParams[k] = v
		}
	}

	ir.Metadata["source_format"] = string(provider.FormatOpenAIChatCompletions)

	// Model
	ir.Model, _ = req["model"].(string)

	// Stream
	if s, ok := req["stream"].(bool); ok {
		ir.Stream = s
	}

	// MaxTokens
	if v, ok := req["max_completion_tokens"].(float64); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	} else if v, ok := req["max_tokens"].(float64); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	}

	// Temperature
	if v, ok := req["temperature"].(float64); ok {
		ir.Temperature = &v
	}

	// TopP
	if v, ok := req["top_p"].(float64); ok {
		ir.TopP = &v
	}

	// StopWords
	switch s := req["stop"].(type) {
	case string:
		if s != "" {
			ir.StopWords = []string{s}
		}
	case []interface{}:
		for _, item := range s {
			if str, ok := item.(string); ok {
				ir.StopWords = append(ir.StopWords, str)
			}
		}
	}

	// Messages
	messages, _ := req["messages"].([]interface{})
	ir.Messages = make([]provider.InternalMessage, 0, len(messages))
	for _, msgRaw := range messages {
		msg, ok := msgRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if err := validateOpenAIMessageConvertible(msg); err != nil {
			return nil, err
		}
		im := parseOpenAIMessage(msg)
		ir.Messages = append(ir.Messages, im)
	}

	// Tools
	if tools, ok := req["tools"].([]interface{}); ok {
		if err := validateOpenAIChatToolsConvertible(tools); err != nil {
			return nil, err
		}
		ir.Tools = make([]provider.InternalTool, 0, len(tools))
		for _, toolRaw := range tools {
			tool, ok := toolRaw.(map[string]interface{})
			if !ok {
				continue
			}
			it := provider.InternalTool{Type: "function"}
			if fn, ok := tool["function"].(map[string]interface{}); ok {
				it.Name, _ = fn["name"].(string)
				it.Description, _ = fn["description"].(string)
				it.Parameters = fn["parameters"]
			}
			ir.Tools = append(ir.Tools, it)
		}
	}

	// ToolChoice
	if tc, ok := req["tool_choice"]; ok {
		if err := validateOpenAIChatToolChoiceConvertible(tc); err != nil {
			return nil, err
		}
		ir.ToolChoice = parseOpenAIToolChoice(tc)
	}

	return ir, nil
}

func responsesToInternal(body []byte) (*provider.InternalRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse openai responses request: %w", err)
	}
	if err := validateResponsesInputConvertible(req["input"], true); err != nil {
		return nil, err
	}
	ir := &provider.InternalRequest{
		Metadata:    make(map[string]interface{}),
		ExtraParams: make(map[string]interface{}),
	}

	// Extract known fields into explicit InternalRequest fields
	if v, ok := req["store"].(bool); ok {
		ir.Store = &v
	}
	if v, ok := req["reasoning"]; ok {
		ir.Reasoning = v
	}
	if v, ok := req["parallel_tool_calls"].(bool); ok {
		ir.ParallelToolCalls = &v
	}
	if v, ok := req["service_tier"].(string); ok {
		ir.ServiceTier = v
	}
	if v, ok := req["top_logprobs"].(float64); ok && v >= 0 {
		tlp := int(v)
		ir.TopLogProbs = &tlp
	}

	ir.Metadata["openai_responses_extra"] = copyFields(req, []string{
		"background", "conversation", "include", "max_tool_calls",
		"metadata", "parallel_tool_calls", "previous_response_id",
		"prompt", "prompt_cache_key", "prompt_cache_retention",
		"reasoning", "safety_identifier", "service_tier", "store",
		"text", "top_logprobs", "truncation", "user", "stream_options",
	})

	// Extract any unrecognized top-level fields into ExtraParams
	knownResponsesFields := map[string]bool{
		"model": true, "input": true, "instructions": true,
		"tools": true, "tool_choice": true, "stream": true,
		"max_output_tokens": true, "max_tokens": true,
		"temperature": true, "top_p": true,
		"background": true, "conversation": true, "include": true,
		"max_tool_calls": true, "metadata": true,
		"parallel_tool_calls": true, "previous_response_id": true,
		"prompt": true, "prompt_cache_key": true,
		"prompt_cache_retention": true, "reasoning": true,
		"safety_identifier": true, "service_tier": true,
		"store": true, "text": true, "top_logprobs": true,
		"truncation": true, "user": true, "stream_options": true,
	}
	for k, v := range req {
		if !knownResponsesFields[k] {
			ir.ExtraParams[k] = v
		}
	}

	ir.Metadata["source_format"] = string(provider.FormatOpenAIResponses)
	ir.Model, _ = req["model"].(string)
	if s, ok := req["stream"].(bool); ok {
		ir.Stream = s
	}
	if v, ok := req["max_output_tokens"].(float64); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	} else if v, ok := req["max_tokens"].(float64); ok && v > 0 {
		tokens := int(v)
		ir.MaxTokens = &tokens
	}
	if v, ok := req["temperature"].(float64); ok {
		ir.Temperature = &v
	}
	if v, ok := req["top_p"].(float64); ok {
		ir.TopP = &v
	}
	if tools, ok := req["tools"].([]interface{}); ok {
		if err := validateResponsesToolsConvertible(tools); err != nil {
			return nil, err
		}
		ir.Metadata["openai_responses_tools_raw"] = tools
		ir.Tools = parseResponsesTools(tools)
	}
	if tc, ok := req["tool_choice"]; ok {
		if err := validateResponsesToolChoiceConvertible(tc); err != nil {
			return nil, err
		}
		ir.Metadata["openai_responses_tool_choice_raw"] = tc
		ir.ToolChoice = parseResponsesToolChoice(tc)
	}
	if instructions, ok := req["instructions"].(string); ok && instructions != "" {
		ir.Messages = append(ir.Messages, provider.InternalMessage{
			Role:    "system",
			Content: []provider.InternalContentPart{{Type: "text", Text: instructions}},
		})
	}
	ir.Messages = append(ir.Messages, parseResponsesInput(req["input"])...)
	return ir, nil
}

func validateResponsesInputConvertible(v interface{}, topLevel bool) error {
	switch x := v.(type) {
	case []interface{}:
		for _, item := range x {
			if err := validateResponsesInputConvertible(item, topLevel); err != nil {
				return err
			}
		}
	case map[string]interface{}:
		typ, _ := x["type"].(string)
		if topLevel && isResponsesTopLevelItemType(typ) && !isSupportedResponsesTopLevelItemType(typ) {
			return fmt.Errorf("openai responses input item type %q cannot be converted to non-responses upstream formats", typ)
		}
		if typ == "input_file" || typ == "file" {
			return fmt.Errorf("openai responses input_file cannot be converted to non-responses upstream formats")
		}
		if typ == "function_call" {
			name, _ := x["name"].(string)
			args, _ := x["arguments"].(string)
			if name == "" || args == "" {
				return fmt.Errorf("openai responses function_call requires name and arguments")
			}
			callID, _ := x["call_id"].(string)
			id, _ := x["id"].(string)
			if callID == "" && id == "" {
				return fmt.Errorf("openai responses function_call requires call_id or id")
			}
		}
		if typ == "function_call_output" {
			callID, _ := x["call_id"].(string)
			_, hasOutput := x["output"].(string)
			if callID == "" || !hasOutput {
				return fmt.Errorf("openai responses function_call_output requires call_id and output")
			}
		}
		if typ == "input_image" {
			if fileID, _ := x["file_id"].(string); fileID != "" {
				return fmt.Errorf("openai responses input_image.file_id cannot be converted to non-responses upstream formats")
			}
			if !responsesImageHasURL(x) {
				return fmt.Errorf("openai responses input_image without image_url cannot be converted to non-responses upstream formats")
			}
		}
		if isResponsesContentPartType(typ) && !isSupportedResponsesContentPart(x) {
			return fmt.Errorf("openai responses content part type %q cannot be converted to non-responses upstream formats", typ)
		}
		if err := validateResponsesInputConvertible(x["content"], false); err != nil {
			return err
		}
	}
	return nil
}

func isResponsesContentPartType(typ string) bool {
	return strings.HasPrefix(typ, "input_") || strings.HasPrefix(typ, "output_") || typ == "text" || typ == "image_url" || typ == "file"
}

func isResponsesTopLevelItemType(typ string) bool {
	if typ == "" {
		return false
	}
	return strings.HasPrefix(typ, "input_") ||
		strings.HasPrefix(typ, "output_") ||
		strings.Contains(typ, "call") ||
		typ == "message" ||
		typ == "reasoning" ||
		typ == "file" ||
		typ == "item_reference"
}

func isSupportedResponsesTopLevelItemType(typ string) bool {
	switch typ {
	case "message", "function_call", "function_call_output":
		return true
	default:
		return false
	}
}

func isSupportedResponsesContentPart(part map[string]interface{}) bool {
	typ, _ := part["type"].(string)
	switch typ {
	case "input_text", "output_text", "text":
		if annotations, ok := part["annotations"].([]interface{}); ok && len(annotations) > 0 {
			return false
		}
		text, _ := part["text"].(string)
		return text != ""
	case "input_image", "image_url":
		return responsesImageHasURL(part)
	default:
		return false
	}
}

func responsesImageHasURL(part map[string]interface{}) bool {
	if imageURL, ok := part["image_url"].(string); ok && imageURL != "" {
		return true
	}
	if imageURL, ok := part["image_url"].(map[string]interface{}); ok {
		url, _ := imageURL["url"].(string)
		return url != ""
	}
	return false
}

func parseResponsesInput(input interface{}) []provider.InternalMessage {
	switch v := input.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []provider.InternalMessage{{Role: "user", Content: []provider.InternalContentPart{{Type: "text", Text: v}}}}
	case []interface{}:
		out := make([]provider.InternalMessage, 0, len(v))
		for _, item := range v {
			switch msg := item.(type) {
			case string:
				if msg != "" {
					out = append(out, provider.InternalMessage{Role: "user", Content: []provider.InternalContentPart{{Type: "text", Text: msg}}})
				}
			case map[string]interface{}:
				out = append(out, parseResponsesMessage(msg))
			}
		}
		return out
	default:
		return nil
	}
}

func parseResponsesMessage(msg map[string]interface{}) provider.InternalMessage {
	msgType, _ := msg["type"].(string)
	if msgType == "function_call" {
		callID, _ := msg["call_id"].(string)
		if callID == "" {
			callID, _ = msg["id"].(string)
		}
		name, _ := msg["name"].(string)
		args, _ := msg["arguments"].(string)
		return provider.InternalMessage{
			Role: "assistant",
			ToolCalls: []provider.InternalToolCall{{
				ID:        callID,
				Name:      name,
				Arguments: args,
			}},
		}
	}
	if msgType == "function_call_output" {
		callID, _ := msg["call_id"].(string)
		output, _ := msg["output"].(string)
		return provider.InternalMessage{
			Role: "tool",
			ToolResult: &provider.InternalToolResult{
				ToolCallID: callID,
				Content:    output,
			},
		}
	}
	role, _ := msg["role"].(string)
	if role == "" {
		role = "user"
	}
	return provider.InternalMessage{
		Role:    role,
		Content: parseResponsesContent(msg["content"]),
	}
}

func parseResponsesContent(content interface{}) []provider.InternalContentPart {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []provider.InternalContentPart{{Type: "text", Text: c}}
	case []interface{}:
		var parts []provider.InternalContentPart
		for _, item := range c {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			switch typ {
			case "input_text", "output_text", "text":
				if text, _ := m["text"].(string); text != "" {
					parts = append(parts, provider.InternalContentPart{Type: "text", Text: text})
				}
			case "input_image", "image_url":
				if imageURL, ok := m["image_url"].(string); ok && imageURL != "" {
					detail, _ := m["detail"].(string)
					parts = append(parts, provider.InternalContentPart{Type: "image_url", ImageURL: &imageURL, ImageDetail: detail})
				} else if imageURL, ok := m["image_url"].(map[string]interface{}); ok {
					url, _ := imageURL["url"].(string)
					if url != "" {
						detail, _ := imageURL["detail"].(string)
						parts = append(parts, provider.InternalContentPart{Type: "image_url", ImageURL: &url, ImageDetail: detail})
					}
				}
			}
		}
		return parts
	default:
		return nil
	}
}

func validateOpenAIMessageContent(content interface{}) error {
	items, ok := content.([]interface{})
	if !ok {
		return nil
	}
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		switch typ {
		case "text":
			text, _ := m["text"].(string)
			if text == "" {
				return fmt.Errorf("openai chat text content part requires text")
			}
		case "image_url":
			imageURL, ok := m["image_url"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("openai chat image_url content part requires image_url object")
			}
			url, _ := imageURL["url"].(string)
			if url == "" {
				return fmt.Errorf("openai chat image_url content part requires image_url.url")
			}
		default:
			return fmt.Errorf("openai chat content part type %q cannot be converted to non-chat upstream formats", typ)
		}
	}
	return nil
}

func validateOpenAIMessageConvertible(msg map[string]interface{}) error {
	if err := validateOpenAIMessageContent(msg["content"]); err != nil {
		return err
	}
	if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
		for _, raw := range toolCalls {
			tc, ok := raw.(map[string]interface{})
			if !ok {
				return fmt.Errorf("openai chat tool_call must be an object")
			}
			id, _ := tc["id"].(string)
			fn, ok := tc["function"].(map[string]interface{})
			if id == "" || !ok {
				return fmt.Errorf("openai chat tool_call requires id and function")
			}
			name, _ := fn["name"].(string)
			args, hasArgs := fn["arguments"].(string)
			if name == "" || !hasArgs || args == "" {
				return fmt.Errorf("openai chat tool_call requires function.name and function.arguments")
			}
		}
	}
	role, _ := msg["role"].(string)
	if role == "tool" {
		toolCallID, _ := msg["tool_call_id"].(string)
		content, ok := msg["content"].(string)
		if toolCallID == "" || !ok {
			return fmt.Errorf("openai chat tool message requires tool_call_id and string content")
		}
		_ = content
	}
	return nil
}

// parseOpenAIMessage converts a single OpenAI message object to InternalMessage.
func parseOpenAIMessage(msg map[string]interface{}) provider.InternalMessage {
	im := provider.InternalMessage{}
	im.Role, _ = msg["role"].(string)

	// Parse content
	im.Content = parseOpenAIContent(msg["content"])

	// Parse tool_calls
	if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
		im.ToolCalls = make([]provider.InternalToolCall, 0, len(toolCalls))
		for _, tcRaw := range toolCalls {
			tc, ok := tcRaw.(map[string]interface{})
			if !ok {
				continue
			}
			itc := provider.InternalToolCall{
				ID: intfStr(tc["id"]),
			}
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				itc.Name, _ = fn["name"].(string)
				itc.Arguments, _ = fn["arguments"].(string)
			}
			im.ToolCalls = append(im.ToolCalls, itc)
		}
	}

	// Parse tool result (for role "tool")
	if im.Role == "tool" {
		im.ToolResult = &provider.InternalToolResult{
			ToolCallID: intfStr(msg["tool_call_id"]),
		}
		if c, ok := msg["content"].(string); ok {
			im.ToolResult.Content = c
		}
	}

	return im
}

// parseOpenAIContent converts OpenAI content (string or array) to InternalContentPart slice.
func parseOpenAIContent(content interface{}) []provider.InternalContentPart {
	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []provider.InternalContentPart{{Type: "text", Text: c}}
	case []interface{}:
		var parts []provider.InternalContentPart
		for _, item := range c {
			if m, ok := item.(map[string]interface{}); ok {
				part := provider.InternalContentPart{}
				part.Type, _ = m["type"].(string)
				part.Text, _ = m["text"].(string)
				if imgURL, ok := m["image_url"].(map[string]interface{}); ok {
					url, _ := imgURL["url"].(string)
					part.ImageURL = &url
					part.ImageDetail, _ = imgURL["detail"].(string)
				}
				parts = append(parts, part)
			}
		}
		return parts
	default:
		return nil
	}
}

// parseOpenAIToolChoice converts OpenAI tool_choice to InternalToolChoice.
func parseOpenAIToolChoice(tc interface{}) *provider.InternalToolChoice {
	switch v := tc.(type) {
	case string:
		return &provider.InternalToolChoice{Type: v}
	case map[string]interface{}:
		itc := &provider.InternalToolChoice{}
		itc.Type, _ = v["type"].(string) // e.g. "function"
		if fn, ok := v["function"].(map[string]interface{}); ok {
			itc.Function, _ = fn["name"].(string)
		}
		return itc
	default:
		return &provider.InternalToolChoice{Type: "auto"}
	}
}

// internalToOpenAIChat converts InternalRequest to OpenAI Chat Completions API JSON.
func internalToOpenAIChat(req *provider.InternalRequest) ([]byte, error) {
	if source, _ := req.Metadata["source_format"].(string); source == string(provider.FormatOpenAIResponses) {
		if extra, ok := req.Metadata["openai_responses_extra"].(map[string]interface{}); ok && len(extra) > 0 {
			warnSkippedFields(string(provider.FormatOpenAIResponses), string(provider.FormatOpenAIChatCompletions), unsupportedResponseExtrasForChat(extra))
		}
	}
	oai := make(map[string]interface{})
	if extra, ok := req.Metadata["openai_chat_extra"].(map[string]interface{}); ok {
		for k, v := range extra {
			oai[k] = v
		}
	}
	if extra, ok := req.Metadata["openai_responses_extra"].(map[string]interface{}); ok {
		copyAllowedFields(oai, extra, openAICommonRequestExtraKeys())
	}
	oai["model"] = req.Model
	oai["stream"] = req.Stream

	if req.MaxTokens != nil {
		if _, ok := oai["max_completion_tokens"]; ok {
			oai["max_completion_tokens"] = *req.MaxTokens
		} else if source, _ := req.Metadata["source_format"].(string); source == string(provider.FormatOpenAIResponses) {
			oai["max_completion_tokens"] = *req.MaxTokens
		} else {
			oai["max_tokens"] = *req.MaxTokens
		}
	}
	if req.Temperature != nil {
		oai["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		oai["top_p"] = *req.TopP
	}
	if len(req.StopWords) > 0 {
		oai["stop"] = req.StopWords
	}

	// Emit explicit InternalRequest fields (override metadata equivalents)
	if req.FrequencyPenalty != nil {
		oai["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		oai["presence_penalty"] = *req.PresencePenalty
	}
	if req.N != nil {
		oai["n"] = *req.N
	}
	if req.Seed != nil {
		oai["seed"] = *req.Seed
	}
	if req.LogProbs {
		oai["logprobs"] = true
	}
	if req.TopLogProbs != nil {
		oai["top_logprobs"] = *req.TopLogProbs
	}
	if req.ResponseFormat != nil {
		oai["response_format"] = req.ResponseFormat
	}
	if req.LogitBias != nil {
		oai["logit_bias"] = req.LogitBias
	}
	if req.ParallelToolCalls != nil {
		oai["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.ServiceTier != "" {
		oai["service_tier"] = req.ServiceTier
	}
	if source, _ := req.Metadata["source_format"].(string); source != string(provider.FormatOpenAIResponses) {
		if req.Store != nil {
			oai["store"] = *req.Store
		}
		if req.Reasoning != nil {
			oai["reasoning"] = req.Reasoning
		}
	}

	// Merge ExtraParams for same-protocol passthrough
	// Explicit struct fields take precedence over ExtraParams
	for k, v := range req.ExtraParams {
		if _, exists := oai[k]; !exists {
			oai[k] = v
		}
	}

	// Messages
	messages := make([]interface{}, 0, len(req.Messages))
	for _, im := range req.Messages {
		messages = append(messages, buildOpenAIMessage(im))
	}
	oai["messages"] = messages

	// Tools
	if len(req.Tools) > 0 {
		tools := make([]interface{}, 0, len(req.Tools))
		for _, it := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"type": it.Type,
				"function": map[string]interface{}{
					"name":        it.Name,
					"description": it.Description,
					"parameters":  it.Parameters,
				},
			})
		}
		oai["tools"] = tools
	}

	// ToolChoice
	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto", "none", "required":
			oai["tool_choice"] = req.ToolChoice.Type
		default:
			oai["tool_choice"] = map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name": req.ToolChoice.Function,
				},
			}
		}
	}

	return json.Marshal(oai)
}

// buildOpenAIMessage converts InternalMessage to OpenAI message map.
func buildOpenAIMessage(im provider.InternalMessage) map[string]interface{} {
	msg := map[string]interface{}{
		"role": im.Role,
	}

	// Content
	if len(im.Content) > 0 {
		if len(im.Content) == 1 && im.Content[0].Type == "text" && im.Content[0].ImageURL == nil {
			msg["content"] = im.Content[0].Text
		} else {
			content := make([]interface{}, 0, len(im.Content))
			for _, part := range im.Content {
				p := map[string]interface{}{"type": part.Type}
				switch part.Type {
				case "text":
					p["text"] = part.Text
				case "image_url":
					if part.ImageURL != nil {
						img := map[string]interface{}{"url": *part.ImageURL}
						if part.ImageDetail != "" {
							img["detail"] = part.ImageDetail
						}
						p["image_url"] = img
					}
				}
				content = append(content, p)
			}
			msg["content"] = content
		}
	} else if im.Role != "tool" {
		msg["content"] = ""
	}

	// ToolCalls
	if len(im.ToolCalls) > 0 {
		tcs := make([]interface{}, 0, len(im.ToolCalls))
		for _, itc := range im.ToolCalls {
			tcs = append(tcs, map[string]interface{}{
				"id":   itc.ID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      itc.Name,
					"arguments": itc.Arguments,
				},
			})
		}
		msg["tool_calls"] = tcs
	}

	// Tool result fields
	if im.ToolResult != nil {
		msg["tool_call_id"] = im.ToolResult.ToolCallID
		msg["content"] = im.ToolResult.Content
	}

	return msg
}

// internalToResponses converts InternalRequest to OpenAI Responses API format.
func internalToResponses(req *provider.InternalRequest) ([]byte, error) {
	if extra, ok := req.Metadata["openai_chat_extra"].(map[string]interface{}); ok {
		warnSkippedFields(string(provider.FormatOpenAIChatCompletions), string(provider.FormatOpenAIResponses), unsupportedChatExtrasForResponses(extra))
	}
	if len(req.StopWords) > 0 {
		logger.Component("provider.openai").Warn("stop sequences cannot be converted to responses format, skipping",
			logger.F("stop_words", strings.Join(req.StopWords, ",")))
	}
	resp := make(map[string]interface{})
	if extra, ok := req.Metadata["openai_responses_extra"].(map[string]interface{}); ok {
		for k, v := range extra {
			resp[k] = v
		}
	}
	if extra, ok := req.Metadata["openai_chat_extra"].(map[string]interface{}); ok {
		copyAllowedFields(resp, extra, openAICommonRequestExtraKeys())
	}
	resp["model"] = req.Model
	if req.Stream {
		resp["stream"] = true
	}
	if req.Temperature != nil {
		resp["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		resp["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		resp["max_output_tokens"] = *req.MaxTokens
	}

	// Emit explicit InternalRequest fields (override metadata equivalents)
	if req.Store != nil {
		resp["store"] = *req.Store
	}
	if req.Reasoning != nil {
		resp["reasoning"] = req.Reasoning
	}
	if req.ParallelToolCalls != nil {
		resp["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.ServiceTier != "" {
		resp["service_tier"] = req.ServiceTier
	}
	if req.TopLogProbs != nil {
		resp["top_logprobs"] = *req.TopLogProbs
	}

	// Merge ExtraParams for same-protocol passthrough
	// Explicit struct fields take precedence over ExtraParams
	for k, v := range req.ExtraParams {
		if _, exists := resp[k]; !exists {
			resp[k] = v
		}
	}

	// Convert messages to input + instructions
	var instructions string
	var input []interface{}

	for _, im := range req.Messages {
		switch im.Role {
		case "system":
			text, err := instructionTextFromContent(im.Content)
			if err != nil {
				return nil, err
			}
			if text != "" {
				instructions = appendInstructions(instructions, text)
			}
		case "developer":
			text, err := instructionTextFromContent(im.Content)
			if err != nil {
				return nil, err
			}
			if text != "" {
				instructions = appendInstructions(instructions, text)
			}
		default:
			if len(im.ToolCalls) > 0 {
				// Add tool_calls as separate function_call items (buildResponsesInputMessage doesn't include them)
				for _, tc := range im.ToolCalls {
					input = append(input, map[string]interface{}{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Name,
						"arguments": tc.Arguments,
					})
				}
				// Also add message content if present
				if len(im.Content) > 0 {
					input = append(input, buildResponsesInputMessage(im))
				}
				continue
			}
			if im.ToolResult != nil {
				input = append(input, map[string]interface{}{
					"type":    "function_call_output",
					"call_id": im.ToolResult.ToolCallID,
					"output":  im.ToolResult.Content,
				})
				continue
			}
			input = append(input, buildResponsesInputMessage(im))
		}
	}

	if instructions != "" {
		resp["instructions"] = instructions
	}
	resp["input"] = input

	// Tools
	if rawTools, ok := req.Metadata["openai_responses_tools_raw"]; ok {
		resp["tools"] = rawTools
	} else if len(req.Tools) > 0 {
		tools := make([]interface{}, 0, len(req.Tools))
		for _, it := range req.Tools {
			toolType := it.Type
			if toolType == "" || toolType == "function" {
				toolType = "function"
			}
			tools = append(tools, map[string]interface{}{
				"type":        toolType,
				"name":        it.Name,
				"description": it.Description,
				"parameters":  it.Parameters,
			})
		}
		resp["tools"] = tools
	}

	// ToolChoice
	if rawToolChoice, ok := req.Metadata["openai_responses_tool_choice_raw"]; ok {
		resp["tool_choice"] = rawToolChoice
	} else if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto", "none", "required":
			resp["tool_choice"] = req.ToolChoice.Type
		default:
			resp["tool_choice"] = map[string]interface{}{
				"type": "function",
				"name": req.ToolChoice.Function,
			}
		}
	}

	return json.Marshal(resp)
}

func buildResponsesInputMessage(im provider.InternalMessage) map[string]interface{} {
	msg := map[string]interface{}{
		"role": im.Role,
	}
	textType := "input_text"
	if im.Role == "assistant" {
		textType = "output_text"
	}
	content := make([]interface{}, 0, len(im.Content))
	for _, part := range im.Content {
		switch part.Type {
		case "text":
			content = append(content, map[string]interface{}{
				"type": textType,
				"text": part.Text,
			})
		case "image_url":
			if part.ImageURL != nil && *part.ImageURL != "" {
				item := map[string]interface{}{
					"type":      "input_image",
					"image_url": *part.ImageURL,
				}
				if part.ImageDetail != "" {
					item["detail"] = part.ImageDetail
				}
				content = append(content, item)
			}
		}
	}
	if len(content) == 0 {
		msg["content"] = ""
	} else if len(content) == 1 {
		if text, ok := content[0].(map[string]interface{}); ok && text["type"] == "input_text" {
			msg["content"] = []interface{}{text}
		} else {
			msg["content"] = content
		}
	} else {
		msg["content"] = content
	}
	return msg
}

func instructionTextFromContent(parts []provider.InternalContentPart) (string, error) {
	var texts []string
	for _, part := range parts {
		if part.Type != "text" || part.ImageURL != nil {
			return "", fmt.Errorf("system/developer non-text content cannot be converted to Responses instructions")
		}
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n\n"), nil
}

func appendInstructions(base, text string) string {
	if base == "" {
		return text
	}
	if text == "" {
		return base
	}
	return base + "\n\n" + text
}

func copyFields(src map[string]interface{}, keys []string) map[string]interface{} {
	out := make(map[string]interface{})
	for _, key := range keys {
		if v, ok := src[key]; ok {
			out[key] = v
		}
	}
	return out
}

func sortedMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyAllowedFields(dst, src map[string]interface{}, allowed map[string]struct{}) {
	for key := range src {
		if _, ok := allowed[key]; ok {
			dst[key] = src[key]
		}
	}
}

func unsupportedChatExtrasForResponses(extra map[string]interface{}) []string {
	allowed := openAICommonRequestExtraKeys()
	allowed["max_completion_tokens"] = struct{}{}
	return unsupportedExtraKeys(extra, allowed)
}

func unsupportedResponseExtrasForChat(extra map[string]interface{}) []string {
	return unsupportedExtraKeys(extra, openAICommonRequestExtraKeys())
}

func unsupportedExtraKeys(extra map[string]interface{}, allowed map[string]struct{}) []string {
	var unsupported []string
	for key := range extra {
		if _, ok := allowed[key]; !ok {
			unsupported = append(unsupported, key)
		}
	}
	sort.Strings(unsupported)
	return unsupported
}

func openAICommonRequestExtraKeys() map[string]struct{} {
	return map[string]struct{}{
		"stream_options":      {},
		"parallel_tool_calls": {},
		"service_tier":        {},
		"metadata":            {},
		"user":                {},
	}
}

func parseResponsesToolChoice(tc interface{}) *provider.InternalToolChoice {
	switch v := tc.(type) {
	case string:
		return &provider.InternalToolChoice{Type: v}
	case map[string]interface{}:
		typ, _ := v["type"].(string)
		itc := &provider.InternalToolChoice{Type: typ}
		if name, _ := v["name"].(string); name != "" {
			itc.Function = name
		}
		if fn, ok := v["function"].(map[string]interface{}); ok {
			itc.Function, _ = fn["name"].(string)
		}
		return itc
	default:
		return &provider.InternalToolChoice{Type: "auto"}
	}
}

func parseResponsesTools(items []interface{}) []provider.InternalTool {
	tools := make([]provider.InternalTool, 0, len(items))
	for _, raw := range items {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ == "" {
			typ = "function"
		}
		name, _ := m["name"].(string)
		description, _ := m["description"].(string)
		parameters := m["parameters"]
		if fn, ok := m["function"].(map[string]interface{}); ok {
			if name == "" {
				name, _ = fn["name"].(string)
			}
			if description == "" {
				description, _ = fn["description"].(string)
			}
			if parameters == nil {
				parameters = fn["parameters"]
			}
		}
		tools = append(tools, provider.InternalTool{Type: typ, Name: name, Description: description, Parameters: parameters})
	}
	return tools
}

func validateOpenAIChatToolsConvertible(items []interface{}) error {
	for _, raw := range items {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "" && typ != "function" {
			return fmt.Errorf("openai chat tool type %q cannot be converted to non-chat upstream formats", typ)
		}
		fn, ok := m["function"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("openai chat function tool is missing function definition")
		}
		name, _ := fn["name"].(string)
		if name == "" {
			return fmt.Errorf("openai chat function tool requires a name")
		}
	}
	return nil
}

func validateOpenAIChatToolChoiceConvertible(tc interface{}) error {
	switch v := tc.(type) {
	case string:
		switch v {
		case "", "auto", "none", "required":
			return nil
		default:
			return fmt.Errorf("openai chat tool_choice %q cannot be converted to non-chat upstream formats", v)
		}
	case map[string]interface{}:
		typ, _ := v["type"].(string)
		if typ != "function" {
			return fmt.Errorf("openai chat tool_choice type %q cannot be converted to non-chat upstream formats", typ)
		}
		fn, ok := v["function"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("openai chat function tool_choice requires function object")
		}
		name, _ := fn["name"].(string)
		if name == "" {
			return fmt.Errorf("openai chat function tool_choice requires function.name")
		}
		return nil
	case nil:
		return nil
	default:
		return fmt.Errorf("openai chat tool_choice cannot be converted to non-chat upstream formats")
	}
}

func validateResponsesToolsConvertible(items []interface{}) error {
	for _, raw := range items {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ == "" || typ == "function" {
			continue
		}
		return fmt.Errorf("openai responses tool type %q cannot be converted to non-responses upstream formats", typ)
	}
	return nil
}

func validateResponsesToolChoiceConvertible(tc interface{}) error {
	switch v := tc.(type) {
	case string:
		switch v {
		case "", "auto", "none", "required":
			return nil
		default:
			return fmt.Errorf("openai responses tool_choice %q cannot be converted to non-responses upstream formats", v)
		}
	case map[string]interface{}:
		typ, _ := v["type"].(string)
		switch typ {
		case "function":
			name, _ := v["name"].(string)
			if name == "" {
				if fn, ok := v["function"].(map[string]interface{}); ok {
					name, _ = fn["name"].(string)
				}
			}
			if name == "" {
				return fmt.Errorf("openai responses function tool_choice requires a function name")
			}
			return nil
		case "":
			return fmt.Errorf("openai responses tool_choice object requires a type")
		default:
			return fmt.Errorf("openai responses tool_choice type %q cannot be converted to non-responses upstream formats", typ)
		}
	case nil:
		return nil
	default:
		return fmt.Errorf("openai responses tool_choice cannot be converted to non-responses upstream formats")
	}
}

func intfStr(v interface{}) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
