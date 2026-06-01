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
		Metadata:       relayir.CloneRawMap(resp.Extra),
		Native:         relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, RawBody: relayir.CloneRaw(body), Fields: relayir.CloneRawMap(resp.Extra), Unknown: relayir.CloneRawMap(resp.Extra)},
	}
	if resp.PromptFeedback != nil && (resp.PromptFeedback.BlockReason != "" || len(resp.PromptFeedback.SafetyRatings) > 0) {
		rawFeedback := rawJSON(resp.PromptFeedback)
		out.Choices = append(out.Choices, relayir.Choice{
			Index: 0,
			Finish: &relayir.Finish{
				Reason:       relayir.FinishSafety,
				NativeReason: resp.PromptFeedback.BlockReason,
				Native:       relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "promptFeedback", Raw: rawFeedback},
			},
			Items:  []relayir.Item{geminiSafetyBlockItem(resp.PromptFeedback.BlockReason, resp.PromptFeedback.BlockReasonMessage, rawFeedback, 0)},
			Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "promptFeedback", Raw: rawFeedback, Fields: relayir.CloneRawMap(resp.PromptFeedback.Extra)},
			Losses: []relayir.Loss{irloss(FormatGemini, "", "$.promptFeedback", "promptFeedback", rawFeedback, "Gemini prompt feedback safety block is preserved as IR safety_block and native metadata")},
		})
	}
	for _, cand := range resp.Candidates {
		choice := relayir.Choice{
			Index: cand.Index,
			Finish: &relayir.Finish{
				Reason:       finishReasonToIR(mapGeminiFinishReason(cand.FinishReason)),
				NativeReason: cand.FinishReason,
				Native:       geminiFinishNative(cand),
			},
			Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "candidate", Raw: rawJSON(cand), Fields: relayir.CloneRawMap(cand.Extra), Unknown: relayir.CloneRawMap(cand.Extra)},
		}
		if cand.Content != nil {
			choice.Role = relayir.Role(geminiRoleToRequestRole(cand.Content.Role))
			for idx, part := range cand.Content.Parts {
				choice.Items = append(choice.Items, geminiResponsePartToIRItem(part, idx))
			}
		}
		if cand.FinishReason == "SAFETY" || len(cand.SafetyRatings) > 0 {
			choice.Items = append(choice.Items, geminiSafetyBlockItem(cand.FinishReason, cand.FinishMessage, rawJSON(cand), len(choice.Items)))
			choice.Losses = append(choice.Losses, irloss(FormatGemini, "", "$.candidates[].safetyRatings", "safetyRatings", cand.SafetyRatings, "Gemini safety ratings are preserved as IR safety_block native metadata"))
		}
		out.Choices = append(out.Choices, choice)
	}
	return out, nil
}

func geminiResponsePartToIRItem(part schema.GeminiPart, idx int) relayir.Item {
	item := geminiPartToIRItem(part, idx, nil, "$.candidates[].content.parts[]")
	if part.FunctionResponse != nil {
		item.Losses = append(item.Losses, geminiFunctionResponseLosses(part.FunctionResponse)...)
	}
	return item
}

func geminiSafetyBlockItem(reason, message string, raw json.RawMessage, index int) relayir.Item {
	return relayir.Item{
		Kind:          relayir.ItemSafetyBlock,
		OriginalIndex: index,
		SafetyBlock:   &relayir.SafetyBlock{Reason: reason, Message: message, Raw: relayir.CloneRaw(raw)},
		Native:        relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "safety_block", Raw: relayir.CloneRaw(raw), Index: index},
		Losses:        []relayir.Loss{irloss(FormatGemini, "", "$.safety", "safety", raw, "Gemini safety metadata has no direct target protocol field and is preserved as IR safety_block")},
	}
}

func geminiFinishNative(cand schema.GeminiCandidate) relayir.NativeEnvelope {
	meta := map[string]json.RawMessage{}
	if cand.FinishMessage != "" {
		meta["finishMessage"] = rawJSON(cand.FinishMessage)
	}
	if len(cand.SafetyRatings) > 0 {
		meta["safetyRatings"] = relayir.CloneRaw(cand.SafetyRatings)
	}
	if len(meta) == 0 {
		return relayir.NativeEnvelope{}
	}
	return relayir.NativeEnvelope{Protocol: relayir.ProtocolGemini, Kind: "finish", Meta: meta}
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
				part, err := geminiPartFromIRItem(item, nil)
				if err != nil {
					return nil, err
				}
				if part != nil {
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
