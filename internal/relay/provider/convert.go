package provider

import "fmt"

var toInternalConverters = map[Format]func([]byte) (*InternalRequest, error){}
var fromInternalConverters = map[Format]func(*InternalRequest) ([]byte, error){}

func RegisterToInternal(format Format, converter func([]byte) (*InternalRequest, error)) {
	toInternalConverters[format] = converter
}

func RegisterFromInternal(format Format, converter func(*InternalRequest) ([]byte, error)) {
	fromInternalConverters[format] = converter
}

func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	if clientFormat == upstreamFormat {
		return body, nil
	}
	toInternal, ok := toInternalConverters[clientFormat]
	if !ok {
		return nil, fmt.Errorf("no ToInternal converter for format: %s", clientFormat)
	}
	internal, err := toInternal(body)
	if err != nil {
		return nil, fmt.Errorf("ToInternal conversion failed: %w", err)
	}
	fromInternal, ok := fromInternalConverters[upstreamFormat]
	if !ok {
		return nil, fmt.Errorf("no FromInternal converter for format: %s", upstreamFormat)
	}
	return fromInternal(internal)
}
