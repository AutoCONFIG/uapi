package convert

import (
	"encoding/json"
	"strings"

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

func instructionText(inst relayir.Instruction) string {
	for _, item := range inst.Items {
		if isAnthropicTransportTextItem(item) {
			continue
		}
		if item.Text != nil && item.Text.Text != "" {
			return item.Text.Text
		}
	}
	return ""
}

func instructionTextForTarget(inst relayir.Instruction) string {
	var texts []string
	removedTransportText := false
	for _, item := range inst.Items {
		if isAnthropicTransportTextItem(item) {
			removedTransportText = true
			continue
		}
		if item.Text != nil && item.Text.Text != "" {
			texts = append(texts, item.Text.Text)
		}
	}
	if removedTransportText {
		return joinNonEmpty(texts, "\n\n")
	}
	if inst.Text != "" {
		return inst.Text
	}
	return joinNonEmpty(texts, "\n\n")
}

func isAnthropicTransportTextItem(item relayir.Item) bool {
	if item.Native.Protocol != relayir.ProtocolAnthropic || item.Text == nil {
		return false
	}
	return isAnthropicTransportText(item.Text.Text)
}

func isAnthropicTransportText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "x-anthropic-billing-header:")
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
	// Only set Function.Name if it's empty, to preserve value from ToolUse or item.Name
	if call.Function.Name == "" {
		call.Function.Name = call.Name
	}
	return call
}

func schemaToolResultFromIR(item relayir.Item) schema.ToolResult {
	result := schema.ToolResult{ToolCallID: item.CallID}
	if item.ToolResult != nil {
		result.ToolCallID = firstNonEmptyString(item.ToolResult.CallID, item.ToolResult.ToolUseID, result.ToolCallID)
		result.Content = item.ToolResult.OutputText
		result.ContentRaw = relayir.CloneRaw(item.ToolResult.OutputRaw)
		result.IsError = item.ToolResult.IsError
	}
	return result
}

func schemaToolFromIR(tool relayir.Tool) schema.Tool {
	parameters := normalizeToolSchemaRaw(firstRawMessage(tool.Parameters, tool.InputSchema))
	inputSchema := normalizeToolSchemaRaw(tool.InputSchema)
	out := schema.Tool{
		Type:        string(tool.Kind),
		Name:        tool.Name,
		Description: tool.Description,
		Parameters:  parameters,
		InputSchema: inputSchema,
		Extra:       relayir.CloneRawMap(tool.Metadata),
	}
	if out.Type == "" {
		out.Type = "function"
	}
	if out.Type == "function" {
		out.Function = &schema.ToolFunction{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  parameters,
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
	case relayir.ProtocolCodex:
		return FormatCodexResponses
	case relayir.ProtocolAnthropic:
		return FormatAnthropic
	case relayir.ProtocolClaudeCode:
		return FormatClaudeCode
	case relayir.ProtocolGemini:
		return FormatGemini
	case relayir.ProtocolGeminiCode:
		return FormatGeminiCode
	case relayir.ProtocolGeminiCLI:
		return FormatGeminiCLI
	case relayir.ProtocolAntigravity:
		return FormatAntigravity
	default:
		return Format(protocol)
	}
}

func firstRawMessage(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 && string(value) != "null" {
			return relayir.CloneRaw(value)
		}
	}
	return nil
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
