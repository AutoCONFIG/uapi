package convert

import (
	"fmt"
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
	dropExtraForCrossProtocol(ir, clientFormat, upstreamFormat)
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
	ir.Extra = nil
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
