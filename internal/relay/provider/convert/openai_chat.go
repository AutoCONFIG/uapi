package convert

import (
	"encoding/json"
	"fmt"

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
	out := schema.OpenAIChatRequest{
		Model:  req.Model,
		Stream: req.Stream,
		Extra:  relayir.CloneRawMap(req.Metadata),
	}
	for _, inst := range req.Instructions {
		out.Messages = append(out.Messages, openAIChatInstructionMessage(inst))
	}
	for _, turn := range req.Turns {
		msg, err := openAIChatTurnMessage(turn)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, msg)
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
	if effort := rawStringFromObject(req.Generation.Reasoning, "effort"); effort != "" {
		out.ReasoningEffort = effort
	}
	if len(req.Tools) > 0 {
		tools := make([]schema.Tool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, schemaToolFromIR(tool))
		}
		out.Tools = openAIChatTools(tools, req.SourceProtocol == relayir.ProtocolOpenAIChat)
	}
	if req.ToolChoice != nil {
		if req.SourceProtocol == relayir.ProtocolOpenAIChat {
			out.ToolChoice = relayir.CloneRaw(req.ToolChoice.Raw)
		} else {
			out.ToolChoice = projectFunctionToolChoice(relayir.CloneRaw(req.ToolChoice.Raw))
		}
	}
	for k, v := range req.Native.Fields {
		out.Extra[k] = relayir.CloneRaw(v)
	}
	return json.Marshal(out)
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
				return schema.ChatMessage{}, fmt.Errorf("cannot emit OpenAI Chat tool_call for IR item %d: missing required function name", item.OriginalIndex)
			}
			if firstNonEmptyString(call.ID, item.CallID, item.ID) == "" {
				return schema.ChatMessage{}, fmt.Errorf("cannot emit OpenAI Chat tool_call for IR item %d: missing required id", item.OriginalIndex)
			}
			msg.ToolCalls = append(msg.ToolCalls, call)
		case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
			result := schemaToolResultFromIR(item)
			if result.ToolCallID == "" {
				return schema.ChatMessage{}, fmt.Errorf("cannot emit OpenAI Chat tool result for IR item %d: missing required tool_call_id", item.OriginalIndex)
			}
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
	return msg, nil
}

func contentPartsFromIRItems(items []relayir.Item) []schema.ContentPart {
	var out []schema.ContentPart
	for _, item := range items {
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
				return nil
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
	return schema.ContentPart{
		Type:     "file",
		FileData: part.FileData,
		FileID:   part.FileID,
		Filename: part.Filename,
	}
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
