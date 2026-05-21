package relay

import (
	"encoding/json"

	ws "github.com/fasthttp/websocket"
)

// ── WebSocket event types ──────────────────────────────────────────────────────
//
// Codex CLI sends events in FLAT format:
//   {"type":"response.create","model":"gpt-4.1","input":[...],...}
// NOT the nested format {"type":"response.create","response":{...}}
//
// Server events from OpenAI Responses WS:
//   response.created, response.output_item.added, response.output_text.delta,
//   response.output_item.done, response.completed, response.failed,
//   response.incomplete, error

const (
	WSEventResponseCreate    = "response.create"
	WSEventResponseCreated   = "response.created"
	WSEventResponseDone      = "response.done"
	WSEventOutputItemAdded   = "response.output_item.added"
	WSEventContentPartAdded  = "response.content_part.added"
	WSEventTextDelta         = "response.output_text.delta"
	WSEventTextDone          = "response.output_text.done"
	WSEventContentPartDone   = "response.content_part.done"
	WSEventOutputItemDone    = "response.output_item.done"
	WSEventResponseCompleted = "response.completed"
	WSEventResponseFailed    = "response.failed"
	WSEventResponseIncomp    = "response.incomplete"
	WSEventError             = "error"
)

// IsTerminalEvent returns true for events that mark the end of a response turn.
// The Responses WS API uses response.completed, response.failed, response.incomplete
// as terminal events. Note: response.done is NOT used by the Responses WS —
// it belongs to the Realtime API.
func IsTerminalEvent(eventType string) bool {
	switch eventType {
	case WSEventResponseCompleted, WSEventResponseFailed, WSEventResponseIncomp:
		return true
	}
	return false
}

// ── Event structures ───────────────────────────────────────────────────────────

// WSEventEnvelope is the outer wrapper for all WebSocket events.
// The Responses WS protocol uses flat top-level fields, not a nested "response" key.
type WSEventEnvelope struct {
	Type    string `json:"type"`
	EventID string `json:"event_id,omitempty"`
}

// WSResponseCreateEvent represents a response.create request from the client.
// Format is FLAT: {"type":"response.create","model":"...","input":[...],...}
// NOT: {"type":"response.create","response":{"model":"..."}}
type WSResponseCreateEvent struct {
	Type               string          `json:"type"`
	EventID            string          `json:"event_id,omitempty"`
	Model              string          `json:"model"`
	Store              *bool           `json:"store,omitempty"`
	Input              json.RawMessage `json:"input,omitempty"`
	Instructions       string          `json:"instructions,omitempty"`
	MaxOutputTokens    int             `json:"max_output_tokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	Tools              json.RawMessage `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
}

// WSUsage represents token usage from a response event.
// OpenAI Responses API uses input_tokens/output_tokens (NOT prompt_tokens/completion_tokens).
type WSUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ParseResponsesUsage extracts usage from a response.completed or response.failed event.
// The event format is: {"type":"response.completed","response":{"usage":{"input_tokens":N,"output_tokens":M},...}}
func ParseResponsesUsage(data []byte) (inputTokens, outputTokens int) {
	var wrapper struct {
		Response struct {
			Usage WSUsage `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return 0, 0
	}
	return wrapper.Response.Usage.InputTokens, wrapper.Response.Usage.OutputTokens
}

// ParseModelFromCreateEvent extracts the model name from a response.create event.
// Supports both flat format ({"type":"response.create","model":"..."})
// and nested format ({"type":"response.create","response":{"model":"..."}}).
func ParseModelFromCreateEvent(msg []byte) string {
	// Try flat format first (Codex CLI format)
	var flat WSResponseCreateEvent
	if err := json.Unmarshal(msg, &flat); err == nil && flat.Model != "" {
		return flat.Model
	}
	// Try nested format (Realtime API format)
	var nested struct {
		Response struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if err := json.Unmarshal(msg, &nested); err == nil && nested.Response.Model != "" {
		return nested.Response.Model
	}
	return ""
}

// WriteWSError sends a standard error event to the client.
// Format matches OpenAI: {"type":"error","status":400,"error":{"code":"...","message":"..."}}
func WriteWSError(conn *ws.Conn, status int, code, message string) error {
	ev := struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Type:   WSEventError,
		Status: status,
	}
	ev.Error.Code = code
	ev.Error.Message = message
	data, _ := json.Marshal(ev)
	return conn.WriteMessage(ws.TextMessage, data)
}

