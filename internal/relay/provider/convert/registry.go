package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

// adapterRequestParser converts raw protocol bytes into a adapter request.
type adapterRequestParser func(body []byte) (*adapterRequest, error)

// adapterRequestEmitter converts a adapter request into raw protocol bytes.
type adapterRequestEmitter func(ir *adapterRequest) ([]byte, error)

type requestIRParser func(body []byte) (*ir.Request, error)
type requestIREmitter func(req *ir.Request) ([]byte, error)

var adapterRequestParsers = map[Format]adapterRequestParser{}
var adapterRequestEmitters = map[Format]adapterRequestEmitter{}
var requestIRParsers = map[Format]requestIRParser{}
var requestIREmitters = map[Format]requestIREmitter{}

// RegisterRequestParser registers a converter from protocol bytes to a adapter request.
func RegisterRequestParser(f Format, fn adapterRequestParser) {
	adapterRequestParsers[f] = fn
	requestIRParsers[f] = func(body []byte) (*ir.Request, error) {
		req, err := parseAdapterRequest(f, body, fn)
		if err != nil {
			return nil, err
		}
		return req.ToIR(), nil
	}
}

// RegisterRequestEmitter registers a converter from a adapter request to protocol bytes.
func RegisterRequestEmitter(f Format, fn adapterRequestEmitter) {
	adapterRequestEmitters[f] = fn
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

// RegisterRequestIRParser registers a native IR parser for protocols with
// protocol-specific envelopes.
func RegisterRequestIRParser(f Format, fn requestIRParser) {
	requestIRParsers[f] = fn
}

// RegisterRequestIREmitter registers a native IR emitter for protocols whose
// output needs protocol-specific envelope handling.
func RegisterRequestIREmitter(f Format, fn requestIREmitter) {
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
	ir.IR = ir.buildIR()
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

var responseParsers = map[Format]responseParser{}
var responseEmitters = map[Format]responseEmitter{}

// RegisterResponseParser registers a response converter from protocol bytes to adapterResponse.
func RegisterResponseParser(f Format, fn responseParser) {
	responseParsers[f] = fn
}

// RegisterResponseEmitter registers a response converter from adapterResponse to protocol bytes.
func RegisterResponseEmitter(f Format, fn responseEmitter) {
	responseEmitters[f] = fn
}

// ConvertResponse converts a response from upstreamFormat to clientFormat.
func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	toResp, ok := responseParsers[upstreamFormat]
	if !ok {
		return nil, fmt.Errorf("no response parser for format %q", upstreamFormat)
	}
	fromResp, ok := responseEmitters[clientFormat]
	if !ok {
		return nil, fmt.Errorf("no response emitter for format %q", clientFormat)
	}
	ir, err := toResp(body)
	if err != nil {
		return nil, err
	}
	respIR := ir.ToIR(upstreamFormat)
	respIR.TargetProtocol = irProtocol(clientFormat)
	return fromResp(adapterResponseFromIR(respIR))
}
