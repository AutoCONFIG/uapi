package convert

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

// GeminiCLIToInternal converts Gemini CLI request to InternalRequest.
// Extracts the inner Gemini request from the CLI envelope.
func GeminiCLIToInternal(body []byte) (*InternalRequest, error) {
	var req schema.GeminiCLIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini CLI request: %w", err)
	}

	// Marshal the inner request and convert using Gemini converter
	innerBody, err := json.Marshal(req.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inner Gemini request: %w", err)
	}

	ir, err := GeminiToInternal(innerBody)
	if err != nil {
		return nil, fmt.Errorf("failed to convert inner Gemini request: %w", err)
	}

	// Override model from CLI envelope
	ir.Model = req.Model
	ir.SourceFormat = FormatGeminiCLI

	// Store CLI-specific fields in Extra for passthrough
	if req.Project != "" {
		ir.Extra["project"] = json.RawMessage(fmt.Sprintf(`%q`, req.Project))
	}
	if req.UserAgent != "" {
		ir.Extra["userAgent"] = json.RawMessage(fmt.Sprintf(`%q`, req.UserAgent))
	}
	if req.RequestType != "" {
		ir.Extra["requestType"] = json.RawMessage(fmt.Sprintf(`%q`, req.RequestType))
	}
	if req.RequestID != "" {
		ir.Extra["requestId"] = json.RawMessage(fmt.Sprintf(`%q`, req.RequestID))
	}
	if req.SessionID != "" {
		ir.Extra["sessionId"] = json.RawMessage(fmt.Sprintf(`%q`, req.SessionID))
	}

	return ir, nil
}

// InternalToGeminiCLI converts InternalRequest to Gemini CLI request.
// Wraps the Gemini request in the CLI envelope.
func InternalToGeminiCLI(ir *InternalRequest) ([]byte, error) {
	// First convert to Gemini format
	innerBody, err := InternalToGemini(ir)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to Gemini format: %w", err)
	}

	// Parse the Gemini request
	var geminiReq schema.GeminiRequest
	if err := json.Unmarshal(innerBody, &geminiReq); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Gemini request: %w", err)
	}

	// Build the CLI envelope
	cliReq := schema.GeminiCLIRequest{
		Model:     ir.Model,
		Request:   geminiReq,
		RequestID: generateRequestID(),
		SessionID: "",
	}

	// Add optional fields from Extra
	if v, ok := ir.Extra["project"]; ok {
		var project string
		json.Unmarshal(v, &project)
		cliReq.Project = project
	}
	if v, ok := ir.Extra["userAgent"]; ok {
		var userAgent string
		json.Unmarshal(v, &userAgent)
		cliReq.UserAgent = userAgent
	}
	if v, ok := ir.Extra["requestType"]; ok {
		var requestType string
		json.Unmarshal(v, &requestType)
		cliReq.RequestType = requestType
	}
	if v, ok := ir.Extra["sessionId"]; ok {
		var sessionID string
		json.Unmarshal(v, &sessionID)
		cliReq.SessionID = sessionID
	}

	return json.Marshal(cliReq)
}

// generateRequestID generates a simple request ID for Gemini CLI.
func generateRequestID() string {
	// Simple ID generation - in production would use UUID
	return fmt.Sprintf("req-%x", time.Now().UnixNano())
}

func init() {
	RegisterToInternal(FormatGeminiCLI, GeminiCLIToInternal)
	RegisterFromInternal(FormatGeminiCLI, InternalToGeminiCLI)
}