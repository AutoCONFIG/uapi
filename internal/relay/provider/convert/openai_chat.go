package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseOpenAIChatRequestDirectIR(body []byte) (*relayir.Request, error) {
	var req schema.OpenAIChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Chat request: %w", err)
	}
	out := &relayir.Request{
		SourceProtocol: relayir.ProtocolOpenAIChat,
		Model:          req.Model,
		Stream:         req.Stream,
		Native: relayir.NativeEnvelope{
			Protocol: relayir.ProtocolOpenAIChat,
			RawBody:  relayir.CloneRaw(body),
		},
		Metadata: relayir.CloneRawMap(req.Extra),
	}
	out.Native.Fields = relayir.CloneRawMap(out.Metadata)
	out.Native.Unknown = relayir.CloneRawMap(out.Metadata)

	for _, msg := range req.Messages {
		rawMsg := rawJSON(msg)
		switch msg.Role {
		case "system", "developer":
			inst := relayir.Instruction{
				Role:     relayir.Role(msg.Role),
				Text:     msg.Content.ExtractText(),
				Name:     msg.Name,
				Metadata: relayir.CloneRawMap(msg.Extra),
				Native:   relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIChat, Kind: "message", Raw: rawMsg, Fields: relayir.CloneRawMap(msg.Extra)},
			}
			content := chatContentParts(msg.Content)
			for idx, part := range content {
				inst.Items = append(inst.Items, irContentPartItem(contentItemKindContent, part, rawJSON(part), FormatOpenAIChatCompletions, idx))
			}
			out.Instructions = append(out.Instructions, inst)
			continue
		}
		out.Turns = append(out.Turns, openAIChatMessageToIRTurn(msg, rawMsg))
	}

	out.Generation.MaxTokens = req.MaxTokens
	out.Generation.MaxTokensField = "max_tokens"
	if req.MaxCompletionTokens != nil {
		out.Generation.MaxTokens = req.MaxCompletionTokens
		out.Generation.MaxTokensField = "max_completion_tokens"
	}
	out.Generation.Temperature = req.Temperature
	out.Generation.TopP = req.TopP
	out.Generation.Stop = chatStopWords(req.Stop)
	out.Generation.FrequencyPenalty = req.FrequencyPenalty
	out.Generation.PresencePenalty = req.PresencePenalty
	out.Generation.N = req.N
	if req.Seed != nil {
		seed := int64(*req.Seed)
		out.Generation.Seed = &seed
	}
	out.Generation.LogProbs = req.LogProbs
	out.Generation.TopLogProbs = req.TopLogProbs
	out.Generation.ResponseFormat = relayir.CloneRaw(req.ResponseFormat)
	out.Generation.LogitBias = relayir.CloneRaw(req.LogitBias)
	out.Generation.ParallelToolCalls = req.ParallelToolCalls
	out.Generation.ServiceTier = req.ServiceTier
	out.Generation.Store = req.Store
	out.Generation.User = req.User
	if len(req.StreamOptions) > 0 {
		if out.Generation.Extra == nil {
			out.Generation.Extra = map[string]json.RawMessage{}
		}
		out.Generation.Extra["stream_options"] = relayir.CloneRaw(req.StreamOptions)
	}
	if req.ReasoningEffort != "" {
		raw, _ := json.Marshal(map[string]string{"effort": req.ReasoningEffort})
		out.Generation.Reasoning = raw
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, irTool(tool, FormatOpenAIChatCompletions))
	}
	if req.ToolChoice != nil {
		out.ToolChoice = &relayir.ToolChoice{Raw: relayir.CloneRaw(req.ToolChoice)}
	}
	return out, nil
}

func openAIChatMessageToIRTurn(msg schema.ChatMessage, rawMsg json.RawMessage) relayir.Turn {
	turn := relayir.Turn{
		Role:     relayir.Role(openAIChatRole(msg.Role)),
		Name:     msg.Name,
		Metadata: relayir.CloneRawMap(msg.Extra),
		Native:   relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIChat, Kind: "message", Raw: rawMsg, Fields: relayir.CloneRawMap(msg.Extra)},
	}
	for idx, part := range reasoningPartsFromOpenAIChatExtra(msg.Extra) {
		turn.Items = append(turn.Items, irContentPartItem(contentItemKindReasoning, part, rawJSON(part), FormatOpenAIChatCompletions, idx))
	}
	if msg.Role != "tool" {
		for idx, part := range chatContentParts(msg.Content) {
			turn.Items = append(turn.Items, irContentPartItem(contentItemKindContent, part, rawJSON(part), FormatOpenAIChatCompletions, idx))
		}
	}
	for idx, tc := range msg.ToolCalls {
		call := schema.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Name: tc.Function.Name,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
		}
		turn.Items = append(turn.Items, irToolUseItem(call, rawJSON(tc), FormatOpenAIChatCompletions, idx))
	}
	if msg.Role == "tool" {
		turn.Items = append(turn.Items, irToolResultItem(schema.ToolResult{
			ToolCallID: msg.ToolCallID,
			Content:    msg.Content.ExtractText(),
			ContentRaw: rawJSON(msg.Content),
		}, rawMsg, FormatOpenAIChatCompletions, len(turn.Items)))
	}
	return turn
}

func emitOpenAIChatRequestDirectIR(req *relayir.Request) ([]byte, error) {
	preserveNative := req.SourceProtocol == relayir.ProtocolOpenAIChat
	out := schema.OpenAIChatRequest{
		Model:  req.Model,
		Stream: req.Stream,
	}
	if preserveNative {
		out.Extra = relayir.CloneRawMap(req.Metadata)
	}
	for _, inst := range req.Instructions {
		out.Messages = append(out.Messages, openAIChatInstructionMessage(inst))
	}
	for _, turn := range req.Turns {
		msgs, err := openAIChatTurnMessages(turn)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, msgs...)
	}
	if req.Generation.MaxTokens != nil {
		if req.Generation.MaxTokensField == "max_completion_tokens" {
			out.MaxCompletionTokens = req.Generation.MaxTokens
		} else {
			out.MaxTokens = req.Generation.MaxTokens
		}
	}
	out.Temperature = req.Generation.Temperature
	out.TopP = req.Generation.TopP
	if len(req.Generation.Stop) > 0 {
		if len(req.Generation.Stop) == 1 {
			out.Stop = rawJSON(req.Generation.Stop[0])
		} else {
			out.Stop = rawJSON(req.Generation.Stop)
		}
	}
	out.FrequencyPenalty = req.Generation.FrequencyPenalty
	out.PresencePenalty = req.Generation.PresencePenalty
	out.N = req.Generation.N
	if req.Generation.Seed != nil {
		seed := int(*req.Generation.Seed)
		out.Seed = &seed
	}
	out.LogProbs = req.Generation.LogProbs
	out.TopLogProbs = req.Generation.TopLogProbs
	out.ResponseFormat = relayir.CloneRaw(req.Generation.ResponseFormat)
	out.LogitBias = relayir.CloneRaw(req.Generation.LogitBias)
	out.ParallelToolCalls = req.Generation.ParallelToolCalls
	out.ServiceTier = req.Generation.ServiceTier
	out.Store = req.Generation.Store
	out.User = req.Generation.User
	if raw := req.Generation.Extra["stream_options"]; len(raw) > 0 {
		out.StreamOptions = relayir.CloneRaw(raw)
	}
	if !preserveNative && out.Stream && len(out.StreamOptions) == 0 {
		out.StreamOptions = json.RawMessage(`{"include_usage":true}`)
	}
	if effort := rawStringFromObject(req.Generation.Reasoning, "effort"); effort != "" {
		out.ReasoningEffort = effort
	}
	if len(req.Tools) > 0 {
		tools := make([]schema.Tool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, schemaToolFromIR(tool))
		}
		out.Tools = openAIChatTools(tools, preserveNative)
		if req.ToolChoice == nil {
			out.ToolChoice = json.RawMessage(`"auto"`)
		}
		if out.ParallelToolCalls == nil {
			parallel := true
			out.ParallelToolCalls = &parallel
		}
	}
	if req.ToolChoice != nil {
		if preserveNative {
			out.ToolChoice = relayir.CloneRaw(req.ToolChoice.Raw)
		} else {
			out.ToolChoice = projectFunctionToolChoice(relayir.CloneRaw(req.ToolChoice.Raw))
		}
	}
	if preserveNative {
		if out.Extra == nil {
			out.Extra = map[string]json.RawMessage{}
		}
		for k, v := range req.Native.Fields {
			out.Extra[k] = relayir.CloneRaw(v)
		}
	} else {
		out.Extra = openAIChatCrossProtocolRequestExtra(req.Metadata)
		sanitizeOpenAIChatCrossProtocolRequest(&out)
	}
	return json.Marshal(out)
}

func sanitizeOpenAIChatCrossProtocolRequest(req *schema.OpenAIChatRequest) {
	req.Extra = openAIChatCrossProtocolRequestExtra(req.Extra)
	for i := range req.Messages {
		req.Messages[i].Extra = openAIChatAllowedCacheExtra(req.Messages[i].Extra)
		for j := range req.Messages[i].Content.Parts {
			req.Messages[i].Content.Parts[j].Extra = openAIChatAllowedCacheExtra(req.Messages[i].Content.Parts[j].Extra)
		}
	}
	req.Messages = reorderOpenAIChatToolResults(req.Messages)
	req.Messages = dropEmptyOpenAIChatMessages(req.Messages)
	for i := range req.Tools {
		req.Tools[i].Extra = openAIChatAllowedCacheExtra(req.Tools[i].Extra)
		if req.Tools[i].Function != nil {
			req.Tools[i].Function.Extra = openAIChatAllowedFunctionExtra(req.Tools[i].Function.Extra)
		}
	}
}

func openAIChatCrossProtocolRequestExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	allowed := []string{"prompt_cache_key", "client_metadata", "safety_identifier"}
	out := map[string]json.RawMessage{}
	for _, key := range allowed {
		if raw := extra[key]; len(raw) > 0 {
			out[key] = relayir.CloneRaw(raw)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func reorderOpenAIChatToolResults(messages []schema.ChatMessage) []schema.ChatMessage {
	out := make([]schema.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "tool" || msg.ToolCallID == "" {
			out = append(out, msg)
			continue
		}
		callIdx := lastOpenAIChatToolCallMessageIndex(out, msg.ToolCallID)
		if callIdx < 0 {
			out = append(out, msg)
			continue
		}
		insertAt := callIdx + 1
		for insertAt < len(out) && out[insertAt].Role == "tool" {
			insertAt++
		}
		out = append(out, schema.ChatMessage{})
		copy(out[insertAt+1:], out[insertAt:])
		out[insertAt] = msg
	}
	return out
}

func lastOpenAIChatToolCallMessageIndex(messages []schema.ChatMessage, callID string) int {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, call := range messages[i].ToolCalls {
			if call.ID == callID {
				return i
			}
		}
	}
	return -1
}

func dropEmptyOpenAIChatMessages(messages []schema.ChatMessage) []schema.ChatMessage {
	out := messages[:0]
	for _, msg := range messages {
		if msg.Role != "tool" && msg.Content.IsEmpty() && len(msg.ToolCalls) == 0 && msg.Refusal == "" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func openAIChatAllowedFunctionExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if len(extra) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	if strict, ok := extra["strict"]; ok {
		out["strict"] = relayir.CloneRaw(strict)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func openAIChatAllowedCacheExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if len(extra) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	if cacheControl, ok := extra["cache_control"]; ok {
		out["cache_control"] = relayir.CloneRaw(cacheControl)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func openAIChatInstructionMessage(inst relayir.Instruction) schema.ChatMessage {
	role := string(inst.Role)
	if role == "" {
		role = "system"
	}
	msg := schema.ChatMessage{Role: role, Name: inst.Name, Extra: relayir.CloneRawMap(inst.Metadata)}
	parts := contentPartsFromIRItems(inst.Items)
	if len(parts) == 0 && inst.Text != "" {
		msg.Content = schema.NewTextContent(inst.Text)
	} else {
		msg.Content = schema.NewPartsContent(openAIChatContentParts(parts)...)
	}
	return msg
}

func openAIChatTurnMessage(turn relayir.Turn) (schema.ChatMessage, error) {
	msgs, err := openAIChatTurnMessages(turn)
	if err != nil {
		return schema.ChatMessage{}, err
	}
	if len(msgs) == 0 {
		return schema.ChatMessage{Role: openAIChatRole(string(turn.Role)), Name: turn.Name, Extra: relayir.CloneRawMap(turn.Metadata)}, nil
	}
	return msgs[0], nil
}

func openAIChatTurnMessages(turn relayir.Turn) ([]schema.ChatMessage, error) {
	if openAIChatTurnHasToolResults(turn) {
		return openAIChatTurnMessagesWithToolResults(turn)
	}
	role := openAIChatRole(string(turn.Role))
	msg := schema.ChatMessage{Role: role, Name: turn.Name, Extra: relayir.CloneRawMap(turn.Metadata)}
	var content []schema.ContentPart
	var reasoning []schema.ContentPart
	for _, item := range turn.Items {
		switch item.Kind {
		case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
			reasoning = append(reasoning, schemaReasoningFromIR(item))
		case relayir.ItemToolUse, relayir.ItemFunctionCall:
			call := schemaToolCallFromIR(item)
			if firstNonEmptyString(call.Name, call.Function.Name) == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Chat tool_call for IR item %d: missing required function name", item.OriginalIndex)
			}
			if firstNonEmptyString(call.ID, item.CallID, item.ID) == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Chat tool_call for IR item %d: missing required id", item.OriginalIndex)
			}
			msg.ToolCalls = append(msg.ToolCalls, call)
		case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
			result := schemaToolResultFromIR(item)
			if result.ToolCallID == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Chat tool result for IR item %d: missing required tool_call_id", item.OriginalIndex)
			}
			msg.Role = "tool"
			msg.ToolCallID = result.ToolCallID
			if len(result.ContentRaw) > 0 {
				var mc schema.MessageContent
				if json.Unmarshal(result.ContentRaw, &mc) == nil && !mc.IsEmpty() {
					msg.Content = mc
					continue
				}
			}
			msg.Content = schema.NewTextContent(result.Content)
		default:
			if part, ok := schemaContentFromIR(item); ok {
				content = append(content, part)
			}
		}
	}
	if role != "tool" {
		parts := openAIChatContentParts(content)
		if len(parts) == 1 && parts[0].Type == "text" && parts[0].Text != "" && len(parts[0].Extra) == 0 {
			msg.Content = schema.NewTextContent(parts[0].Text)
		} else if len(parts) > 0 {
			msg.Content = schema.NewPartsContent(parts...)
		}
	}
	if len(reasoning) > 0 {
		if msg.Extra == nil {
			msg.Extra = map[string]json.RawMessage{}
		}
		if text := contentPartsText(reasoning); text != "" {
			msg.Extra["reasoning_content"] = rawJSON(text)
		}
		if details := reasoningDetailsFromParts(reasoning); len(details) > 0 {
			msg.Extra["reasoning_details"] = rawJSON(details)
		}
	}
	return []schema.ChatMessage{msg}, nil
}

func openAIChatTurnHasToolResults(turn relayir.Turn) bool {
	for _, item := range turn.Items {
		switch item.Kind {
		case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
			return true
		}
	}
	return false
}

func openAIChatTurnMessagesWithToolResults(turn relayir.Turn) ([]schema.ChatMessage, error) {
	role := openAIChatRole(string(turn.Role))
	var messages []schema.ChatMessage
	var content []schema.ContentPart
	var reasoning []schema.ContentPart

	for _, item := range turn.Items {
		switch item.Kind {
		case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
			reasoning = append(reasoning, schemaReasoningFromIR(item))
		case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
			msg, err := openAIChatToolResultMessage(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, msg)
		default:
			if part, ok := schemaContentFromIR(item); ok {
				content = append(content, part)
			}
		}
	}

	if len(content) > 0 || len(reasoning) > 0 {
		msg := schema.ChatMessage{Role: role, Name: turn.Name, Extra: relayir.CloneRawMap(turn.Metadata)}
		parts := openAIChatContentParts(content)
		if len(parts) == 1 && parts[0].Type == "text" && parts[0].Text != "" && len(parts[0].Extra) == 0 {
			msg.Content = schema.NewTextContent(parts[0].Text)
		} else if len(parts) > 0 {
			msg.Content = schema.NewPartsContent(parts...)
		}
		if len(reasoning) > 0 {
			if msg.Extra == nil {
				msg.Extra = map[string]json.RawMessage{}
			}
			if text := contentPartsText(reasoning); text != "" {
				msg.Extra["reasoning_content"] = rawJSON(text)
			}
			if details := reasoningDetailsFromParts(reasoning); len(details) > 0 {
				msg.Extra["reasoning_details"] = rawJSON(details)
			}
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func openAIChatToolResultMessage(item relayir.Item) (schema.ChatMessage, error) {
	result := schemaToolResultFromIR(item)
	if result.ToolCallID == "" {
		return schema.ChatMessage{}, fmt.Errorf("cannot emit OpenAI Chat tool result for IR item %d: missing required tool_call_id", item.OriginalIndex)
	}
	msg := schema.ChatMessage{Role: "tool", ToolCallID: result.ToolCallID, Extra: openAIChatAllowedCacheExtra(item.Metadata)}
	if len(result.ContentRaw) > 0 {
		var mc schema.MessageContent
		if json.Unmarshal(result.ContentRaw, &mc) == nil && !mc.IsEmpty() {
			msg.Content = mc
			return msg, nil
		}
	}
	msg.Content = schema.NewTextContent(result.Content)
	return msg, nil
}

func contentPartsFromIRItems(items []relayir.Item) []schema.ContentPart {
	var out []schema.ContentPart
	for _, item := range items {
		if isAnthropicTransportTextItem(item) {
			continue
		}
		if part, ok := schemaContentFromIR(item); ok {
			out = append(out, part)
		}
	}
	return out
}

func chatContentParts(content schema.MessageContent) []schema.ContentPart {
	if content.Text != nil {
		return []schema.ContentPart{{Type: "text", Text: *content.Text}}
	}
	return content.Parts
}

func chatStopWords(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	return nil
}

func rawStringFromObject(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	return rawString(obj[key])
}

func openAIChatTools(tools []schema.Tool, preserveNative bool) []schema.Tool {
	projected := tools
	if !preserveNative {
		projected = functionToolProjections(tools)
	}
	out := make([]schema.Tool, 0, len(projected))
	for _, tool := range projected {
		normalized, ok := openAIChatTool(tool, preserveNative)
		if ok {
			out = append(out, normalized)
		}
	}
	return out
}

func openAIChatTool(tool schema.Tool, preserveNative bool) (schema.Tool, bool) {
	name, description, parameters := normalizedFunctionTool(tool)
	if name != "" {
		functionExtra := map[string]json.RawMessage(nil)
		if tool.Function != nil {
			functionExtra = tool.Function.Extra
		}
		return schema.Tool{
			Type: "function",
			Function: &schema.ToolFunction{
				Name:        name,
				Description: description,
				Parameters:  parameters,
				Extra:       functionExtra,
			},
			Extra: func() map[string]json.RawMessage {
				if preserveNative {
					return tool.Extra
				}
				return openAIChatAllowedCacheExtra(tool.Extra)
			}(),
		}, true
	}
	if preserveNative && tool.Type != "" {
		return tool, true
	}
	return schema.Tool{}, false
}

func openAIChatRole(role string) string {
	switch role {
	case "model":
		return "assistant"
	case "function":
		return "tool"
	case "unknown", "opaque", "":
		return "user"
	default:
		return role
	}
}

func openAIChatContentParts(parts []schema.ContentPart) []schema.ContentPart {
	out := make([]schema.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == "input_file" {
			part.Type = "file"
		}
		if part.Type == "input_image" {
			part.Type = "image_url"
		}
		if part.Type == "input_text" || part.Type == "output_text" {
			part.Type = "text"
		}
		if part.Type == "file" {
			part = openAIChatFilePart(part)
		}
		out = append(out, part)
	}
	return out
}

func openAIChatFilePart(part schema.ContentPart) schema.ContentPart {
	if openAIChatFilePartIsPDF(part) && (part.FileData != "" || part.FileURL != "") {
		imageURL := firstNonEmptyString(part.FileData, part.FileURL)
		return schema.ContentPart{
			Type:     "image_url",
			ImageURL: &imageURL,
		}
	}
	return schema.ContentPart{
		Type:     "file",
		FileData: part.FileData,
		FileID:   part.FileID,
		Filename: part.Filename,
	}
}

func openAIChatFilePartIsPDF(part schema.ContentPart) bool {
	mimeType := strings.ToLower(firstNonEmptyString(part.FileType, part.MimeType))
	if mimeType == "application/pdf" {
		return true
	}
	if parsedMime, _, ok := splitDataURI(part.FileData); ok && strings.EqualFold(parsedMime, "application/pdf") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(part.Filename), ".pdf")
}

// joinNonEmpty joins non-empty strings with the given separator.
func joinNonEmpty(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if s == "" {
			continue
		}
		if i > 0 && result != "" {
			result += sep
		}
		result += s
	}
	return result
}

func init() {
	registerRequestIRParser(FormatOpenAIChatCompletions, parseOpenAIChatRequestIR)
	registerRequestIREmitter(FormatOpenAIChatCompletions, emitOpenAIChatRequestIR)
}
