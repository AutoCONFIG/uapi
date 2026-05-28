package provider

import (
	"encoding/json"
	"fmt"

	newconvert "github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func toConvertFormat(format Format) (newconvert.Format, error) {
	switch format {
	case FormatOpenAIChatCompletions:
		return newconvert.FormatOpenAIChatCompletions, nil
	case FormatOpenAIResponses:
		return newconvert.FormatOpenAIResponses, nil
	case FormatAnthropic:
		return newconvert.FormatAnthropic, nil
	case FormatGemini:
		return newconvert.FormatGemini, nil
	case FormatGeminiCode, FormatGeminiCLI, FormatAntigravity:
		return newconvert.FormatGeminiCLI, nil
	default:
		return "", fmt.Errorf("unsupported format %q", format)
	}
}

func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, err
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, err
	}
	return newconvert.ConvertRequest(client, upstream, body)
}

func ConvertRequestWithAdaptor(clientFormat, upstreamFormat Format, body []byte, adaptor Adaptor) ([]byte, error) {
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, err
	}
	internal, err := newconvert.ToInternalOnly(client, body)
	if err != nil {
		return nil, fmt.Errorf("ToInternal(%s): %w", clientFormat, err)
	}
	if adaptor != nil {
		return adaptor.FromInternal(ToProviderInternal(internal))
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, err
	}
	return newconvert.ConvertRequest(client, upstream, body)
}

func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	if upstreamFormat == clientFormat {
		if upstreamFormat == FormatOpenAIChatCompletions {
			return preserveOpenAIChatReasoningAlias(body), nil
		}
		return body, nil
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, err
	}
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, err
	}
	return newconvert.ConvertResponse(upstream, client, body)
}

func preserveOpenAIChatReasoningAlias(body []byte) []byte {
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	choices, _ := root["choices"].([]interface{})
	changed := false
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]interface{})
		message, _ := choice["message"].(map[string]interface{})
		if message == nil {
			continue
		}
		reasoning, ok := message["reasoning_content"]
		if !ok {
			continue
		}
		if _, exists := message["reasoning"]; !exists {
			message["reasoning"] = reasoning
			changed = true
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

func ToProviderInternal(ir *newconvert.InternalRequest) *InternalRequest {
	if ir == nil {
		return nil
	}
	pr := &InternalRequest{
		Model:             ir.Model,
		Stream:            ir.Stream,
		MaxTokens:         ir.MaxTokens,
		Temperature:       ir.Temperature,
		TopP:              ir.TopP,
		TopK:              ir.TopK,
		StopWords:         ir.StopWords,
		Instructions:      ir.Instructions,
		Reasoning:         rawToInterface(ir.Reasoning),
		Thinking:          rawToInterface(ir.Thinking),
		ToolChoice:        rawToToolChoice(ir.ToolChoice),
		ResponseFormat:    rawToInterface(ir.ResponseFormat),
		LogitBias:         rawToInterface(ir.LogitBias),
		ParallelToolCalls: ir.ParallelToolCalls,
		FrequencyPenalty:  ir.FrequencyPenalty,
		PresencePenalty:   ir.PresencePenalty,
		N:                 ir.N,
		Seed:              ir.Seed,
		TopLogProbs:       ir.TopLogProbs,
		ServiceTier:       ir.ServiceTier,
		Store:             ir.Store,
		SafetySettings:    rawToInterface(ir.SafetySettings),
		CandidateCount:    ir.CandidateCount,
		ExtraParams:       rawMapToInterface(ir.Extra),
	}
	if ir.LogProbs != nil {
		pr.LogProbs = *ir.LogProbs
	}
	pr.Messages = toProviderMessages(ir.Messages)
	pr.Tools = toProviderTools(ir.Tools)
	return pr
}

func FromProviderInternal(pr *InternalRequest) *newconvert.InternalRequest {
	if pr == nil {
		return nil
	}
	logProbs := pr.LogProbs
	return &newconvert.InternalRequest{
		Model:             pr.Model,
		Stream:            pr.Stream,
		Messages:          fromProviderMessages(pr.Messages),
		Tools:             fromProviderTools(pr.Tools),
		MaxTokens:         pr.MaxTokens,
		Temperature:       pr.Temperature,
		TopP:              pr.TopP,
		TopK:              pr.TopK,
		StopWords:         pr.StopWords,
		Instructions:      pr.Instructions,
		Reasoning:         toRawMessage(pr.Reasoning),
		ToolChoice:        toRawMessage(pr.ToolChoice),
		ResponseFormat:    toRawMessage(pr.ResponseFormat),
		ParallelToolCalls: pr.ParallelToolCalls,
		FrequencyPenalty:  pr.FrequencyPenalty,
		PresencePenalty:   pr.PresencePenalty,
		N:                 pr.N,
		Seed:              pr.Seed,
		LogProbs:          &logProbs,
		TopLogProbs:       pr.TopLogProbs,
		LogitBias:         toRawMessage(pr.LogitBias),
		ServiceTier:       pr.ServiceTier,
		Store:             pr.Store,
		Thinking:          toRawMessage(pr.Thinking),
		SafetySettings:    toRawMessage(pr.SafetySettings),
		CandidateCount:    pr.CandidateCount,
		Extra:             toRawMessageMap(pr.ExtraParams),
	}
}

func toProviderMessages(messages []newconvert.InternalMessage) []InternalMessage {
	out := make([]InternalMessage, 0, len(messages))
	for _, m := range messages {
		pm := InternalMessage{
			Role:             m.Role,
			Content:          toProviderContentParts(m.Content),
			ToolCalls:        toProviderToolCalls(m.ToolCalls),
			ToolResult:       toProviderToolResult(m.ToolResult),
			ReasoningContent: toProviderContentParts(m.ReasoningContent),
			Name:             m.Name,
		}
		out = append(out, pm)
	}
	return out
}

func fromProviderMessages(messages []InternalMessage) []newconvert.InternalMessage {
	out := make([]newconvert.InternalMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, newconvert.InternalMessage{
			Role:             m.Role,
			Content:          fromProviderContentParts(m.Content),
			ToolCalls:        fromProviderToolCalls(m.ToolCalls),
			ToolResult:       fromProviderToolResult(m.ToolResult),
			ReasoningContent: fromProviderContentParts(m.ReasoningContent),
			Name:             m.Name,
		})
	}
	return out
}

func toProviderContentParts(parts []schema.ContentPart) []InternalContentPart {
	out := make([]InternalContentPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, InternalContentPart{
			Type:     p.Type,
			Text:     p.Text,
			ImageURL: p.ImageURL,
			Refusal:  p.Refusal,
			Extra:    rawMapToInterface(p.Extra),
		})
	}
	return out
}

func fromProviderContentParts(parts []InternalContentPart) []schema.ContentPart {
	out := make([]schema.ContentPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, schema.ContentPart{
			Type:     p.Type,
			Text:     p.Text,
			ImageURL: p.ImageURL,
			Refusal:  p.Refusal,
			Extra:    toRawMessageMap(p.Extra),
		})
	}
	return out
}

func toProviderToolCalls(calls []schema.ToolCall) []InternalToolCall {
	out := make([]InternalToolCall, 0, len(calls))
	for _, tc := range calls {
		name := tc.Name
		if name == "" {
			name = tc.Function.Name
		}
		out = append(out, InternalToolCall{ID: tc.ID, Name: name, Arguments: tc.Function.Arguments})
	}
	return out
}

func fromProviderToolCalls(calls []InternalToolCall) []schema.ToolCall {
	out := make([]schema.ToolCall, 0, len(calls))
	for _, tc := range calls {
		out = append(out, schema.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Name: tc.Name,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return out
}

func toProviderToolResult(result *schema.ToolResult) *InternalToolResult {
	if result == nil {
		return nil
	}
	return &InternalToolResult{ToolCallID: result.ToolCallID, Content: result.Content, IsError: result.IsError}
}

func fromProviderToolResult(result *InternalToolResult) *schema.ToolResult {
	if result == nil {
		return nil
	}
	return &schema.ToolResult{ToolCallID: result.ToolCallID, Content: result.Content, IsError: result.IsError}
}

func toProviderTools(tools []schema.Tool) []InternalTool {
	out := make([]InternalTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, InternalTool{Type: t.Type, Name: t.Name, Description: t.Description, Parameters: rawToInterface(t.Parameters)})
	}
	return out
}

func fromProviderTools(tools []InternalTool) []schema.Tool {
	out := make([]schema.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, schema.Tool{Type: t.Type, Name: t.Name, Description: t.Description, Parameters: toRawMessage(t.Parameters)})
	}
	return out
}

func rawToToolChoice(raw json.RawMessage) *InternalToolChoice {
	if len(raw) == 0 {
		return nil
	}
	var choice InternalToolChoice
	if err := json.Unmarshal(raw, &choice); err == nil && (choice.Type != "" || choice.Function != "") {
		return &choice
	}
	return nil
}

func rawToInterface(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func rawMapToInterface(raw map[string]json.RawMessage) map[string]interface{} {
	if raw == nil {
		return nil
	}
	out := make(map[string]interface{}, len(raw))
	for k, v := range raw {
		out[k] = rawToInterface(v)
	}
	return out
}

func toRawMessage(v interface{}) json.RawMessage {
	if v == nil {
		return nil
	}
	data, _ := json.Marshal(v)
	return data
}

func toRawMessageMap(m map[string]interface{}) map[string]json.RawMessage {
	if m == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		out[k] = toRawMessage(v)
	}
	return out
}
