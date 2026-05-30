package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
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
	body = cleanJSONUndefinedPlaceholders(body)
	if clientFormat == upstreamFormat && clientFormat == FormatOpenAIChatCompletions {
		return body, nil
	}
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
	attachRawRequest(ir, body)
	dropExtraForCrossProtocol(ir, clientFormat, upstreamFormat)
	ir.RefreshIR()
	result, err := fromInternal(ir)
	if err != nil {
		return nil, fmt.Errorf("FromInternal(%s): %w", upstreamFormat, err)
	}
	return result, nil
}

func dropExtraForCrossProtocol(ir *InternalRequest, clientFormat, upstreamFormat Format) {
	if ir == nil || clientFormat == upstreamFormat {
		return
	}
	for key, raw := range ir.Extra {
		ir.Losses = append(ir.Losses, irloss(clientFormat, upstreamFormat, "$."+key, key, raw, "top-level native field is not emitted across protocols by the compatibility converter"))
	}
	ir.Extra = nil
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

// ToInternalOnly converts a request body to InternalRequest without converting back.
// Useful for extracting model/messages for routing decisions.
func ToInternalOnly(format Format, body []byte) (*InternalRequest, error) {
	body = cleanJSONUndefinedPlaceholders(body)
	toInternal, ok := toInternalRegistry[format]
	if !ok {
		return nil, fmt.Errorf("no ToInternal converter for format %q", format)
	}
	ir, err := toInternal(body)
	if err != nil {
		return nil, err
	}
	attachRawRequest(ir, body)
	ir.RefreshIR()
	return ir, nil
}

func attachRawRequest(ir *InternalRequest, body []byte) {
	if ir == nil || len(ir.RawRequestBody) > 0 {
		return
	}
	ir.RawRequestBody = append([]byte(nil), body...)
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
