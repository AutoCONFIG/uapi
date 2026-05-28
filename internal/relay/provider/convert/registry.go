package convert

import (
	"encoding/json"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// toInternalFunc converts raw protocol bytes into an InternalRequest.
type toInternalFunc func(body []byte) (*InternalRequest, error)

// fromInternalFunc converts an InternalRequest into raw protocol bytes.
type fromInternalFunc func(ir *InternalRequest) ([]byte, error)

var toInternalRegistry = map[Format]toInternalFunc{}
var fromInternalRegistry = map[Format]fromInternalFunc{}

// GetFromInternalFunc returns the FromInternal converter for a format.
func GetFromInternalFunc(f Format) (fromInternalFunc, bool) {
	fn, ok := fromInternalRegistry[f]
	return fn, ok
}

// RegisterToInternal registers a converter from protocol bytes to InternalRequest.
func RegisterToInternal(f Format, fn toInternalFunc) {
	toInternalRegistry[f] = fn
}

// RegisterFromInternal registers a converter from InternalRequest to protocol bytes.
func RegisterFromInternal(f Format, fn fromInternalFunc) {
	fromInternalRegistry[f] = fn
}

// ConvertRequest converts a request from clientFormat to upstreamFormat.
// It first converts the raw bytes to InternalRequest using clientFormat's
// ToInternal converter, then converts InternalRequest to upstreamFormat bytes
// using upstreamFormat's FromInternal converter.
func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	toInternal, ok := toInternalRegistry[clientFormat]
	if !ok {
		return nil, fmt.Errorf("no ToInternal converter for format %q", clientFormat)
	}
	fromInternal, ok := fromInternalRegistry[upstreamFormat]
	if !ok {
		return nil, fmt.Errorf("no FromInternal converter for format %q", upstreamFormat)
	}

	ir, err := toInternal(body)
	if err != nil {
		return nil, fmt.Errorf("ToInternal(%s): %w", clientFormat, err)
	}
	result, err := fromInternal(ir)
	if err != nil {
		return nil, fmt.Errorf("FromInternal(%s): %w", upstreamFormat, err)
	}
	return result, nil
}

// ToInternalOnly converts a request body to InternalRequest without converting back.
// Useful for extracting model/messages for routing decisions.
func ToInternalOnly(format Format, body []byte) (*InternalRequest, error) {
	toInternal, ok := toInternalRegistry[format]
	if !ok {
		return nil, fmt.Errorf("no ToInternal converter for format %q", format)
	}
	return toInternal(body)
}

// ToInternalFromProvider converts provider.InternalRequest to convert.InternalRequest.
// This is needed when adaptors receive provider.InternalRequest but need to pass
// convert.InternalRequest to the convert package's FromInternal converters.
func ToInternalFromProvider(pr *provider.InternalRequest) *InternalRequest {
	if pr == nil {
		return nil
	}
	ir := &InternalRequest{
		Model:        pr.Model,
		Stream:       pr.Stream,
		MaxTokens:    pr.MaxTokens,
		Temperature:  pr.Temperature,
		TopP:         pr.TopP,
		TopK:         pr.TopK,
		StopWords:    pr.StopWords,
		Instructions: pr.Instructions,
		Reasoning:    toRawMessage(pr.Reasoning),
		Thinking:     toRawMessage(pr.Thinking),
		SourceFormat: "", // Unknown when coming from provider
	}

	// Convert Messages
	if len(pr.Messages) > 0 {
		ir.Messages = make([]InternalMessage, len(pr.Messages))
		for i, m := range pr.Messages {
			parts := make([]schema.ContentPart, len(m.Content))
			for j, p := range m.Content {
				parts[j] = schema.ContentPart{
					Type:     p.Type,
					Text:     p.Text,
					ImageURL: p.ImageURL,
					Refusal:  p.Refusal,
					Extra:    convertProviderExtra(p.Extra),
				}
			}
			tcs := make([]schema.ToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				tcs[j] = schema.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Name: tc.Name,
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				}
			}
			var tr *schema.ToolResult
			if m.ToolResult != nil {
				tr = &schema.ToolResult{
					ToolCallID: m.ToolResult.ToolCallID,
					Content:    m.ToolResult.Content,
					IsError:    m.ToolResult.IsError,
				}
			}
			ir.Messages[i] = InternalMessage{
				Role:             m.Role,
				Content:          parts,
				ToolCalls:        tcs,
				ToolResult:       tr,
				ReasoningContent: convertProviderContentParts(m.ReasoningContent),
				Name:             m.Name,
			}
		}
	}

	// Convert Tools
	if len(pr.Tools) > 0 {
		ir.Tools = make([]schema.Tool, len(pr.Tools))
		for i, t := range pr.Tools {
			var params json.RawMessage
			if t.Parameters != nil {
				pbytes, _ := json.Marshal(t.Parameters)
				params = pbytes
			}
			ir.Tools[i] = schema.Tool{
				Type:        t.Type,
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			}
		}
	}

	return ir
}

// convertProviderExtra converts map[string]interface{} to map[string]json.RawMessage.
func convertProviderExtra(extra map[string]interface{}) map[string]json.RawMessage {
	if extra == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(extra))
	for k, v := range extra {
		result[k], _ = json.Marshal(v)
	}
	return result
}

// convertProviderContentParts converts provider.InternalContentPart to schema.ContentPart.
func convertProviderContentParts(parts []provider.InternalContentPart) []schema.ContentPart {
	result := make([]schema.ContentPart, len(parts))
	for i, p := range parts {
		result[i] = schema.ContentPart{
			Type:     p.Type,
			Text:     p.Text,
			ImageURL: p.ImageURL,
			Refusal:  p.Refusal,
			Extra:    convertProviderExtra(p.Extra),
		}
	}
	return result
}

// response converter types
type toResponseInternalFunc func(body []byte) (*InternalResponse, error)
type fromResponseInternalFunc func(ir *InternalResponse) ([]byte, error)

var toResponseRegistry = map[Format]toResponseInternalFunc{}
var fromResponseRegistry = map[Format]fromResponseInternalFunc{}

// RegisterToResponseInternal registers a response converter from protocol bytes to InternalResponse.
func RegisterToResponseInternal(f Format, fn toResponseInternalFunc) {
	toResponseRegistry[f] = fn
}

// RegisterFromResponseInternal registers a response converter from InternalResponse to protocol bytes.
func RegisterFromResponseInternal(f Format, fn fromResponseInternalFunc) {
	fromResponseRegistry[f] = fn
}

// ConvertResponse converts a response from upstreamFormat to clientFormat.
func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	toResp, ok := toResponseRegistry[upstreamFormat]
	if !ok {
		return nil, fmt.Errorf("no response ToInternal converter for format %q", upstreamFormat)
	}
	fromResp, ok := fromResponseRegistry[clientFormat]
	if !ok {
		return nil, fmt.Errorf("no response FromInternal converter for format %q", clientFormat)
	}
	ir, err := toResp(body)
	if err != nil {
		return nil, err
	}
	return fromResp(ir)
}

// ToProviderInternal converts convert.InternalRequest to provider.InternalRequest.
func ToProviderInternal(ir *InternalRequest) *provider.InternalRequest {
	if ir == nil {
		return nil
	}
	pr := &provider.InternalRequest{
		Model:       ir.Model,
		Stream:      ir.Stream,
		MaxTokens:   ir.MaxTokens,
		Temperature: ir.Temperature,
		TopP:        ir.TopP,
		TopK:        ir.TopK,
		StopWords:   ir.StopWords,
		Instructions: ir.Instructions,
		Reasoning:   ir.Reasoning,
		Thinking:    ir.Thinking,
	}

	// Convert Messages
	if len(ir.Messages) > 0 {
		pr.Messages = make([]provider.InternalMessage, len(ir.Messages))
		for i, m := range ir.Messages {
			parts := make([]provider.InternalContentPart, len(m.Content))
			for j, p := range m.Content {
				parts[j] = provider.InternalContentPart{
					Type:     p.Type,
					Text:     p.Text,
					ImageURL: p.ImageURL,
					Refusal:  p.Refusal,
					Extra:    convertExtra(p.Extra),
				}
			}
			tcs := make([]provider.InternalToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				tcs[j] = provider.InternalToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Function.Arguments,
				}
			}
			var tr *provider.InternalToolResult
			if m.ToolResult != nil {
				tr = &provider.InternalToolResult{
					ToolCallID: m.ToolResult.ToolCallID,
					Content:    m.ToolResult.Content,
					IsError:    m.ToolResult.IsError,
				}
			}
			pr.Messages[i] = provider.InternalMessage{
				Role:             m.Role,
				Content:          parts,
				ToolCalls:        tcs,
				ToolResult:       tr,
				ReasoningContent: convertContentParts(m.ReasoningContent),
			}
		}
	}

	// Convert Tools
	if len(ir.Tools) > 0 {
		pr.Tools = make([]provider.InternalTool, len(ir.Tools))
		for i, t := range ir.Tools {
			pr.Tools[i] = provider.InternalTool{
				Type:        t.Type,
				Name:       t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			}
		}
	}

	return pr
}

// convertExtra converts map[string]json.RawMessage to map[string]interface{}.
func convertExtra(extra map[string]json.RawMessage) map[string]interface{} {
	if extra == nil {
		return nil
	}
	result := make(map[string]interface{}, len(extra))
	for k, v := range extra {
		var iface interface{}
		json.Unmarshal(v, &iface)
		result[k] = iface
	}
	return result
}

// convertContentParts converts schema.ContentPart to provider.InternalContentPart.
func convertContentParts(parts []schema.ContentPart) []provider.InternalContentPart {
	result := make([]provider.InternalContentPart, len(parts))
	for i, p := range parts {
		result[i] = provider.InternalContentPart{
			Type:     p.Type,
			Text:     p.Text,
			ImageURL: p.ImageURL,
			Refusal:  p.Refusal,
			Extra:    convertExtra(p.Extra),
		}
	}
	return result
}

// FromProviderInternal converts provider.InternalRequest to convert.InternalRequest.
func FromProviderInternal(pr *provider.InternalRequest) *InternalRequest {
	if pr == nil {
		return nil
	}
	ir := &InternalRequest{
		Model:        pr.Model,
		Stream:       pr.Stream,
		MaxTokens:    pr.MaxTokens,
		Temperature:  pr.Temperature,
		TopP:         pr.TopP,
		TopK:         pr.TopK,
		StopWords:    pr.StopWords,
		Instructions: pr.Instructions,
		Reasoning:    toRawMessage(pr.Reasoning),
		Thinking:     toRawMessage(pr.Thinking),
	}

	// Convert Messages
	if len(pr.Messages) > 0 {
		ir.Messages = make([]InternalMessage, len(pr.Messages))
		for i, m := range pr.Messages {
			parts := make([]schema.ContentPart, len(m.Content))
			for j, p := range m.Content {
				parts[j] = schema.ContentPart{
					Type:     p.Type,
					Text:     p.Text,
					ImageURL: p.ImageURL,
					Refusal:  p.Refusal,
					Extra:    toRawMessageMap(p.Extra),
				}
			}
			tcs := make([]schema.ToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				tcs[j] = schema.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				}
			}
			var tr *schema.ToolResult
			if m.ToolResult != nil {
				tr = &schema.ToolResult{
					ToolCallID: m.ToolResult.ToolCallID,
					Content:    m.ToolResult.Content,
					IsError:    m.ToolResult.IsError,
				}
			}
			ir.Messages[i] = InternalMessage{
				Role:             m.Role,
				Content:          parts,
				ToolCalls:        tcs,
				ToolResult:       tr,
				ReasoningContent: convertProviderContentParts(m.ReasoningContent),
				Name:             m.Name,
			}
		}
	}

	// Convert Tools
	if len(pr.Tools) > 0 {
		ir.Tools = make([]schema.Tool, len(pr.Tools))
		for i, t := range pr.Tools {
			ir.Tools[i] = schema.Tool{
				Type:        t.Type,
				Name:        t.Name,
				Description: t.Description,
				Parameters:  toRawMessage(t.Parameters),
			}
		}
	}

	return ir
}

// toRawMessage converts interface{} to json.RawMessage.
func toRawMessage(v interface{}) json.RawMessage {
	if v == nil {
		return nil
	}
	data, _ := json.Marshal(v)
	return data
}

// toRawMessageMap converts map[string]interface{} to map[string]json.RawMessage.
func toRawMessageMap(m map[string]interface{}) map[string]json.RawMessage {
	if m == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		data, _ := json.Marshal(v)
		result[k] = data
	}
	return result
}