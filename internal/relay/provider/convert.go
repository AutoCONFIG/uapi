package provider

import (
	"encoding/json"
	"fmt"

	newconvert "github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
)

func toConvertFormat(format Format) (newconvert.Format, error) {
	switch format {
	case FormatOpenAIChatCompletions:
		return newconvert.FormatOpenAIChatCompletions, nil
	case FormatOpenAIResponses:
		return newconvert.FormatOpenAIResponses, nil
	case FormatAnthropic:
		return newconvert.FormatAnthropic, nil
	case FormatGemini:
		return newconvert.FormatGemini, nil
	case FormatGeminiCode, FormatGeminiCLI, FormatAntigravity:
		return newconvert.FormatGeminiCLI, nil
	default:
		return "", fmt.Errorf("unsupported format %q", format)
	}
}

func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, err
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, err
	}
	return newconvert.ConvertRequest(client, upstream, body)
}

func ConvertRequestWithAdaptor(clientFormat, upstreamFormat Format, body []byte, adaptor Adaptor) ([]byte, error) {
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, err
	}
	internal, err := newconvert.ToInternalOnly(client, body)
	if err != nil {
		return nil, fmt.Errorf("ToInternal(%s): %w", clientFormat, err)
	}
	if clientFormat != upstreamFormat {
		internal.Extra = nil
	}
	if adaptor != nil {
		return adaptor.FromInternal(internal)
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, err
	}
	return newconvert.ConvertRequest(client, upstream, body)
}

func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	if upstreamFormat == clientFormat {
		if upstreamFormat == FormatOpenAIChatCompletions {
			return preserveOpenAIChatReasoningAlias(body), nil
		}
		return body, nil
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, err
	}
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, err
	}
	return newconvert.ConvertResponse(upstream, client, body)
}

func preserveOpenAIChatReasoningAlias(body []byte) []byte {
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	choices, _ := root["choices"].([]interface{})
	changed := false
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]interface{})
		message, _ := choice["message"].(map[string]interface{})
		if message == nil {
			continue
		}
		reasoning, ok := message["reasoning_content"]
		if !ok {
			continue
		}
		if _, exists := message["reasoning"]; !exists {
			message["reasoning"] = reasoning
			changed = true
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

func ToProviderInternal(ir *newconvert.InternalRequest) *InternalRequest {
	return ir
}

func FromProviderInternal(pr *InternalRequest) *newconvert.InternalRequest {
	return pr
}
