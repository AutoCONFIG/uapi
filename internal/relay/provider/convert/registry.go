package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

// adapterRequestParser is a protocol-local parser stage; the package registry
// exposes only ir.Request parsers.
type adapterRequestParser func(body []byte) (*adapterRequest, error)

// adapterRequestEmitter is a protocol-local emitter stage; the package registry
// exposes only ir.Request emitters.
type adapterRequestEmitter func(ir *adapterRequest) ([]byte, error)

type requestIRParser func(body []byte) (*ir.Request, error)
type requestIREmitter func(req *ir.Request) ([]byte, error)

var requestIRParsers = map[Format]requestIRParser{}
var requestIREmitters = map[Format]requestIREmitter{}

func registerAdapterRequestParser(f Format, fn adapterRequestParser) {
	requestIRParsers[f] = func(body []byte) (*ir.Request, error) {
		req, err := parseAdapterRequest(f, body, fn)
		if err != nil {
			return nil, err
		}
		return req.ToIR(), nil
	}
}

func registerAdapterRequestEmitter(f Format, fn adapterRequestEmitter) {
	requestIREmitters[f] = func(req *ir.Request) ([]byte, error) {
		internal := adapterRequestFromIR(req)
		if internal.SourceFormat == "" {
			internal.SourceFormat = protocolFormat(req.SourceProtocol)
		}
		if internal.SourceFormat == "" {
			internal.SourceFormat = f
		}
		return fn(internal)
	}
}

// registerRequestIRParser registers a native IR parser for protocols with
// protocol-specific envelopes.
func registerRequestIRParser(f Format, fn requestIRParser) {
	requestIRParsers[f] = fn
}

// registerRequestIREmitter registers a native IR emitter for protocols whose
// output needs protocol-specific envelope handling.
func registerRequestIREmitter(f Format, fn requestIREmitter) {
	requestIREmitters[f] = fn
}

// ConvertRequest converts a request from clientFormat to upstreamFormat.
func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	result, _, err := ConvertRequestDetailed(clientFormat, upstreamFormat, body)
	return result, err
}

// ConvertRequestDetailed converts a request and returns the IR used for
// auditing loss records and native preservation decisions.
func ConvertRequestDetailed(clientFormat, upstreamFormat Format, body []byte) ([]byte, *ir.Request, error) {
	body = cleanJSONUndefinedPlaceholders(body)
	if clientFormat == upstreamFormat && clientFormat == FormatOpenAIChatCompletions {
		req, _ := ToIR(clientFormat, body)
		PrepareRequestForTarget(req, clientFormat, upstreamFormat)
		return body, req, nil
	}
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
	dropExtraForCrossProtocol(req, clientFormat, upstreamFormat)
}

func dropExtraForCrossProtocol(req *ir.Request, clientFormat, upstreamFormat Format) {
	if req == nil || clientFormat == upstreamFormat {
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
	req.Native.Fields = nil
	req.Native.Unknown = nil
	req.Metadata = nil
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

func parseAdapterRequest(format Format, body []byte, parse adapterRequestParser) (*adapterRequest, error) {
	ir, err := parse(body)
	if err != nil {
		return nil, err
	}
	attachRawRequest(ir, body)
	return ir, nil
}

func attachRawRequest(ir *adapterRequest, body []byte) {
	if ir == nil || len(ir.RawRequestBody) > 0 {
		return
	}
	ir.RawRequestBody = append([]byte(nil), body...)
}

// response converter types
type responseParser func(body []byte) (*adapterResponse, error)
type responseEmitter func(ir *adapterResponse) ([]byte, error)
type responseIRParser func(body []byte) (*ir.Response, error)
type responseIREmitter func(resp *ir.Response) ([]byte, error)

var responseIRParsers = map[Format]responseIRParser{}
var responseIREmitters = map[Format]responseIREmitter{}

func registerAdapterResponseParser(f Format, fn responseParser) {
	responseIRParsers[f] = func(body []byte) (*ir.Response, error) {
		resp, err := fn(body)
		if err != nil {
			return nil, err
		}
		return resp.ToIR(f), nil
	}
}

func registerAdapterResponseEmitter(f Format, fn responseEmitter) {
	responseIREmitters[f] = func(resp *ir.Response) ([]byte, error) {
		return fn(adapterResponseFromIR(resp))
	}
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
