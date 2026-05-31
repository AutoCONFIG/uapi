package convert

import (
	"encoding/json"
	"fmt"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseGeminiResponseDirectIR(body []byte) (*relayir.Response, error) {
	var wrapped struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(body, &wrapped) == nil && len(wrapped.Response) > 0 {
		body = wrapped.Response
	}
	var resp schema.GeminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini response: %w", err)
	}
	out := &relayir.Response{
		SourceProtocol: relayir.ProtocolGemini,
		Model:          resp.ModelVersion,
		Usage:          geminiResponseUsageToIR(resp.UsageMetadata),
		Native:         relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, RawBody: relayir.CloneRaw(body)},
	}
	for _, cand := range resp.Candidates {
		choice := relayir.Choice{
			Index: cand.Index,
			Finish: &relayir.Finish{
				Reason:       finishReasonToIR(mapGeminiFinishReason(cand.FinishReason)),
				NativeReason: cand.FinishReason,
			},
			Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "candidate", Raw: rawJSON(cand)},
		}
		if cand.Content != nil {
			choice.Role = relayir.Role(geminiRoleToRequestRole(cand.Content.Role))
			for idx, part := range cand.Content.Parts {
				choice.Items = append(choice.Items, geminiPartToIRItem(part, idx, nil, "$.candidates[].content.parts[]"))
			}
		}
		out.Choices = append(out.Choices, choice)
	}
	return out, nil
}

func geminiResponseUsageToIR(usage *schema.GeminiUsageMetadata) *relayir.Usage {
	if usage == nil {
		return nil
	}
	total := usage.TotalTokenCount
	if total == 0 {
		total = usage.PromptTokenCount + usage.CandidatesTokenCount
	}
	out := &relayir.Usage{
		InputTokens:      usage.PromptTokenCount,
		OutputTokens:     usage.CandidatesTokenCount,
		TotalTokens:      total,
		PromptTokens:     usage.PromptTokenCount,
		CompletionTokens: usage.CandidatesTokenCount,
		CacheReadTokens:  usage.CachedContentTokenCount,
	}
	if usage.ThoughtsTokenCount > 0 {
		out.OutputTokenDetails = map[string]json.RawMessage{"reasoning_tokens": rawJSON(usage.ThoughtsTokenCount)}
	}
	return out
}

func emitGeminiResponseDirectIR(resp *relayir.Response) ([]byte, error) {
	out := map[string]interface{}{}
	var candidates []map[string]interface{}
	for _, choice := range resp.Choices {
		cand := map[string]interface{}{
			"index":        choice.Index,
			"finishReason": geminiFinishFromIR(choice.Finish),
		}
		if len(choice.Items) > 0 {
			parts := make([]map[string]interface{}, 0, len(choice.Items))
			for _, item := range choice.Items {
				if part := geminiPartFromIRItem(item, nil); part != nil {
					parts = append(parts, part)
				}
			}
			if len(parts) > 0 {
				cand["content"] = map[string]interface{}{"role": internalRoleToGemini(string(choice.Role)), "parts": parts}
			}
		}
		candidates = append(candidates, cand)
	}
	out["candidates"] = candidates
	if resp.Usage != nil {
		out["usageMetadata"] = geminiUsageFromIR(resp.Usage)
	}
	if resp.Model != "" {
		out["modelVersion"] = resp.Model
	}
	for k, v := range resp.Native.Fields {
		out[k] = relayir.CloneRaw(v)
	}
	return json.Marshal(out)
}

func geminiUsageFromIR(usage *relayir.Usage) map[string]interface{} {
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	out := map[string]interface{}{"promptTokenCount": usage.InputTokens, "candidatesTokenCount": usage.OutputTokens, "totalTokenCount": total}
	if usage.CacheReadTokens > 0 {
		out["cachedContentTokenCount"] = usage.CacheReadTokens
	}
	if reasoningTokens := rawInt(usage.OutputTokenDetails["reasoning_tokens"]); reasoningTokens > 0 {
		out["thoughtsTokenCount"] = reasoningTokens
	}
	return out
}

func rawInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	_ = json.Unmarshal(raw, &n)
	return n
}

func geminiFinishFromIR(finish *relayir.Finish) string {
	if finish == nil {
		return "STOP"
	}
	if finish.NativeReason != "" {
		return finish.NativeReason
	}
	return mapGeminiResponseFinishReason(internalFinishReasonFromIR(finish.Reason))
}

func mapGeminiFinishReason(fr string) string {
	switch fr {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY":
		return "safety"
	case "RECITATION":
		return "recitation"
	case "OTHER":
		return "other"
	default:
		return fr
	}
}

func mapGeminiResponseFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "STOP"
	case "max_tokens":
		return "MAX_TOKENS"
	case "safety":
		return "SAFETY"
	case "recitation":
		return "RECITATION"
	case "other":
		return "OTHER"
	default:
		return fr
	}
}

func parseGeminiCLIResponseDirectIR(body []byte) (*relayir.Response, error) {
	var env schema.GeminiCLIResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini CLI response: %w", err)
	}
	innerBody, err := json.Marshal(env.Response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inner Gemini response: %w", err)
	}
	out, err := parseGeminiResponseDirectIR(innerBody)
	if err != nil {
		return nil, fmt.Errorf("failed to convert inner Gemini response: %w", err)
	}
	out.SourceProtocol = relayir.ProtocolGeminiCLI
	out.Native.Protocol = relayir.ProtocolGeminiCLI
	out.Native.RawBody = relayir.CloneRaw(body)
	return out, nil
}

func emitGeminiCLIResponseDirectIR(resp *relayir.Response) ([]byte, error) {
	innerBody, err := emitGeminiResponseDirectIR(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to Gemini format: %w", err)
	}
	var geminiResp schema.GeminiResponse
	if err := json.Unmarshal(innerBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini response: %w", err)
	}
	return json.Marshal(schema.GeminiCLIResponse{Response: geminiResp})
}

func init() {
	registerResponseIRParser(FormatGemini, parseGeminiResponseIR)
	registerResponseIREmitter(FormatGemini, emitGeminiResponseIR)
	registerResponseIRParser(FormatGeminiCLI, parseGeminiCLIResponseIR)
	registerResponseIREmitter(FormatGeminiCLI, emitGeminiCLIResponseIR)
}
