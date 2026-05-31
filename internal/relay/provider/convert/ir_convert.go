package convert

import (
	"encoding/json"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func ToIR(format Format, body []byte) (*relayir.Request, error) {
	body = cleanJSONUndefinedPlaceholders(body)
	toIR, ok := requestIRParsers[format]
	if !ok {
		return nil, &NoRequestParserError{Format: format}
	}
	return toIR(body)
}

func FromIR(req *relayir.Request, target Format) ([]byte, error) {
	if req.TargetProtocol != "" {
		target = protocolFormat(req.TargetProtocol)
	}
	from, ok := requestIREmitters[target]
	if !ok {
		return nil, &NoRequestEmitterError{Format: target}
	}
	return from(req)
}

type NoRequestParserError struct {
	Format Format
}

func (e *NoRequestParserError) Error() string {
	return "no request parser for format " + string(e.Format)
}

type NoRequestEmitterError struct {
	Format Format
}

func (e *NoRequestEmitterError) Error() string {
	return "no request emitter for format " + string(e.Format)
}

func RequestEnvelopeFromIR(req *relayir.Request) *RequestEnvelope {
	if req == nil {
		return nil
	}
	out := &RequestEnvelope{
		Model:          req.Model,
		Stream:         req.Stream,
		RawRequestBody: relayir.CloneRaw(req.Native.RawBody),
		Extra:          relayir.CloneRawMap(req.Metadata),
		SourceFormat:   protocolFormat(req.SourceProtocol),
		Losses:         append([]relayir.Loss(nil), req.Losses...),
	}
	if len(req.Native.Fields) > 0 {
		out.Extra = relayir.CloneRawMap(req.Native.Fields)
	}
	if len(req.Instructions) > 0 {
		text := req.Instructions[0].Text
		if text == "" {
			text = instructionText(req.Instructions[0])
		}
		if text != "" {
			out.Instructions = &text
		}
		out.InstructionsRaw = relayir.CloneRaw(req.Instructions[0].Native.Raw)
	}
	out.MaxTokens = req.Generation.MaxTokens
	out.MaxTokensField = req.Generation.MaxTokensField
	out.Temperature = req.Generation.Temperature
	out.TopP = req.Generation.TopP
	out.TopK = req.Generation.TopK
	out.StopWords = append([]string(nil), req.Generation.Stop...)
	out.N = req.Generation.N
	out.CandidateCount = req.Generation.CandidateCount
	out.Seed = req.Generation.Seed
	out.LogProbs = req.Generation.LogProbs
	out.TopLogProbs = req.Generation.TopLogProbs
	out.FrequencyPenalty = req.Generation.FrequencyPenalty
	out.PresencePenalty = req.Generation.PresencePenalty
	out.ResponseFormat = relayir.CloneRaw(req.Generation.ResponseFormat)
	out.Reasoning = relayir.CloneRaw(req.Generation.Reasoning)
	out.Thinking = relayir.CloneRaw(req.Generation.Thinking)
	out.ServiceTier = req.Generation.ServiceTier
	out.Store = req.Generation.Store
	out.ParallelToolCalls = req.Generation.ParallelToolCalls
	out.GeminiGenerationConfigExtra = relayir.CloneRawMap(req.Generation.Extra)
	out.SafetySettings = relayir.CloneRaw(req.Safety.Settings)
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, schemaToolFromIR(tool))
	}
	if req.ToolChoice != nil {
		out.ToolChoice = relayir.CloneRaw(req.ToolChoice.Raw)
	}
	for _, turn := range req.Turns {
		out.Messages = append(out.Messages, requestMessageFromIRTurn(turn))
	}
	out.IR = req
	return out
}

func instructionText(inst relayir.Instruction) string {
	for _, item := range inst.Items {
		if item.Text != nil && item.Text.Text != "" {
			return item.Text.Text
		}
	}
	return ""
}

func requestMessageFromIRTurn(turn relayir.Turn) RequestMessage {
	msg := RequestMessage{
		Role:    string(turn.Role),
		Name:    turn.Name,
		ItemID:  turn.ID,
		Status:  turn.Status,
		Phase:   turn.Phase,
		RawItem: relayir.CloneRaw(turn.Native.Raw),
		Extra:   relayir.CloneRawMap(turn.Metadata),
	}
	for _, item := range turn.Items {
		switch item.Kind {
		case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
			appendReasoningItem(&msg, schemaReasoningFromIR(item), relayir.CloneRaw(item.Native.Raw))
		case relayir.ItemToolUse, relayir.ItemFunctionCall:
			appendToolCallItem(&msg, schemaToolCallFromIR(item), relayir.CloneRaw(item.Native.Raw))
		case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
			appendToolResultItem(&msg, schemaToolResultFromIR(item), relayir.CloneRaw(item.Native.Raw))
		default:
			if part, ok := schemaContentFromIR(item); ok {
				appendContentItem(&msg, part, relayir.CloneRaw(item.Native.Raw))
			}
		}
	}
	return msg
}

func schemaContentFromIR(item relayir.Item) (schema.ContentPart, bool) {
	switch item.Kind {
	case relayir.ItemText:
		if item.Text == nil {
			return schema.ContentPart{}, false
		}
		return schema.ContentPart{Type: "text", Text: item.Text.Text, Extra: relayir.CloneRawMap(item.Metadata)}, true
	case relayir.ItemImage:
		if item.Image == nil {
			return schema.ContentPart{}, false
		}
		imageURL := item.Image.URL
		if imageURL == "" {
			imageURL = item.Image.DataURI
		}
		return schema.ContentPart{Type: "image_url", ImageURL: stringPtr(imageURL), ImageDetail: item.Image.Detail, MimeType: item.Image.MimeType, Extra: relayir.CloneRawMap(item.Metadata)}, true
	case relayir.ItemFile, relayir.ItemDocument:
		file := item.File
		if file == nil {
			file = item.Document
		}
		if file == nil {
			return schema.ContentPart{}, false
		}
		return schema.ContentPart{Type: "file", FileURL: file.URL, FileID: file.FileID, FileData: file.DataURI, Filename: file.Name, FileType: file.MimeType, MimeType: file.MimeType, Extra: relayir.CloneRawMap(item.Metadata)}, true
	case relayir.ItemAudio:
		if item.Audio == nil {
			return schema.ContentPart{}, false
		}
		return schema.ContentPart{Type: "audio", Data: item.Audio.DataURI, MimeType: item.Audio.MimeType, Extra: relayir.CloneRawMap(item.Metadata)}, true
	case relayir.ItemRefusal:
		if item.Refusal == nil {
			return schema.ContentPart{}, false
		}
		return schema.ContentPart{Type: "refusal", Refusal: item.Refusal.Text, Extra: relayir.CloneRawMap(item.Metadata)}, true
	case relayir.ItemExecutableCode:
		if item.ExecutableCode == nil {
			return schema.ContentPart{}, false
		}
		return schema.ContentPart{Type: "executable_code", Text: item.ExecutableCode.Code, Extra: setRawString(nil, "language", item.ExecutableCode.Language)}, true
	case relayir.ItemCodeExecutionResult:
		if item.CodeExecutionResult == nil {
			return schema.ContentPart{}, false
		}
		return schema.ContentPart{Type: "code_execution_result", Text: item.CodeExecutionResult.Output, Extra: setRawString(nil, "outcome", item.CodeExecutionResult.Outcome)}, true
	case relayir.ItemOpaque:
		if item.Opaque == nil {
			return schema.ContentPart{}, false
		}
		return schema.ContentPart{Type: item.Opaque.Type, Text: item.Opaque.Text, Extra: relayir.CloneRawMap(item.Metadata)}, item.Opaque.Type != ""
	default:
		return schema.ContentPart{}, false
	}
}

func schemaReasoningFromIR(item relayir.Item) schema.ContentPart {
	part := schema.ContentPart{Type: "thinking", Extra: relayir.CloneRawMap(item.Metadata)}
	if item.Reasoning == nil {
		return part
	}
	part.Text = item.Reasoning.Text
	if item.Reasoning.Signature != "" {
		part.Extra = setRawString(part.Extra, reasoningExtraSignature, item.Reasoning.Signature)
	}
	if item.Reasoning.ThoughtSignature != "" {
		part.Extra = setRawString(part.Extra, reasoningExtraThoughtSignature, item.Reasoning.ThoughtSignature)
	}
	if item.Reasoning.EncryptedContent != "" {
		part.Extra = setRawString(part.Extra, reasoningExtraEncryptedContent, item.Reasoning.EncryptedContent)
	}
	if item.Reasoning.RedactedContent != "" {
		part.Extra = setRawString(part.Extra, reasoningExtraData, item.Reasoning.RedactedContent)
	}
	if len(item.Reasoning.Details) > 0 {
		if part.Extra == nil {
			part.Extra = make(map[string]json.RawMessage)
		}
		part.Extra["reasoning_details"] = relayir.CloneRaw(item.Reasoning.Details)
	}
	return part
}

func schemaToolCallFromIR(item relayir.Item) schema.ToolCall {
	call := schema.ToolCall{ID: firstNonEmptyString(item.CallID, item.ID), Type: "function", Name: item.Name}
	if item.ToolUse != nil {
		call.ID = firstNonEmptyString(item.ToolUse.CallID, item.ToolUse.ID, call.ID)
		call.Name = firstNonEmptyString(item.ToolUse.Name, call.Name)
		call.Function.Arguments = string(item.ToolUse.Arguments)
		if call.Function.Arguments == "" {
			call.Function.Arguments = item.ToolUse.ArgumentsText
		}
	}
	call.Function.Name = call.Name
	return call
}

func schemaToolResultFromIR(item relayir.Item) schema.ToolResult {
	result := schema.ToolResult{ToolCallID: item.CallID}
	if item.ToolResult != nil {
		result.ToolCallID = firstNonEmptyString(item.ToolResult.CallID, item.ToolResult.ToolUseID, result.ToolCallID)
		result.Content = item.ToolResult.OutputText
		result.IsError = item.ToolResult.IsError
	}
	return result
}

func schemaToolFromIR(tool relayir.Tool) schema.Tool {
	out := schema.Tool{
		Type:        string(tool.Kind),
		Name:        tool.Name,
		Description: tool.Description,
		Parameters:  relayir.CloneRaw(tool.Parameters),
		InputSchema: relayir.CloneRaw(tool.InputSchema),
		Extra:       relayir.CloneRawMap(tool.Metadata),
	}
	if out.Type == "" {
		out.Type = "function"
	}
	if out.Type == "function" {
		out.Function = &schema.ToolFunction{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  relayir.CloneRaw(tool.Parameters),
			Extra:       relayir.CloneRawMap(tool.FunctionMetadata),
		}
	}
	return out
}

func protocolFormat(protocol relayir.Protocol) Format {
	switch protocol {
	case relayir.ProtocolOpenAIChat:
		return FormatOpenAIChatCompletions
	case relayir.ProtocolOpenAIResponses:
		return FormatOpenAIResponses
	case relayir.ProtocolAnthropic:
		return FormatAnthropic
	case relayir.ProtocolGemini:
		return FormatGemini
	case relayir.ProtocolGeminiCLI, relayir.ProtocolAntigravity:
		return FormatGeminiCLI
	default:
		return Format(protocol)
	}
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
