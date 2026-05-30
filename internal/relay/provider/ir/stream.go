package ir

type EventType string

const (
	EventUnknown          EventType = "unknown"
	EventResponseCreated  EventType = "response_created"
	EventMessageStart     EventType = "message_start"
	EventContentPartStart EventType = "content_part_start"
	EventContentDelta     EventType = "content_delta"
	EventContentPartEnd   EventType = "content_part_end"
	EventToolCallStart    EventType = "tool_call_start"
	EventToolArgDelta     EventType = "tool_argument_delta"
	EventToolCallEnd      EventType = "tool_call_end"
	EventReasoningStart   EventType = "reasoning_start"
	EventReasoningDelta   EventType = "reasoning_delta"
	EventReasoningEnd     EventType = "reasoning_end"
	EventItemStart        EventType = "item_start"
	EventDelta            EventType = "delta"
	EventItemDone         EventType = "item_done"
	EventUsage            EventType = "usage"
	EventSafetyBlock      EventType = "safety_block"
	EventError            EventType = "error"
	EventMessageDone      EventType = "message_done"
	EventResponseDone     EventType = "response_done"
	EventDone             EventType = "done"
)

type StreamEvent struct {
	Type        EventType      `json:"type"`
	ResponseID  string         `json:"response_id,omitempty"`
	Model       string         `json:"model,omitempty"`
	ChoiceIndex int            `json:"choice_index,omitempty"`
	ItemIndex   int            `json:"item_index,omitempty"`
	Delta       ItemDelta      `json:"delta,omitempty"`
	Usage       *Usage         `json:"usage,omitempty"`
	Finish      *Finish        `json:"finish,omitempty"`
	Error       *Error         `json:"error,omitempty"`
	Losses      []Loss         `json:"losses,omitempty"`
	Native      NativeEnvelope `json:"native,omitempty"`
}

type ItemDelta struct {
	Kind       ItemKind `json:"kind,omitempty"`
	Text       string   `json:"text,omitempty"`
	Arguments  string   `json:"arguments,omitempty"`
	OutputText string   `json:"output_text,omitempty"`
	Signature  string   `json:"signature,omitempty"`
}
