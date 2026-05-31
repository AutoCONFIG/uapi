package convert

import (
	"encoding/json"
	"fmt"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func parseCodexRequest(body []byte) (*relayir.Request, error) {
	req, err := ParseOpenAIResponsesRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatCodexResponses
	out := req.ToIR()
	out.SourceProtocol = relayir.ProtocolCodex
	out.Native.Protocol = relayir.ProtocolCodex
	return out, nil
}

func emitCodexRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatCodexResponses
	return EmitOpenAIResponsesRequest(internal)
}

func parseClaudeCodeRequest(body []byte) (*relayir.Request, error) {
	req, err := ParseAnthropicRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatClaudeCode
	out := req.ToIR()
	out.SourceProtocol = relayir.ProtocolClaudeCode
	out.Native.Protocol = relayir.ProtocolClaudeCode
	return out, nil
}

func emitClaudeCodeRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatClaudeCode
	return EmitAnthropicRequest(internal)
}

func parseGeminiCodeRequest(body []byte) (*relayir.Request, error) {
	req, err := ParseGeminiCLIRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatGeminiCode
	out := req.ToIR()
	out.SourceProtocol = relayir.ProtocolGeminiCode
	out.Native.Protocol = relayir.ProtocolGeminiCode
	return out, nil
}

func emitGeminiCodeRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatGeminiCode
	return EmitGeminiCLIRequest(internal)
}

func parseAntigravityRequest(body []byte) (*relayir.Request, error) {
	req, err := ParseGeminiCLIRequest(body)
	if err != nil {
		return nil, err
	}
	req.SourceFormat = FormatAntigravity
	out := req.ToIR()
	out.SourceProtocol = relayir.ProtocolAntigravity
	out.Native.Protocol = relayir.ProtocolAntigravity
	return out, nil
}

func emitAntigravityRequest(req *relayir.Request) ([]byte, error) {
	internal := adapterRequestFromIR(req)
	internal.SourceFormat = FormatAntigravity
	body, err := EmitGeminiCLIRequest(internal)
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

func init() {
	RegisterRequestIRParser(FormatCodexResponses, parseCodexRequest)
	RegisterRequestIREmitter(FormatCodexResponses, emitCodexRequest)
	RegisterResponseParser(FormatCodexResponses, ParseOpenAIResponsesResponse)
	RegisterResponseEmitter(FormatCodexResponses, EmitOpenAIResponsesResponse)
	RegisterRequestIRParser(FormatClaudeCode, parseClaudeCodeRequest)
	RegisterRequestIREmitter(FormatClaudeCode, emitClaudeCodeRequest)
	RegisterResponseParser(FormatClaudeCode, ParseAnthropicResponse)
	RegisterResponseEmitter(FormatClaudeCode, EmitAnthropicResponse)
	RegisterRequestIRParser(FormatGeminiCode, parseGeminiCodeRequest)
	RegisterRequestIREmitter(FormatGeminiCode, emitGeminiCodeRequest)
	RegisterResponseParser(FormatGeminiCode, ParseGeminiCLIResponse)
	RegisterResponseEmitter(FormatGeminiCode, EmitGeminiCLIResponse)
	RegisterRequestIRParser(FormatAntigravity, parseAntigravityRequest)
	RegisterRequestIREmitter(FormatAntigravity, emitAntigravityRequest)
	RegisterResponseParser(FormatAntigravity, ParseGeminiCLIResponse)
	RegisterResponseEmitter(FormatAntigravity, EmitGeminiCLIResponse)
}
