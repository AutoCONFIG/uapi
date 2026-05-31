package convert

import (
	"encoding/json"
	"fmt"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func parseCodexRequest(body []byte) (*relayir.Request, error) {
	out, err := parseOpenAIResponsesRequestDirectIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeRequestProtocol(out, relayir.ProtocolCodex)
	return out, nil
}

func parseCodexResponse(body []byte) (*relayir.Response, error) {
	resp, err := parseOpenAIResponsesResponseIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeResponseProtocol(resp, relayir.ProtocolCodex)
	return resp, nil
}

func emitCodexRequest(req *relayir.Request) ([]byte, error) {
	return emitOpenAIResponsesRequestDirectIR(req)
}

func emitCodexResponse(resp *relayir.Response) ([]byte, error) {
	return emitOpenAIResponsesResponseIR(resp)
}

func parseClaudeCodeRequest(body []byte) (*relayir.Request, error) {
	out, err := parseAnthropicRequestDirectIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeRequestProtocol(out, relayir.ProtocolClaudeCode)
	return out, nil
}

func parseClaudeCodeResponse(body []byte) (*relayir.Response, error) {
	resp, err := parseAnthropicResponseIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeResponseProtocol(resp, relayir.ProtocolClaudeCode)
	return resp, nil
}

func emitClaudeCodeRequest(req *relayir.Request) ([]byte, error) {
	return emitAnthropicRequestDirectIR(req)
}

func emitClaudeCodeResponse(resp *relayir.Response) ([]byte, error) {
	return emitAnthropicResponseIR(resp)
}

func parseGeminiCodeRequest(body []byte) (*relayir.Request, error) {
	out, err := parseGeminiCLIRequestDirectIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeRequestProtocol(out, relayir.ProtocolGeminiCode)
	return out, nil
}

func parseGeminiCodeResponse(body []byte) (*relayir.Response, error) {
	resp, err := parseGeminiCLIResponseIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeResponseProtocol(resp, relayir.ProtocolGeminiCode)
	return resp, nil
}

func emitGeminiCodeRequest(req *relayir.Request) ([]byte, error) {
	return emitGeminiCLIRequestDirectIR(req)
}

func emitGeminiCodeResponse(resp *relayir.Response) ([]byte, error) {
	return emitGeminiCLIResponseIR(resp)
}

func parseAntigravityRequest(body []byte) (*relayir.Request, error) {
	out, err := parseGeminiCLIRequestDirectIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeRequestProtocol(out, relayir.ProtocolAntigravity)
	return out, nil
}

func parseAntigravityResponse(body []byte) (*relayir.Response, error) {
	resp, err := parseGeminiCLIResponseIR(body)
	if err != nil {
		return nil, err
	}
	normalizeNativeResponseProtocol(resp, relayir.ProtocolAntigravity)
	return resp, nil
}

func emitAntigravityRequest(req *relayir.Request) ([]byte, error) {
	body, err := emitGeminiCLIRequestDirectIR(req)
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

func emitAntigravityResponse(resp *relayir.Response) ([]byte, error) {
	return emitGeminiCLIResponseIR(resp)
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

func normalizeNativeResponseProtocol(resp *relayir.Response, protocol relayir.Protocol) {
	if resp == nil {
		return
	}
	resp.SourceProtocol = protocol
	resp.Native.Protocol = protocol
	for i := range resp.Losses {
		resp.Losses[i].SourceProtocol = protocol
	}
	for i := range resp.Choices {
		resp.Choices[i].Native.Protocol = protocol
		for j := range resp.Choices[i].Items {
			resp.Choices[i].Items[j].Native.Protocol = protocol
			for k := range resp.Choices[i].Items[j].Losses {
				resp.Choices[i].Items[j].Losses[k].SourceProtocol = protocol
			}
		}
		for j := range resp.Choices[i].Losses {
			resp.Choices[i].Losses[j].SourceProtocol = protocol
		}
	}
}

func init() {
	registerRequestIRParser(FormatCodexResponses, parseCodexRequest)
	registerRequestIREmitter(FormatCodexResponses, emitCodexRequest)
	registerResponseIRParser(FormatCodexResponses, parseCodexResponse)
	registerResponseIREmitter(FormatCodexResponses, emitCodexResponse)
	registerRequestIRParser(FormatClaudeCode, parseClaudeCodeRequest)
	registerRequestIREmitter(FormatClaudeCode, emitClaudeCodeRequest)
	registerResponseIRParser(FormatClaudeCode, parseClaudeCodeResponse)
	registerResponseIREmitter(FormatClaudeCode, emitClaudeCodeResponse)
	registerRequestIRParser(FormatGeminiCode, parseGeminiCodeRequest)
	registerRequestIREmitter(FormatGeminiCode, emitGeminiCodeRequest)
	registerResponseIRParser(FormatGeminiCode, parseGeminiCodeResponse)
	registerResponseIREmitter(FormatGeminiCode, emitGeminiCodeResponse)
	registerRequestIRParser(FormatAntigravity, parseAntigravityRequest)
	registerRequestIREmitter(FormatAntigravity, emitAntigravityRequest)
	registerResponseIRParser(FormatAntigravity, parseAntigravityResponse)
	registerResponseIREmitter(FormatAntigravity, emitAntigravityResponse)
}
