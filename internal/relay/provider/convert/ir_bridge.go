package convert

import (
	"encoding/json"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func (r *adapterRequest) ToIR() *ir.Request {
	if r == nil {
		return nil
	}
	if r.IR != nil {
		return r.IR
	}
	r.IR = r.buildIR()
	return r.IR
}

func (r *adapterRequest) buildIR() *ir.Request {
	req := &ir.Request{
		SourceProtocol: irProtocol(r.SourceFormat),
		Model:          r.Model,
		Stream:         r.Stream,
		Native:         ir.NativeEnvelope{Protocol: irProtocol(r.SourceFormat), RawBody: ir.CloneRaw(r.RawRequestBody), Fields: ir.CloneRawMap(r.Extra), Unknown: ir.CloneRawMap(r.Extra)},
		Metadata:       ir.CloneRawMap(r.Extra),
		Losses:         append([]ir.Loss(nil), r.Losses...),
		Generation: ir.GenerationConfig{
			MaxTokens:         r.MaxTokens,
			MaxTokensField:    r.MaxTokensField,
			Temperature:       r.Temperature,
			TopP:              r.TopP,
			TopK:              r.TopK,
			Stop:              append([]string(nil), r.StopWords...),
			N:                 r.N,
			CandidateCount:    r.CandidateCount,
			Seed:              r.Seed,
			LogProbs:          r.LogProbs,
			TopLogProbs:       r.TopLogProbs,
			FrequencyPenalty:  r.FrequencyPenalty,
			PresencePenalty:   r.PresencePenalty,
			ResponseFormat:    ir.CloneRaw(r.ResponseFormat),
			Reasoning:         ir.CloneRaw(r.Reasoning),
			Thinking:          ir.CloneRaw(r.Thinking),
			ServiceTier:       r.ServiceTier,
			Store:             r.Store,
			ParallelToolCalls: r.ParallelToolCalls,
			Extra:             ir.CloneRawMap(r.GeminiGenerationConfigExtra),
		},
		Safety: ir.SafetyConfig{Settings: ir.CloneRaw(r.SafetySettings)},
	}
	if r.Instructions != nil || len(r.InstructionsRaw) > 0 {
		inst := ir.Instruction{
			Role:   ir.RoleSystem,
			Native: ir.NativeEnvelope{Protocol: irProtocol(r.SourceFormat), Raw: ir.CloneRaw(r.InstructionsRaw)},
		}
		if r.Instructions != nil {
			inst.Text = *r.Instructions
			inst.Items = []ir.Item{{Kind: ir.ItemText, Text: &ir.Text{Text: *r.Instructions}}}
		}
		req.Instructions = append(req.Instructions, inst)
	}
	for _, tool := range r.Tools {
		req.Tools = append(req.Tools, irTool(tool, r.SourceFormat))
	}
	if len(r.ToolChoice) > 0 {
		req.ToolChoice = &ir.ToolChoice{Raw: ir.CloneRaw(r.ToolChoice)}
	}
	for _, msg := range r.Messages {
		req.Turns = append(req.Turns, irTurn(msg, r.SourceFormat))
	}
	return req
}

func irTurn(msg adapterTurn, source Format) ir.Turn {
	turn := ir.Turn{
		Role:     irRole(msg.Role),
		Name:     msg.Name,
		ID:       msg.ItemID,
		Status:   msg.Status,
		Phase:    msg.Phase,
		Metadata: ir.CloneRawMap(msg.Extra),
		Native:   ir.NativeEnvelope{Protocol: irProtocol(source), Raw: ir.CloneRaw(msg.RawItem), Fields: ir.CloneRawMap(msg.Extra)},
	}
	for idx, item := range canonicalMessageParts(msg) {
		turn.Items = append(turn.Items, irItem(item, source, idx))
	}
	if len(turn.Items) == 0 && len(msg.RawItem) > 0 {
		turn.Items = append(turn.Items, ir.Item{
			Kind:   ir.ItemOpaque,
			Opaque: &ir.Opaque{Raw: ir.CloneRaw(msg.RawItem)},
			Native: ir.NativeEnvelope{Protocol: irProtocol(source), Raw: ir.CloneRaw(msg.RawItem)},
		})
	}
	return turn
}

func irItem(item adapterItem, source Format, index int) ir.Item {
	switch item.Kind {
	case contentItemKindContent, contentItemKindReasoning:
		return irContentPartItem(item.Kind, item.Content, item.Raw, source, index)
	case contentItemKindToolCall:
		return irToolUseItem(item.ToolCall, item.Raw, source, index)
	case contentItemKindToolResult:
		return irToolResultItem(item.ToolResult, item.Raw, source, index)
	default:
		return ir.Item{
			Kind:          ir.ItemOpaque,
			OriginalIndex: index,
			Opaque:        &ir.Opaque{Type: item.Kind, Raw: ir.CloneRaw(item.Raw)},
			Native:        ir.NativeEnvelope{Protocol: irProtocol(source), Kind: item.Kind, Raw: ir.CloneRaw(item.Raw), Index: index},
		}
	}
}

func irContentPartItem(kind string, part schema.ContentPart, raw json.RawMessage, source Format, index int) ir.Item {
	out := ir.Item{
		ID:            rawString(part.Extra["id"]),
		OriginalIndex: index,
		Metadata:      ir.CloneRawMap(part.Extra),
		Native:        ir.NativeEnvelope{Protocol: irProtocol(source), Kind: part.Type, Raw: ir.CloneRaw(raw), Fields: ir.CloneRawMap(part.Extra), Unknown: ir.CloneRawMap(part.Extra), Index: index},
	}
	if kind == contentItemKindReasoning {
		out.Kind = ir.ItemReasoning
		out.Reasoning = &ir.Reasoning{
			Text:             part.Text,
			EncryptedContent: rawString(part.Extra[reasoningExtraEncryptedContent]),
			RedactedContent:  rawString(part.Extra[reasoningExtraData]),
			Signature:        rawString(part.Extra[reasoningExtraSignature]),
			ThoughtSignature: rawString(part.Extra[reasoningExtraThoughtSignature]),
		}
		if details := part.Extra["reasoning_details"]; len(details) > 0 {
			out.Reasoning.Details = ir.CloneRaw(details)
		}
		return out
	}
	switch part.Type {
	case "text", "input_text", "output_text":
		out.Kind = ir.ItemText
		out.Text = &ir.Text{Text: part.Text}
	case "image_url", "input_image":
		out.Kind = ir.ItemImage
		image := &ir.Image{MimeType: part.MimeType, Detail: part.ImageDetail}
		if part.ImageURL != nil {
			if strings.HasPrefix(*part.ImageURL, "data:") {
				image.DataURI = *part.ImageURL
			} else {
				image.URL = *part.ImageURL
			}
		}
		out.Image = image
	case "file", "input_file":
		out.Kind = ir.ItemFile
		out.File = &ir.File{
			URL:      part.FileURL,
			FileID:   part.FileID,
			DataURI:  part.FileData,
			Name:     part.Filename,
			MimeType: firstNonEmptyString(part.FileType, part.MimeType),
		}
	case "input_audio", "audio":
		out.Kind = ir.ItemAudio
		out.Audio = &ir.Audio{DataURI: part.Data, MimeType: part.MimeType}
	case "refusal":
		out.Kind = ir.ItemRefusal
		out.Refusal = &ir.Refusal{Text: part.Refusal}
	case "executable_code":
		out.Kind = ir.ItemExecutableCode
		out.ExecutableCode = &ir.ExecutableCode{Language: rawString(part.Extra["language"]), Code: part.Text}
	case "code_execution_result":
		out.Kind = ir.ItemCodeExecutionResult
		out.CodeExecutionResult = &ir.CodeExecutionResult{Outcome: rawString(part.Extra["outcome"]), Output: part.Text}
	default:
		out.Kind = ir.ItemOpaque
		out.Opaque = &ir.Opaque{Type: part.Type, Raw: ir.CloneRaw(raw), Text: part.Text}
	}
	return out
}

func irToolUseItem(call schema.ToolCall, raw json.RawMessage, source Format, index int) ir.Item {
	name := call.Name
	if name == "" {
		name = call.Function.Name
	}
	return ir.Item{
		ID:            call.ID,
		CallID:        call.ID,
		Name:          name,
		OriginalIndex: index,
		Kind:          ir.ItemToolUse,
		ToolUse: &ir.ToolUse{
			ID:            call.ID,
			CallID:        call.ID,
			Name:          name,
			Arguments:     rawArgument(call.Function.Arguments),
			ArgumentsText: call.Function.Arguments,
		},
		Native: ir.NativeEnvelope{Protocol: irProtocol(source), Kind: "tool_call", Raw: ir.CloneRaw(raw), Index: index},
	}
}

func irToolResultItem(result schema.ToolResult, raw json.RawMessage, source Format, index int) ir.Item {
	return ir.Item{
		CallID:        result.ToolCallID,
		OriginalIndex: index,
		Kind:          ir.ItemToolResult,
		ToolResult: &ir.ToolResult{
			ToolUseID:  result.ToolCallID,
			CallID:     result.ToolCallID,
			OutputText: result.Content,
			IsError:    result.IsError,
		},
		Native: ir.NativeEnvelope{Protocol: irProtocol(source), Kind: "tool_result", Raw: ir.CloneRaw(raw), Index: index},
	}
}

func irTool(tool schema.Tool, source Format) ir.Tool {
	name := tool.Name
	description := tool.Description
	parameters := ir.CloneRaw(tool.Parameters)
	if tool.Function != nil {
		name = firstNonEmptyString(name, tool.Function.Name)
		description = firstNonEmptyString(description, tool.Function.Description)
		if len(parameters) == 0 {
			parameters = ir.CloneRaw(tool.Function.Parameters)
		}
	}
	kind := ir.ToolKind(tool.Type)
	if kind == "" {
		kind = ir.ToolOpaque
	}
	return ir.Tool{
		Kind:        kind,
		Name:        name,
		Description: description,
		InputSchema: ir.CloneRaw(tool.InputSchema),
		Parameters:  parameters,
		Metadata:    ir.CloneRawMap(tool.Extra),
		FunctionMetadata: func() map[string]json.RawMessage {
			if tool.Function == nil {
				return nil
			}
			return ir.CloneRawMap(tool.Function.Extra)
		}(),
		Native: ir.NativeEnvelope{Protocol: irProtocol(source), Fields: ir.CloneRawMap(tool.Extra)},
	}
}

func irProtocol(f Format) ir.Protocol {
	switch f {
	case FormatOpenAIChatCompletions:
		return ir.ProtocolOpenAIChat
	case FormatOpenAIResponses:
		return ir.ProtocolOpenAIResponses
	case FormatCodexResponses:
		return ir.ProtocolCodex
	case FormatAnthropic:
		return ir.ProtocolAnthropic
	case FormatClaudeCode:
		return ir.ProtocolClaudeCode
	case FormatGemini:
		return ir.ProtocolGemini
	case FormatGeminiCode:
		return ir.ProtocolGeminiCode
	case FormatGeminiCLI:
		return ir.ProtocolGeminiCLI
	case FormatAntigravity:
		return ir.ProtocolAntigravity
	default:
		return ir.Protocol(f)
	}
}

func irRole(role string) ir.Role {
	switch role {
	case "system":
		return ir.RoleSystem
	case "developer":
		return ir.RoleDeveloper
	case "user":
		return ir.RoleUser
	case "assistant":
		return ir.RoleAssistant
	case "model":
		return ir.RoleModel
	case "tool":
		return ir.RoleTool
	case "function":
		return ir.RoleFunction
	case "":
		return ir.RoleUnknown
	default:
		return ir.Role(role)
	}
}

func rawArgument(arguments string) json.RawMessage {
	if strings.TrimSpace(arguments) == "" {
		return nil
	}
	if json.Valid([]byte(arguments)) {
		return json.RawMessage(arguments)
	}
	raw, _ := json.Marshal(arguments)
	return raw
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
