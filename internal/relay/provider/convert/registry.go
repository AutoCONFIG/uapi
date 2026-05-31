package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

type requestIRParser func(body []byte) (*ir.Request, error)
type requestIREmitter func(req *ir.Request) ([]byte, error)

var requestIRParsers = map[Format]requestIRParser{}
var requestIREmitters = map[Format]requestIREmitter{}

func registerRequestIRParser(f Format, fn requestIRParser) {
	requestIRParsers[f] = fn
}

func registerRequestIREmitter(f Format, fn requestIREmitter) {
	requestIREmitters[f] = fn
}

// ConvertRequest converts a request from clientFormat to upstreamFormat.
func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	result, _, err := ConvertRequestDetailed(clientFormat, upstreamFormat, body)
	return result, err
}

// NormalizeRequestSameProtocol prepares a request for same-protocol forwarding.
// It intentionally avoids cross-protocol IR emission: same-format traffic only
// gets client-noise cleanup plus protocol parser validation.
func NormalizeRequestSameProtocol(format Format, body []byte) ([]byte, error) {
	body = cleanJSONUndefinedPlaceholders(body)
	toIR, ok := requestIRParsers[format]
	if !ok {
		return nil, fmt.Errorf("no request parser for format %q", format)
	}
	if _, err := toIR(body); err != nil {
		return nil, fmt.Errorf("parse request %s: %w", format, err)
	}
	return body, nil
}

// ConvertRequestDetailed converts a request and returns the IR used for
// auditing loss records and native preservation decisions.
func ConvertRequestDetailed(clientFormat, upstreamFormat Format, body []byte) ([]byte, *ir.Request, error) {
	body = cleanJSONUndefinedPlaceholders(body)
	toIR, ok := requestIRParsers[clientFormat]
	if !ok {
		return nil, nil, fmt.Errorf("no request parser for format %q", clientFormat)
	}
	fromIR, ok := requestIREmitters[upstreamFormat]
	if !ok {
		return nil, nil, fmt.Errorf("no request emitter for format %q", upstreamFormat)
	}

	req, err := toIR(body)
	if err != nil {
		return nil, nil, fmt.Errorf("parse request %s: %w", clientFormat, err)
	}
	PrepareRequestForTarget(req, clientFormat, upstreamFormat)
	result, err := fromIR(req)
	if err != nil {
		return nil, req, fmt.Errorf("emit request %s: %w", upstreamFormat, err)
	}
	return result, req, nil
}

func PrepareRequestForTarget(req *ir.Request, clientFormat, upstreamFormat Format) {
	if req == nil {
		return
	}
	req.TargetProtocol = irProtocol(upstreamFormat)
	completeLossProtocols(req, clientFormat, upstreamFormat)
	recordTargetSpecificContentLosses(req, clientFormat, upstreamFormat)
	dropExtraForCrossProtocol(req, clientFormat, upstreamFormat)
}

func recordTargetSpecificContentLosses(req *ir.Request, clientFormat, upstreamFormat Format) {
	if req == nil {
		return
	}
	if len(req.Generation.LogitBias) > 0 && !supportsLogitBias(upstreamFormat) {
		req.Losses = append(req.Losses, irloss(clientFormat, upstreamFormat, "$.logit_bias", "logit_bias", req.Generation.LogitBias, "OpenAI Chat logit_bias has no equivalent in target protocol and is not emitted"))
	}
	recordItemLosses := func(items []ir.Item) {
		for i := range items {
			if isResponsesFamily(upstreamFormat) && !isResponsesFamily(clientFormat) {
				if raw := items[i].Metadata["cache_control"]; len(raw) > 0 {
					items[i].Losses = append(items[i].Losses, irloss(clientFormat, upstreamFormat, "$.content[].cache_control", "cache_control", raw, "Anthropic cache_control has no OpenAI Responses content-part equivalent and is not emitted"))
				}
			}
			if items[i].ToolResult != nil && len(items[i].ToolResult.OutputRaw) > 0 && !supportsStructuredToolResultOutput(upstreamFormat) {
				items[i].Losses = append(items[i].Losses, irloss(clientFormat, upstreamFormat, "$.tool_result.output", "output", items[i].ToolResult.OutputRaw, "structured tool result output is serialized as text for target protocol"))
			}
		}
	}
	for i := range req.Instructions {
		recordItemLosses(req.Instructions[i].Items)
	}
	for i := range req.Turns {
		recordItemLosses(req.Turns[i].Items)
	}
}

func supportsLogitBias(format Format) bool {
	return format == FormatOpenAIChatCompletions
}

func supportsStructuredToolResultOutput(format Format) bool {
	if isResponsesFamily(format) {
		return true
	}
	switch format {
	case FormatGemini, FormatGeminiCode, FormatGeminiCLI, FormatAntigravity:
		return true
	default:
		return false
	}
}

func completeLossProtocols(req *ir.Request, clientFormat, upstreamFormat Format) {
	source := irProtocol(clientFormat)
	target := irProtocol(upstreamFormat)
	for i := range req.Losses {
		if req.Losses[i].SourceProtocol == "" {
			req.Losses[i].SourceProtocol = source
		}
		if req.Losses[i].TargetProtocol == "" {
			req.Losses[i].TargetProtocol = target
		}
	}
	for i := range req.Instructions {
		for j := range req.Instructions[i].Items {
			for k := range req.Instructions[i].Items[j].Losses {
				if req.Instructions[i].Items[j].Losses[k].SourceProtocol == "" {
					req.Instructions[i].Items[j].Losses[k].SourceProtocol = source
				}
				if req.Instructions[i].Items[j].Losses[k].TargetProtocol == "" {
					req.Instructions[i].Items[j].Losses[k].TargetProtocol = target
				}
			}
		}
	}
	for i := range req.Turns {
		for j := range req.Turns[i].Items {
			for k := range req.Turns[i].Items[j].Losses {
				if req.Turns[i].Items[j].Losses[k].SourceProtocol == "" {
					req.Turns[i].Items[j].Losses[k].SourceProtocol = source
				}
				if req.Turns[i].Items[j].Losses[k].TargetProtocol == "" {
					req.Turns[i].Items[j].Losses[k].TargetProtocol = target
				}
			}
		}
	}
}

func dropExtraForCrossProtocol(req *ir.Request, clientFormat, upstreamFormat Format) {
	if req == nil || sameNativeRequestFamily(clientFormat, upstreamFormat) {
		return
	}
	for key, raw := range req.Native.Fields {
		req.Losses = append(req.Losses, irloss(clientFormat, upstreamFormat, "$."+key, key, raw, "top-level native field is not emitted across protocols"))
	}
	for key, raw := range req.Metadata {
		if _, exists := req.Native.Fields[key]; exists {
			continue
		}
		req.Losses = append(req.Losses, irloss(clientFormat, upstreamFormat, "$."+key, key, raw, "top-level native field is not emitted across protocols"))
	}
	for key, raw := range req.Generation.Extra {
		req.Losses = append(req.Losses, irloss(clientFormat, upstreamFormat, "$.generation."+key, key, raw, "provider-specific generation config field is not emitted across protocols"))
	}
	req.Native.Fields = nil
	req.Native.Unknown = nil
	req.Metadata = nil
	req.Generation.Extra = nil
}

func sameNativeRequestFamily(a, b Format) bool {
	if a == b {
		return true
	}
	return isAnthropicFamily(a) && isAnthropicFamily(b)
}

func irloss(source, target Format, path, field string, raw []byte, reason string) ir.Loss {
	return ir.Loss{
		SourceProtocol: irProtocol(source),
		TargetProtocol: irProtocol(target),
		Path:           path,
		Field:          field,
		Kind:           "unsupported_field",
		Reason:         reason,
		Severity:       ir.LossWarning,
		ValueHash:      rawHash(raw),
		Preserved:      len(raw) > 0,
		Native:         append([]byte(nil), raw...),
	}
}

func rawHash(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type responseIRParser func(body []byte) (*ir.Response, error)
type responseIREmitter func(resp *ir.Response) ([]byte, error)

var responseIRParsers = map[Format]responseIRParser{}
var responseIREmitters = map[Format]responseIREmitter{}

func registerResponseIRParser(f Format, fn responseIRParser) {
	responseIRParsers[f] = fn
}

func registerResponseIREmitter(f Format, fn responseIREmitter) {
	responseIREmitters[f] = fn
}

func ToResponseIR(format Format, body []byte) (*ir.Response, error) {
	parser, ok := responseIRParsers[format]
	if !ok {
		return nil, fmt.Errorf("no response parser for format %q", format)
	}
	return parser(body)
}

func FromResponseIR(resp *ir.Response, target Format) ([]byte, error) {
	emitter, ok := responseIREmitters[target]
	if !ok {
		return nil, fmt.Errorf("no response emitter for format %q", target)
	}
	return emitter(resp)
}

// ConvertResponse converts a response from upstreamFormat to clientFormat.
func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	respIR, err := ToResponseIR(upstreamFormat, body)
	if err != nil {
		return nil, err
	}
	respIR.TargetProtocol = irProtocol(clientFormat)
	return FromResponseIR(respIR, clientFormat)
}
