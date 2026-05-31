package provider

import (
	"encoding/json"
	"fmt"

	newconvert "github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func toConvertFormat(format Format) (newconvert.Format, error) {
	switch format {
	case FormatOpenAIChatCompletions:
		return newconvert.FormatOpenAIChatCompletions, nil
	case FormatOpenAIResponses:
		return newconvert.FormatOpenAIResponses, nil
	case FormatCodexResponses:
		return newconvert.FormatCodexResponses, nil
	case FormatAnthropic:
		return newconvert.FormatAnthropic, nil
	case FormatClaudeCode:
		return newconvert.FormatClaudeCode, nil
	case FormatGemini:
		return newconvert.FormatGemini, nil
	case FormatGeminiCode:
		return newconvert.FormatGeminiCode, nil
	case FormatGeminiCLI:
		return newconvert.FormatGeminiCLI, nil
	case FormatAntigravity:
		return newconvert.FormatAntigravity, nil
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

func ConvertRequestDetailed(clientFormat, upstreamFormat Format, body []byte) ([]byte, *ir.Request, error) {
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, nil, err
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, nil, err
	}
	return newconvert.ConvertRequestDetailed(client, upstream, body)
}

func ConvertRequestWithAdaptor(clientFormat, upstreamFormat Format, body []byte, adaptor Adaptor) ([]byte, error) {
	client, err := toConvertFormat(clientFormat)
	if err != nil {
		return nil, err
	}
	req, err := newconvert.ToIR(client, body)
	if err != nil {
		return nil, fmt.Errorf("parse request %s: %w", clientFormat, err)
	}
	newconvert.PrepareRequestForTarget(req, client, mustConvertFormat(upstreamFormat))
	if adaptor != nil {
		return adaptor.FromIR(req)
	}
	upstream, err := toConvertFormat(upstreamFormat)
	if err != nil {
		return nil, err
	}
	return newconvert.ConvertRequest(client, upstream, body)
}

func mustConvertFormat(format Format) newconvert.Format {
	converted, err := toConvertFormat(format)
	if err != nil {
		return newconvert.Format(format)
	}
	return converted
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
