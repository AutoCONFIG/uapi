package convert

import (
	"encoding/json"
	"fmt"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func parseCodexRequest(body []byte) (*relayir.Request, error) {
	req, err := parseOpenAIResponsesRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatCodexResponses
	out := req.ToIR()
	normalizeNativeRequestProtocol(out, relayir.ProtocolCodex)
	return out, nil
}

func emitCodexRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatCodexResponses
	return emitOpenAIResponsesRequest(internal)
}

func parseClaudeCodeRequest(body []byte) (*relayir.Request, error) {
	req, err := parseAnthropicRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatClaudeCode
	out := req.ToIR()
	normalizeNativeRequestProtocol(out, relayir.ProtocolClaudeCode)
	return out, nil
}

func emitClaudeCodeRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatClaudeCode
	return emitAnthropicRequest(internal)
}

func parseGeminiCodeRequest(body []byte) (*relayir.Request, error) {
	req, err := parseGeminiCLIRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatGeminiCode
	out := req.ToIR()
	normalizeNativeRequestProtocol(out, relayir.ProtocolGeminiCode)
	return out, nil
}

func emitGeminiCodeRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatGeminiCode
	return emitGeminiCLIRequest(internal)
}

func parseAntigravityRequest(body []byte) (*relayir.Request, error) {
	req, err := parseGeminiCLIRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatAntigravity
	out := req.ToIR()
	normalizeNativeRequestProtocol(out, relayir.ProtocolAntigravity)
	return out, nil
}

func emitAntigravityRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatAntigravity
	body, err := emitGeminiCLIRequest(internal)
	if err != nil {
		return nil, err
	}
	var envelope map[string]interface{}
	if err := decodeJSONUseNumber(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode antigravity envelope: %w", err)
	}
	if _, ok := envelope["userAgent"]; !ok {
		envelope["userAgent"] = "antigravity"
	}
	if _, ok := envelope["requestType"]; !ok {
		envelope["requestType"] = "generateContent"
	}
	return json.Marshal(envelope)
}

func normalizeNativeRequestProtocol(req *relayir.Request, protocol relayir.Protocol) {
	if req == nil {
		return
	}
	req.SourceProtocol = protocol
	req.Native.Protocol = protocol
	for i := range req.Losses {
		req.Losses[i].SourceProtocol = protocol
	}
	for i := range req.Instructions {
		req.Instructions[i].Native.Protocol = protocol
		for j := range req.Instructions[i].Items {
			req.Instructions[i].Items[j].Native.Protocol = protocol
			for k := range req.Instructions[i].Items[j].Losses {
				req.Instructions[i].Items[j].Losses[k].SourceProtocol = protocol
			}
		}
	}
	for i := range req.Turns {
		req.Turns[i].Native.Protocol = protocol
		for j := range req.Turns[i].Items {
			req.Turns[i].Items[j].Native.Protocol = protocol
			for k := range req.Turns[i].Items[j].Losses {
				req.Turns[i].Items[j].Losses[k].SourceProtocol = protocol
			}
		}
	}
	for i := range req.Tools {
		req.Tools[i].Native.Protocol = protocol
	}
}

func init() {
	registerRequestIRParser(FormatCodexResponses, parseCodexRequest)
	registerRequestIREmitter(FormatCodexResponses, emitCodexRequest)
	registerAdapterResponseParser(FormatCodexResponses, parseOpenAIResponsesResponse)
	registerAdapterResponseEmitter(FormatCodexResponses, emitOpenAIResponsesResponse)
	registerRequestIRParser(FormatClaudeCode, parseClaudeCodeRequest)
	registerRequestIREmitter(FormatClaudeCode, emitClaudeCodeRequest)
	registerAdapterResponseParser(FormatClaudeCode, parseAnthropicResponse)
	registerAdapterResponseEmitter(FormatClaudeCode, emitAnthropicResponse)
	registerRequestIRParser(FormatGeminiCode, parseGeminiCodeRequest)
	registerRequestIREmitter(FormatGeminiCode, emitGeminiCodeRequest)
	registerAdapterResponseParser(FormatGeminiCode, parseGeminiCLIResponse)
	registerAdapterResponseEmitter(FormatGeminiCode, emitGeminiCLIResponse)
	registerRequestIRParser(FormatAntigravity, parseAntigravityRequest)
	registerRequestIREmitter(FormatAntigravity, emitAntigravityRequest)
	registerAdapterResponseParser(FormatAntigravity, parseGeminiCLIResponse)
	registerAdapterResponseEmitter(FormatAntigravity, emitGeminiCLIResponse)
}
