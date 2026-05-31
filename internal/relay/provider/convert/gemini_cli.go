package convert

import (
	"encoding/json"
	"fmt"
	"time"

	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func parseGeminiCLIRequestDirectIR(body []byte) (*relayir.Request, error) {
	var req schema.GeminiCLIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini CLI request: %w", err)
	}
	innerBody, err := json.Marshal(req.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inner Gemini request: %w", err)
	}
	out, err := parseGeminiRequestDirectIR(innerBody)
	if err != nil {
		return nil, fmt.Errorf("failed to convert inner Gemini request: %w", err)
	}
	out.SourceProtocol = relayir.ProtocolGeminiCLI
	out.Native.Protocol = relayir.ProtocolGeminiCLI
	out.Native.RawBody = relayir.CloneRaw(body)
	out.Model = req.Model
	if out.Metadata == nil {
		out.Metadata = map[string]json.RawMessage{}
	}
	setEnvelopeRaw := func(key, value string) {
		if value != "" {
			out.Metadata[key] = rawJSON(value)
		}
	}
	setEnvelopeRaw("project", req.Project)
	setEnvelopeRaw("user_prompt_id", req.UserPromptID)
	setEnvelopeRaw("userAgent", req.UserAgent)
	setEnvelopeRaw("requestType", req.RequestType)
	setEnvelopeRaw("requestId", req.RequestID)
	setEnvelopeRaw("sessionId", req.SessionID)
	if len(req.EnabledCreditTypes) > 0 {
		out.Metadata["enabled_credit_types"] = rawJSON(req.EnabledCreditTypes)
	}
	out.Native.Fields = relayir.CloneRawMap(out.Metadata)
	return out, nil
}

func emitGeminiCLIRequestDirectIR(req *relayir.Request) ([]byte, error) {
	innerBody, err := emitGeminiRequestDirectIR(req)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to Gemini format: %w", err)
	}
	var geminiReq schema.GeminiRequest
	if err := json.Unmarshal(innerBody, &geminiReq); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini request: %w", err)
	}
	cliReq := schema.GeminiCLIRequest{Model: req.Model, Request: geminiReq, RequestID: generateRequestID()}
	read := func(key string) string { return rawString(req.Metadata[key]) }
	cliReq.Project = read("project")
	cliReq.UserPromptID = read("user_prompt_id")
	cliReq.UserAgent = read("userAgent")
	cliReq.RequestType = read("requestType")
	cliReq.SessionID = read("sessionId")
	if raw := req.Metadata["enabled_credit_types"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &cliReq.EnabledCreditTypes)
	}
	return json.Marshal(cliReq)
}

// generateRequestID generates a simple request ID for Gemini CLI.
func generateRequestID() string {
	// Simple ID generation - in production would use UUID
	return fmt.Sprintf("req-%x", time.Now().UnixNano())
}

func init() {
	registerRequestIRParser(FormatGeminiCLI, parseGeminiCLIRequestIR)
	registerRequestIREmitter(FormatGeminiCLI, emitGeminiCLIRequestIR)
}
