package convert

import (
	"encoding/json"
	"fmt"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseAnthropicResponseDirectIR(body []byte) (*relayir.Response, error) {
	var resp schema.AnthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Anthropic response: %w", err)
	}
	var rawRoot map[string]json.RawMessage
	_ = json.Unmarshal(body, &rawRoot)
	meta := map[string]json.RawMessage{}
	copyRawFields(meta, rawRoot, "type", "stop_sequence")
	out := &relayir.Response{
		SourceProtocol: relayir.ProtocolAnthropic,
		ID:             resp.ID,
		Model:          resp.Model,
		Metadata:       relayir.CloneRawMap(meta),
		Usage:          anthropicUsageToIR(resp.Usage),
		Native:         relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, RawBody: relayir.CloneRaw(body), Fields: relayir.CloneRawMap(meta), Unknown: relayir.CloneRawMap(meta)},
	}
	choice := relayir.Choice{
		Index: 0,
		Role:  relayir.Role(anthropicRole(resp.Role)),
		Finish: &relayir.Finish{
			Reason:       finishReasonToIR(mapAnthropicFinishReason(resp.StopReason)),
			NativeReason: resp.StopReason,
			StopSequence: resp.StopSequence,
		},
		Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, Kind: "message", Raw: relayir.CloneRaw(body)},
	}
	for idx, block := range resp.Content {
		choice.Items = append(choice.Items, anthropicBlockToIRItem(block, idx))
	}
	out.Choices = append(out.Choices, choice)
	return out, nil
}

func anthropicUsageToIR(usage schema.AnthropicUsage) *relayir.Usage {
	cacheCreation := anthropicCacheCreationInputTokens(usage)
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && cacheCreation == 0 && usage.CacheReadInputTokens == 0 {
		return nil
	}
	return &relayir.Usage{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		TotalTokens:         usage.InputTokens + usage.OutputTokens,
		PromptTokens:        usage.InputTokens,
		CompletionTokens:    usage.OutputTokens,
		CacheCreationTokens: cacheCreation,
		CacheWriteTokens:    cacheCreation,
		CacheReadTokens:     usage.CacheReadInputTokens,
		Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolAnthropic, Fields: func() map[string]json.RawMessage {
			raw := rawJSON(usage)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			return fields
		}()},
	}
}

func emitAnthropicResponseDirectIR(resp *relayir.Response) ([]byte, error) {
	out := make(map[string]interface{})
	out["id"] = resp.ID
	out["type"] = "message"
	out["role"] = "assistant"
	out["model"] = resp.Model
	preserveNativeFields := resp.SourceProtocol == relayir.ProtocolAnthropic || resp.SourceProtocol == relayir.ProtocolClaudeCode
	if preserveNativeFields {
		for k, v := range resp.Metadata {
			out[k] = v
		}
	}
	var content []map[string]interface{}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		for _, item := range choice.Items {
			if (resp.SourceProtocol == relayir.ProtocolAnthropic || resp.SourceProtocol == relayir.ProtocolClaudeCode) && len(item.Native.Raw) > 0 {
				var raw map[string]interface{}
				if json.Unmarshal(item.Native.Raw, &raw) == nil {
					content = append(content, raw)
					continue
				}
			}
			block, err := anthropicBlockFromIRItem(item, resp.Model)
			if err != nil {
				return nil, err
			}
			if block != nil {
				content = append(content, block)
			}
		}
		out["stop_reason"] = anthropicFinishFromIR(resp.SourceProtocol, choice.Finish)
		if choice.Finish != nil && choice.Finish.StopSequence != "" {
			out["stop_sequence"] = choice.Finish.StopSequence
		}
	}
	out["content"] = content
	out["usage"] = anthropicUsageFromIR(resp.Usage)
	if preserveNativeFields {
		for k, v := range resp.Native.Fields {
			out[k] = relayir.CloneRaw(v)
		}
	}
	return json.Marshal(out)
}

func anthropicUsageFromIR(usage *relayir.Usage) map[string]interface{} {
	if usage == nil {
		return map[string]interface{}{"input_tokens": 0, "output_tokens": 0}
	}
	out := map[string]interface{}{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	}
	if usage.CacheCreationTokens > 0 {
		out["cache_creation_input_tokens"] = usage.CacheCreationTokens
	}
	if usage.CacheReadTokens > 0 {
		out["cache_read_input_tokens"] = usage.CacheReadTokens
	}
	for k, v := range usage.Native.Fields {
		if _, exists := out[k]; !exists {
			out[k] = relayir.CloneRaw(v)
		}
	}
	return out
}

func anthropicFinishFromIR(source relayir.Protocol, finish *relayir.Finish) string {
	if finish == nil {
		return "end_turn"
	}
	if finish.NativeReason != "" && (source == relayir.ProtocolAnthropic || source == relayir.ProtocolClaudeCode) {
		return finish.NativeReason
	}
	return mapAnthropicResponseFinishReason(internalFinishReasonFromIR(finish.Reason))
}

func mapAnthropicFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "end_turn"
	case "max_tokens":
		return "max_tokens"
	case "tool_use":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return fr
	}
}

func mapAnthropicResponseFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "end_turn"
	case "max_tokens":
		return "max_tokens"
	case "tool_use":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return fr
	}
}

func anthropicCacheCreationInputTokens(usage schema.AnthropicUsage) int {
	if usage.CacheCreationInputTokens > 0 {
		return usage.CacheCreationInputTokens
	}
	if usage.CacheCreation == nil {
		return 0
	}
	return usage.CacheCreation.Ephemeral5mInputTokens + usage.CacheCreation.Ephemeral1hInputTokens
}

func mergeAnthropicRawUsageExtras(usage map[string]interface{}, rawBody json.RawMessage) {
	if len(rawBody) == 0 {
		return
	}
	var raw struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return
	}
	for key, value := range raw.Usage {
		switch key {
		case "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens":
			continue
		default:
			usage[key] = value
		}
	}
}

func anthropicUnknownBlockExtra(block schema.AnthropicContentBlock) map[string]json.RawMessage {
	extra := copyRawMap(block.Extra)
	if block.ID != "" {
		extra = setRawString(extra, "id", block.ID)
	}
	if block.Name != "" {
		extra = setRawString(extra, "name", block.Name)
	}
	if len(block.Input) > 0 && string(block.Input) != "null" {
		if extra == nil {
			extra = make(map[string]json.RawMessage)
		}
		extra["input"] = append(json.RawMessage(nil), block.Input...)
	}
	if len(block.Content) > 0 && string(block.Content) != "null" {
		if extra == nil {
			extra = make(map[string]json.RawMessage)
		}
		extra["content"] = append(json.RawMessage(nil), block.Content...)
	}
	return extra
}

func init() {
	registerResponseIRParser(FormatAnthropic, parseAnthropicResponseIR)
	registerResponseIREmitter(FormatAnthropic, emitAnthropicResponseIR)
}
