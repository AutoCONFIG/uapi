package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseOpenAIChatResponseDirectIR(body []byte) (*relayir.Response, error) {
	var resp schema.OpenAIChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Chat response: %w", err)
	}
	out := &relayir.Response{
		SourceProtocol: relayir.ProtocolOpenAIChat,
		ID:             resp.ID,
		Model:          resp.Model,
		Metadata:       relayir.CloneRawMap(resp.Extra),
		Native: relayir.NativeEnvelope{
			Protocol: relayir.ProtocolOpenAIChat,
			RawBody:  relayir.CloneRaw(body),
			Fields:   relayir.CloneRawMap(resp.Extra),
			Unknown:  relayir.CloneRawMap(resp.Extra),
		},
	}
	if resp.Usage != nil {
		out.Usage = openAIChatUsageToIR(resp.Usage)
	}
	for _, choice := range resp.Choices {
		irChoice := relayir.Choice{
			Index: choice.Index,
			Role:  relayir.Role(openAIChatRole(choice.Message.Role)),
			Finish: &relayir.Finish{
				Reason:       finishReasonToIR(mapOpenAIChatFinishReason(choice.FinishReason)),
				NativeReason: choice.FinishReason,
			},
			Native: relayir.NativeEnvelope{
				Protocol: relayir.ProtocolOpenAIChat,
				Kind:     "choice",
				Raw:      rawJSON(choice),
				Fields:   relayir.CloneRawMap(choice.Extra),
			},
		}
		for _, part := range reasoningPartsFromOpenAIChatExtra(choice.Message.Extra) {
			irChoice.Items = append(irChoice.Items, irContentPartItem(contentItemKindReasoning, part, rawJSON(part), FormatOpenAIChatCompletions, len(irChoice.Items)))
		}
		for _, part := range chatContentParts(choice.Message.Content) {
			irChoice.Items = append(irChoice.Items, irContentPartItem(contentItemKindContent, part, rawJSON(part), FormatOpenAIChatCompletions, len(irChoice.Items)))
		}
		for _, tc := range choice.Message.ToolCalls {
			call := schema.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Name: tc.Function.Name,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			}
			irChoice.Items = append(irChoice.Items, irToolUseItem(call, rawJSON(tc), FormatOpenAIChatCompletions, len(irChoice.Items)))
		}
		if choice.Message.Refusal != "" {
			irChoice.Items = append(irChoice.Items, relayir.Item{
				Kind:          relayir.ItemRefusal,
				OriginalIndex: len(irChoice.Items),
				Refusal:       &relayir.Refusal{Text: choice.Message.Refusal},
				Native:        relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIChat, Kind: "refusal", Raw: rawJSON(choice.Message.Refusal)},
			})
		}
		out.Choices = append(out.Choices, irChoice)
	}
	return out, nil
}

func openAIChatUsageToIR(usage *schema.Usage) *relayir.Usage {
	if usage == nil {
		return nil
	}
	cachedTokens := usageDetailInt(usage.PromptTokensDetails, "cached_tokens")
	if cachedTokens == 0 {
		cachedTokens = usage.PromptCacheHitTokens
	}
	creationTokens := usageDetailInt(usage.PromptTokensDetails, "cache_creation_input_tokens")
	if creationTokens == 0 {
		creationTokens = usageDetailInt(usage.PromptTokensDetails, "cached_write_tokens")
	}
	return &relayir.Usage{
		InputTokens:         usage.PromptTokens,
		OutputTokens:        usage.CompletionTokens,
		TotalTokens:         usage.TotalTokens,
		PromptTokens:        usage.PromptTokens,
		CompletionTokens:    usage.CompletionTokens,
		CacheReadTokens:     cachedTokens,
		CacheCreationTokens: creationTokens,
		CacheWriteTokens:    creationTokens,
		InputTokenDetails:   rawDetails(usage.PromptTokensDetails),
		OutputTokenDetails:  rawDetails(usage.CompletionTokensDetails),
	}
}

func usageDetailInt(details map[string]interface{}, key string) int {
	if details == nil {
		return 0
	}
	switch v := details[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

// mapOpenAIChatFinishReason converts OpenAI finish_reason to internal format.
func mapOpenAIChatFinishReason(fr string) string {
	switch fr {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "content_filter"
	case "function_call":
		return "tool_use"
	default:
		return fr
	}
}

// mapOpenAIChatResponseFinishReason converts internal finish_reason to OpenAI format.
func mapOpenAIChatResponseFinishReason(fr string) string {
	switch fr {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "content_filter":
		return "content_filter"
	default:
		return fr
	}
}

func emitOpenAIChatResponseDirectIR(ir *relayir.Response) ([]byte, error) {
	resp := schema.OpenAIChatResponse{
		ID:      ir.ID,
		Object:  "chat.completion",
		Created: 0,
		Model:   ir.Model,
		Choices: make([]schema.ChatChoice, 0, len(ir.Choices)),
		Extra:   relayir.CloneRawMap(ir.Metadata),
	}
	if ir.Usage != nil {
		resp.Usage = openAIChatUsageFromIR(ir.Usage)
	}
	for _, choice := range ir.Choices {
		chatChoice := schema.ChatChoice{
			Index:        choice.Index,
			FinishReason: openAIChatFinishFromIR(choice.Finish),
			Message: schema.ChatMessage{
				Role:  openAIChatRole(string(choice.Role)),
				Extra: relayir.CloneRawMap(choice.Native.Fields),
			},
			Extra: relayir.CloneRawMap(choice.Native.Fields),
		}
		var content []schema.ContentPart
		var reasoning []schema.ContentPart
		for _, item := range choice.Items {
			switch item.Kind {
			case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
				reasoning = append(reasoning, schemaReasoningFromIR(item))
			case relayir.ItemToolUse, relayir.ItemFunctionCall:
				chatChoice.Message.ToolCalls = append(chatChoice.Message.ToolCalls, schemaToolCallFromIR(item))
			case relayir.ItemRefusal:
				if item.Refusal != nil {
					chatChoice.Message.Refusal = item.Refusal.Text
				}
			default:
				if part, ok := schemaContentFromIR(item); ok {
					content = append(content, part)
				}
			}
		}
		content = openAIChatContentParts(content)
		if len(content) > 0 {
			if len(content) == 1 && content[0].Type == "text" && content[0].Text != "" && len(content[0].Extra) == 0 {
				chatChoice.Message.Content = schema.NewTextContent(content[0].Text)
			} else {
				chatChoice.Message.Content = schema.NewPartsContent(content...)
			}
		}
		if len(reasoning) > 0 {
			if chatChoice.Message.Extra == nil {
				chatChoice.Message.Extra = make(map[string]json.RawMessage)
			}
			if text := contentPartsText(reasoning); text != "" {
				chatChoice.Message.Extra["reasoning_content"] = rawJSON(text)
			}
			if details := reasoningDetailsFromParts(reasoning); len(details) > 0 {
				chatChoice.Message.Extra["reasoning_details"] = rawJSON(details)
			}
		}
		resp.Choices = append(resp.Choices, chatChoice)
	}
	for k, v := range ir.Native.Fields {
		resp.Extra[k] = relayir.CloneRaw(v)
	}
	return json.Marshal(resp)
}

func openAIChatUsageFromIR(usage *relayir.Usage) *schema.Usage {
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && total == 0 &&
		usage.CacheReadTokens == 0 && usage.CacheCreationTokens == 0 && usage.CacheWriteTokens == 0 {
		return nil
	}
	out := &schema.Usage{
		PromptTokens:            usage.InputTokens,
		CompletionTokens:        usage.OutputTokens,
		TotalTokens:             total,
		PromptTokensDetails:     detailsFromRaw(usage.InputTokenDetails),
		CompletionTokensDetails: detailsFromRaw(usage.OutputTokenDetails),
	}
	if out.PromptTokensDetails == nil {
		out.PromptTokensDetails = map[string]interface{}{}
	}
	if usage.CacheReadTokens > 0 {
		out.PromptTokensDetails["cached_tokens"] = usage.CacheReadTokens
		out.PromptTokensDetails["cached_read_tokens"] = usage.CacheReadTokens
	}
	if usage.CacheCreationTokens > 0 {
		out.PromptTokensDetails["cache_creation_input_tokens"] = usage.CacheCreationTokens
		out.PromptTokensDetails["cached_write_tokens"] = usage.CacheCreationTokens
	} else if usage.CacheWriteTokens > 0 {
		out.PromptTokensDetails["cached_write_tokens"] = usage.CacheWriteTokens
	}
	if len(out.PromptTokensDetails) == 0 {
		out.PromptTokensDetails = nil
	}
	return out
}

func openAIChatFinishFromIR(finish *relayir.Finish) string {
	if finish == nil {
		return "stop"
	}
	if finish.NativeReason != "" {
		return finish.NativeReason
	}
	return mapOpenAIChatResponseFinishReason(internalFinishReasonFromIR(finish.Reason))
}

func contentPartsText(parts []schema.ContentPart) string {
	var out []string
	for _, part := range parts {
		if part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

func parseOpenAIResponsesResponseDirectIR(body []byte) (*relayir.Response, error) {
	var resp schema.OpenAIResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OpenAI Responses response: %w", err)
	}
	var rawRoot map[string]json.RawMessage
	_ = json.Unmarshal(body, &rawRoot)
	out := &relayir.Response{
		SourceProtocol: relayir.ProtocolOpenAIResponses,
		ID:             resp.ID,
		Model:          resp.Model,
		Native: relayir.NativeEnvelope{
			Protocol: relayir.ProtocolOpenAIResponses,
			RawBody:  relayir.CloneRaw(body),
		},
		Metadata: map[string]json.RawMessage{},
	}
	copyResponseRawFields(out.Metadata, rawRoot, "object", "created_at", "status", "metadata", "error", "incomplete_details", "parallel_tool_calls", "temperature", "top_p", "tool_choice")
	out.Native.Fields = relayir.CloneRawMap(out.Metadata)
	out.Native.Unknown = relayir.CloneRawMap(out.Metadata)
	if resp.Usage != nil {
		out.Usage = responsesResponseUsageToIR(resp.Usage)
	}
	var pendingReasoning []relayir.Item
	flushPendingReasoning := func() {
		if len(pendingReasoning) == 0 {
			return
		}
		out.Choices = append(out.Choices, relayir.Choice{
			Index: len(out.Choices),
			Role:  relayir.RoleAssistant,
			Items: append([]relayir.Item(nil), pendingReasoning...),
			Finish: &relayir.Finish{
				Reason:       relayir.FinishStop,
				NativeReason: "completed",
			},
		})
		pendingReasoning = nil
	}
	for _, item := range resp.Output {
		rawItem := rawJSON(item)
		switch item.Type {
		case "message":
			choice := relayir.Choice{
				Index: len(out.Choices),
				Role:  relayir.Role(responsesRole(item.Role)),
				Items: append([]relayir.Item(nil), pendingReasoning...),
				Native: relayir.NativeEnvelope{
					Protocol: relayir.ProtocolOpenAIResponses,
					Kind:     item.Type,
					Raw:      rawItem,
					Fields:   relayir.CloneRawMap(item.Extra),
				},
				Finish: &relayir.Finish{
					Reason:       finishReasonToIR(mapResponsesStatusToFinishReason(item.Status)),
					NativeReason: item.Status,
				},
			}
			for idx, part := range item.Content {
				choice.Items = append(choice.Items, irContentPartItem(contentItemKindContent, part, rawJSON(part), FormatOpenAIResponses, idx))
			}
			pendingReasoning = nil
			out.Choices = append(out.Choices, choice)
		case "function_call":
			name := qualifyResponsesNamespaceToolName(rawString(item.Extra["namespace"]), item.Name)
			choice := relayir.Choice{
				Index: len(out.Choices),
				Role:  relayir.Role(responsesRole(item.Role)),
				Items: append([]relayir.Item(nil), pendingReasoning...),
				Native: relayir.NativeEnvelope{
					Protocol: relayir.ProtocolOpenAIResponses,
					Kind:     item.Type,
					Raw:      rawItem,
					Fields:   relayir.CloneRawMap(item.Extra),
				},
				Finish: &relayir.Finish{Reason: relayir.FinishToolCall, NativeReason: "function_call"},
			}
			choice.Items = append(choice.Items, irToolUseItem(schema.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Name: name,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: name, Arguments: item.Arguments},
			}, rawItem, FormatOpenAIResponses, 0))
			pendingReasoning = nil
			out.Choices = append(out.Choices, choice)
		case "reasoning":
			for idx, part := range reasoningPartsFromResponsesExtra(item.Extra) {
				pendingReasoning = append(pendingReasoning, irContentPartItem(contentItemKindReasoning, part, rawItem, FormatOpenAIResponses, idx))
			}
		default:
			out.Losses = append(out.Losses, irloss(FormatOpenAIResponses, "", "$.output[]", item.Type, rawItem, "Responses output item is preserved as native raw/opaque IR"))
			flushPendingReasoning()
			out.Choices = append(out.Choices, relayir.Choice{
				Index: len(out.Choices),
				Role:  relayir.RoleAssistant,
				Items: []relayir.Item{{
					Kind:   relayir.ItemOpaque,
					Opaque: &relayir.Opaque{Type: item.Type, Raw: rawItem},
					Native: relayir.NativeEnvelope{Protocol: relayir.ProtocolOpenAIResponses, Kind: item.Type, Raw: rawItem},
				}},
				Finish: &relayir.Finish{Reason: relayir.FinishUnknown, NativeReason: item.Status},
			})
		}
	}
	flushPendingReasoning()
	return out, nil
}

func responsesResponseUsageToIR(usage *schema.ResponsesUsage) *relayir.Usage {
	if usage == nil {
		return nil
	}
	cachedTokens := usageDetailInt(usage.InputTokensDetails, "cached_tokens")
	if cachedTokens == 0 {
		cachedTokens = usage.CacheReadInputTokens
	}
	if cachedTokens == 0 {
		cachedTokens = usage.PromptCacheHitTokens
	}
	creationTokens := usage.CacheCreationInputTokens
	if creationTokens == 0 {
		creationTokens = usageDetailInt(usage.InputTokensDetails, "cache_creation_input_tokens")
	}
	if creationTokens == 0 {
		creationTokens = usageDetailInt(usage.InputTokensDetails, "cached_write_tokens")
	}
	return &relayir.Usage{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		TotalTokens:         usage.TotalTokens,
		PromptTokens:        usage.InputTokens,
		CompletionTokens:    usage.OutputTokens,
		CacheReadTokens:     cachedTokens,
		CacheCreationTokens: creationTokens,
		CacheWriteTokens:    creationTokens,
		InputTokenDetails:   rawDetails(usage.InputTokensDetails),
	}
}

func emitOpenAIResponsesResponseDirectIR(resp *relayir.Response) ([]byte, error) {
	if resp == nil {
		return json.Marshal(map[string]interface{}{})
	}
	output, err := responsesOutputItemsFromIR(resp)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"id":         resp.ID,
		"object":     "response",
		"created_at": int64(0),
		"model":      resp.Model,
		"output":     output,
	}
	if resp.Usage != nil {
		out["usage"] = responsesUsageFromIR(resp.Usage)
	}
	for k, v := range resp.Metadata {
		out[k] = v
	}
	for k, v := range resp.Native.Fields {
		out[k] = v
	}
	if _, ok := out["status"]; !ok {
		out["status"] = responsesResponseStatusFromIR(resp)
	}
	return json.Marshal(out)
}

func responsesOutputItemsFromIR(resp *relayir.Response) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	for _, choice := range resp.Choices {
		items, err := responsesOutputItemsFromIRChoice(choice)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func responsesOutputItemsFromIRChoice(choice relayir.Choice) ([]map[string]interface{}, error) {
	if isResponsesFamily(protocolFormat(choice.Native.Protocol)) && len(choice.Native.Raw) > 0 {
		var raw map[string]interface{}
		if err := decodeJSONUseNumber(choice.Native.Raw, &raw); err == nil {
			return []map[string]interface{}{raw}, nil
		}
	}
	var out []map[string]interface{}
	var pendingContent []schema.ContentPart
	role := responsesRole(string(choice.Role))
	status := responsesStatusFromIRFinish(choice.Finish)
	flushContent := func() {
		if len(pendingContent) == 0 {
			return
		}
		item := map[string]interface{}{
			"type":    "message",
			"role":    role,
			"content": responsesMessageContent(role, pendingContent),
			"status":  status,
		}
		out = append(out, item)
		pendingContent = nil
	}
	for _, item := range choice.Items {
		if isResponsesFamily(protocolFormat(item.Native.Protocol)) && len(item.Native.Raw) > 0 && (item.Kind == relayir.ItemOpaque || item.Native.Kind == "image_generation_call") {
			flushContent()
			var raw map[string]interface{}
			if err := decodeJSONUseNumber(item.Native.Raw, &raw); err == nil {
				out = append(out, raw)
				continue
			}
		}
		switch item.Kind {
		case relayir.ItemReasoning, relayir.ItemThinking, relayir.ItemRedactedThinking, relayir.ItemEncryptedReasoning:
			flushContent()
			out = append(out, responsesReasoningOutputItemFromIR(item, status))
		case relayir.ItemText, relayir.ItemImage, relayir.ItemFile, relayir.ItemDocument, relayir.ItemAudio, relayir.ItemRefusal, relayir.ItemExecutableCode, relayir.ItemCodeExecutionResult, relayir.ItemOpaque:
			if part, ok := schemaContentFromIR(item); ok {
				if part.ImageURL != nil && *part.ImageURL != "" {
					if mimeType, b64, ok := splitDataURI(*part.ImageURL); ok {
						flushContent()
						out = append(out, map[string]interface{}{
							"type":          "image_generation_call",
							"id":            firstNonEmptyString(item.ID, fmt.Sprintf("ig_%d", item.OriginalIndex)),
							"status":        "completed",
							"result":        b64,
							"output_format": imageOutputFormatFromMime(mimeType),
						})
						continue
					}
				}
				pendingContent = append(pendingContent, part)
			}
		case relayir.ItemToolUse, relayir.ItemFunctionCall:
			flushContent()
			call := schemaToolCallFromIR(item)
			name := call.Name
			if name == "" {
				name = call.Function.Name
			}
			callID := firstNonEmptyString(call.ID, item.CallID, item.ID)
			if name == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Responses function_call output item %d: missing required name", item.OriginalIndex)
			}
			if callID == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Responses function_call output item %d: missing required call_id", item.OriginalIndex)
			}
			out = append(out, map[string]interface{}{
				"type":      "function_call",
				"role":      role,
				"status":    status,
				"call_id":   callID,
				"name":      name,
				"arguments": call.Function.Arguments,
			})
		case relayir.ItemToolResult, relayir.ItemFunctionCallOutput:
			flushContent()
			result := schemaToolResultFromIR(item)
			if result.ToolCallID == "" {
				return nil, fmt.Errorf("cannot emit OpenAI Responses function_call_output item %d: missing required call_id", item.OriginalIndex)
			}
			out = append(out, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": result.ToolCallID,
				"output":  responsesToolResultOutput(result),
			})
		}
	}
	flushContent()
	return out, nil
}

func responsesReasoningOutputItemFromIR(item relayir.Item, status string) map[string]interface{} {
	part := schemaReasoningFromIR(item)
	out := map[string]interface{}{
		"type":   "reasoning",
		"id":     firstNonEmptyString(item.ID, generateResponsesReasoningID()),
		"status": status,
	}
	if content := responsesReasoningContent([]schema.ContentPart{part}); len(content) > 0 {
		out["content"] = content
	}
	if summary := responsesReasoningSummary([]schema.ContentPart{part}); len(summary) > 0 {
		out["summary"] = summary
	}
	if encrypted := reasoningEncryptedContent([]schema.ContentPart{part}); encrypted != "" {
		out[reasoningExtraEncryptedContent] = encrypted
	}
	for k, v := range item.Metadata {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

func responsesUsageFromIR(usage *relayir.Usage) map[string]interface{} {
	if usage == nil {
		return nil
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	out := map[string]interface{}{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
		"total_tokens":  total,
	}
	details := detailsFromRaw(usage.InputTokenDetails)
	if details == nil {
		details = map[string]interface{}{}
	}
	if usage.CacheReadTokens > 0 {
		details["cached_tokens"] = usage.CacheReadTokens
	}
	if len(details) > 0 {
		out["input_tokens_details"] = details
	}
	return out
}

func responsesStatusFromIRFinish(finish *relayir.Finish) string {
	if finish == nil {
		return "completed"
	}
	if finish.NativeReason != "" && finish.NativeReason != "function_call" {
		return responsesStatusFromFinishReason(finish.NativeReason)
	}
	return responsesStatusFromFinishReason(internalFinishReasonFromIR(finish.Reason))
}

func responsesResponseStatusFromIR(resp *relayir.Response) string {
	for _, choice := range resp.Choices {
		status := responsesStatusFromIRFinish(choice.Finish)
		if status != "" {
			return status
		}
	}
	return "completed"
}

func copyResponseRawFields(dst map[string]json.RawMessage, src map[string]json.RawMessage, keys ...string) {
	copyRawFields(dst, src, keys...)
}

// mapResponsesStatusToFinishReason converts Responses API status to internal finish reason.
func mapResponsesStatusToFinishReason(status string) string {
	switch status {
	case "completed":
		return "end_turn"
	case "incomplete":
		return "max_tokens"
	default:
		return status
	}
}

func responsesReasoningContent(parts []schema.ContentPart) []map[string]interface{} {
	var content []map[string]interface{}
	for _, part := range parts {
		if typ := reasoningPartExtraString(part, reasoningExtraType); typ == reasoningDetailTypeSummary || typ == reasoningDetailTypeEncrypted {
			continue
		}
		if part.Text == "" && reasoningSignature([]schema.ContentPart{part}) == "" {
			continue
		}
		block := map[string]interface{}{
			"type": "reasoning",
		}
		if part.Text != "" {
			block["text"] = part.Text
		}
		if sig := reasoningSignature([]schema.ContentPart{part}); sig != "" {
			block["signature"] = sig
		}
		content = append(content, block)
	}
	return content
}

func responsesStatusFromFinishReason(finishReason string) string {
	switch finishReason {
	case "end_turn", "stop":
		return "completed"
	case "max_tokens", "length":
		return "incomplete"
	default:
		return finishReason
	}
}

func splitDataURI(uri string) (mimeType, data string, ok bool) {
	if !strings.HasPrefix(uri, "data:") {
		return "", "", false
	}
	comma := strings.Index(uri, ",")
	if comma < 0 {
		return "", "", false
	}
	meta := uri[len("data:"):comma]
	data = uri[comma+1:]
	if data == "" {
		return "", "", false
	}
	mimeType = "image/png"
	if semi := strings.Index(meta, ";"); semi >= 0 {
		if meta[:semi] != "" {
			mimeType = meta[:semi]
		}
	} else if meta != "" {
		mimeType = meta
	}
	return mimeType, data, true
}

func imageOutputFormatFromMime(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func init() {
	registerResponseIRParser(FormatOpenAIChatCompletions, parseOpenAIChatResponseIR)
	registerResponseIREmitter(FormatOpenAIChatCompletions, emitOpenAIChatResponseIR)
	registerResponseIRParser(FormatOpenAIResponses, parseOpenAIResponsesResponseIR)
	registerResponseIREmitter(FormatOpenAIResponses, emitOpenAIResponsesResponseIR)
}
