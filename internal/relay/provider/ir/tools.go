package ir

import "encoding/json"

type ToolKind string

const (
	ToolFunction        ToolKind = "function"
	ToolCustom          ToolKind = "custom"
	ToolServer          ToolKind = "server"
	ToolComputer        ToolKind = "computer"
	ToolWebSearch       ToolKind = "web_search"
	ToolWebFetch        ToolKind = "web_fetch"
	ToolFileSearch      ToolKind = "file_search"
	ToolCodeInterpreter ToolKind = "code_interpreter"
	ToolMCP             ToolKind = "mcp"
	ToolLocalShell      ToolKind = "local_shell"
	ToolNamespace       ToolKind = "namespace"
	ToolOpaque          ToolKind = "opaque"
)

type Tool struct {
	Kind        ToolKind                   `json:"kind"`
	Name        string                     `json:"name,omitempty"`
	Namespace   string                     `json:"namespace,omitempty"`
	Description string                     `json:"description,omitempty"`
	InputSchema json.RawMessage            `json:"input_schema,omitempty"`
	Parameters  json.RawMessage            `json:"parameters,omitempty"`
	Metadata    map[string]json.RawMessage `json:"metadata,omitempty"`
	Native      NativeEnvelope             `json:"native,omitempty"`
}

type ToolChoice struct {
	Mode      string          `json:"mode,omitempty"`
	Name      string          `json:"name,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

type ToolUse struct {
	ID            string                     `json:"id,omitempty"`
	CallID        string                     `json:"call_id,omitempty"`
	Name          string                     `json:"name,omitempty"`
	Namespace     string                     `json:"namespace,omitempty"`
	Arguments     json.RawMessage            `json:"arguments,omitempty"`
	ArgumentsText string                     `json:"arguments_text,omitempty"`
	Action        json.RawMessage            `json:"action,omitempty"`
	Metadata      map[string]json.RawMessage `json:"metadata,omitempty"`
}

type ToolResult struct {
	ToolUseID  string                     `json:"tool_use_id,omitempty"`
	CallID     string                     `json:"call_id,omitempty"`
	OutputText string                     `json:"output_text,omitempty"`
	Output     []Item                     `json:"output,omitempty"`
	IsError    bool                       `json:"is_error,omitempty"`
	Action     json.RawMessage            `json:"action,omitempty"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
}
