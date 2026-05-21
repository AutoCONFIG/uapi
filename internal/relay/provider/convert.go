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

func ConvertRequestWithAdaptor(clientFormat, upstreamFormat Format, body []byte, adaptor Adaptor) ([]byte, error) {
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
	if adaptor == nil {
		fromInternal, ok := fromInternalConverters[upstreamFormat]
		if !ok {
			return nil, fmt.Errorf("no FromInternal converter for format: %s", upstreamFormat)
		}
		return fromInternal(internal)
	}
	return adaptor.FromInternal(internal)
}

// --- Response conversion (upstream → InternalResponse → client) ---

var toResponseInternal = map[Format]func([]byte) (*InternalResponse, error){}
var fromResponseInternal = map[Format]func(*InternalResponse) ([]byte, error){}

func RegisterToResponseInternal(format Format, converter func([]byte) (*InternalResponse, error)) {
	toResponseInternal[format] = converter
}

func RegisterFromResponseInternal(format Format, converter func(*InternalResponse) ([]byte, error)) {
	fromResponseInternal[format] = converter
}

func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	if upstreamFormat == clientFormat {
		return body, nil
	}
	toResp, ok := toResponseInternal[upstreamFormat]
	if !ok {
		return nil, fmt.Errorf("no response ToInternal converter for format: %s", upstreamFormat)
	}
	internal, err := toResp(body)
	if err != nil {
		return nil, fmt.Errorf("response ToInternal conversion failed: %w", err)
	}
	fromResp, ok := fromResponseInternal[clientFormat]
	if !ok {
		return nil, fmt.Errorf("no response FromInternal converter for format: %s", clientFormat)
	}
	return fromResp(internal)
}
