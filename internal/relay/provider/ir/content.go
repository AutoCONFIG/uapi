package ir

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleFunction  Role = "function"
	RoleModel     Role = "model"
	RoleUnknown   Role = "unknown"
	RoleOpaque    Role = "opaque"
)

type ItemKind string

const (
	ItemText                ItemKind = "text"
	ItemImage               ItemKind = "image"
	ItemFile                ItemKind = "file"
	ItemDocument            ItemKind = "document"
	ItemAudio               ItemKind = "audio"
	ItemVideo               ItemKind = "video"
	ItemToolUse             ItemKind = "tool_use"
	ItemToolResult          ItemKind = "tool_result"
	ItemFunctionCall        ItemKind = "function_call"
	ItemFunctionCallOutput  ItemKind = "function_call_output"
	ItemReasoning           ItemKind = "reasoning"
	ItemThinking            ItemKind = "thinking"
	ItemRedactedThinking    ItemKind = "redacted_thinking"
	ItemEncryptedReasoning  ItemKind = "encrypted_reasoning"
	ItemRefusal             ItemKind = "refusal"
	ItemCitation            ItemKind = "citation"
	ItemWebSearchResult     ItemKind = "web_search_result"
	ItemExecutableCode      ItemKind = "executable_code"
	ItemCodeExecutionResult ItemKind = "code_execution_result"
	ItemRenderedContent     ItemKind = "rendered_content"
	ItemSearchContent       ItemKind = "search_content"
	ItemCacheMarker         ItemKind = "cache_marker"
	ItemSafetyBlock         ItemKind = "safety_block"
	ItemOpaque              ItemKind = "opaque"
)

type Instruction struct {
	Role     Role                       `json:"role,omitempty"`
	Text     string                     `json:"text,omitempty"`
	Items    []Item                     `json:"items,omitempty"`
	Name     string                     `json:"name,omitempty"`
	ID       string                     `json:"id,omitempty"`
	Metadata map[string]json.RawMessage `json:"metadata,omitempty"`
	Native   NativeEnvelope             `json:"native,omitempty"`
}

type Turn struct {
	Role     Role                       `json:"role,omitempty"`
	Items    []Item                     `json:"items,omitempty"`
	Name     string                     `json:"name,omitempty"`
	ID       string                     `json:"id,omitempty"`
	Status   string                     `json:"status,omitempty"`
	Phase    string                     `json:"phase,omitempty"`
	Metadata map[string]json.RawMessage `json:"metadata,omitempty"`
	Native   NativeEnvelope             `json:"native,omitempty"`
}

type Item struct {
	ID                  string                     `json:"id,omitempty"`
	CallID              string                     `json:"call_id,omitempty"`
	Name                string                     `json:"name,omitempty"`
	OriginalIndex       int                        `json:"original_index,omitempty"`
	Kind                ItemKind                   `json:"kind"`
	Text                *Text                      `json:"text,omitempty"`
	Image               *Image                     `json:"image,omitempty"`
	File                *File                      `json:"file,omitempty"`
	Document            *File                      `json:"document,omitempty"`
	Audio               *Audio                     `json:"audio,omitempty"`
	Video               *File                      `json:"video,omitempty"`
	ToolUse             *ToolUse                   `json:"tool_use,omitempty"`
	ToolResult          *ToolResult                `json:"tool_result,omitempty"`
	Reasoning           *Reasoning                 `json:"reasoning,omitempty"`
	Refusal             *Refusal                   `json:"refusal,omitempty"`
	Citation            *Citation                  `json:"citation,omitempty"`
	WebSearchResult     *WebSearchResult           `json:"web_search_result,omitempty"`
	ExecutableCode      *ExecutableCode            `json:"executable_code,omitempty"`
	CodeExecutionResult *CodeExecutionResult       `json:"code_execution_result,omitempty"`
	CacheMarker         *CacheMarker               `json:"cache_marker,omitempty"`
	SafetyBlock         *SafetyBlock               `json:"safety_block,omitempty"`
	Opaque              *Opaque                    `json:"opaque,omitempty"`
	Metadata            map[string]json.RawMessage `json:"metadata,omitempty"`
	Native              NativeEnvelope             `json:"native,omitempty"`
	Losses              []Loss                     `json:"losses,omitempty"`
}

type Text struct {
	Text string `json:"text"`
}

type Image struct {
	URL      string `json:"url,omitempty"`
	DataURI  string `json:"data_uri,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type File struct {
	URL      string `json:"url,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	DataURI  string `json:"data_uri,omitempty"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type Audio struct {
	DataURI  string `json:"data_uri,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Format   string `json:"format,omitempty"`
}

type Reasoning struct {
	Text             string          `json:"text,omitempty"`
	Summary          []Text          `json:"summary,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	RedactedContent  string          `json:"redacted_content,omitempty"`
	Signature        string          `json:"signature,omitempty"`
	ThoughtSignature string          `json:"thought_signature,omitempty"`
	Details          json.RawMessage `json:"details,omitempty"`
}

type Refusal struct {
	Text string `json:"text,omitempty"`
}

type ExecutableCode struct {
	Language string `json:"language,omitempty"`
	Code     string `json:"code,omitempty"`
}

type CodeExecutionResult struct {
	Outcome string `json:"outcome,omitempty"`
	Output  string `json:"output,omitempty"`
}

type Opaque struct {
	Type string          `json:"type,omitempty"`
	Raw  json.RawMessage `json:"raw,omitempty"`
	Text string          `json:"text,omitempty"`
}

type Citation struct {
	URL        string          `json:"url,omitempty"`
	Title      string          `json:"title,omitempty"`
	StartIndex int             `json:"start_index,omitempty"`
	EndIndex   int             `json:"end_index,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

type WebSearchResult struct {
	Query   string          `json:"query,omitempty"`
	URL     string          `json:"url,omitempty"`
	Title   string          `json:"title,omitempty"`
	Snippet string          `json:"snippet,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

type CacheMarker struct {
	Type  string          `json:"type,omitempty"`
	TTL   string          `json:"ttl,omitempty"`
	Scope string          `json:"scope,omitempty"`
	Raw   json.RawMessage `json:"raw,omitempty"`
}

type SafetyBlock struct {
	Reason   string          `json:"reason,omitempty"`
	Category string          `json:"category,omitempty"`
	Message  string          `json:"message,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}
