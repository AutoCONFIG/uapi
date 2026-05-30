package ir

import "encoding/json"

type Request struct {
	SourceProtocol Protocol                   `json:"source_protocol,omitempty"`
	TargetProtocol Protocol                   `json:"target_protocol,omitempty"`
	Model          string                     `json:"model,omitempty"`
	RoutedProvider string                     `json:"routed_provider,omitempty"`
	RoutedChannel  string                     `json:"routed_channel,omitempty"`
	RoutedModel    string                     `json:"routed_model,omitempty"`
	Stream         bool                       `json:"stream,omitempty"`
	Instructions   []Instruction              `json:"instructions,omitempty"`
	Turns          []Turn                     `json:"turns,omitempty"`
	Tools          []Tool                     `json:"tools,omitempty"`
	ToolChoice     *ToolChoice                `json:"tool_choice,omitempty"`
	Generation     GenerationConfig           `json:"generation,omitempty"`
	Safety         SafetyConfig               `json:"safety,omitempty"`
	Cache          CacheConfig                `json:"cache,omitempty"`
	Usage          *Usage                     `json:"usage,omitempty"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
	Native         NativeEnvelope             `json:"native,omitempty"`
	Losses         []Loss                     `json:"losses,omitempty"`
}

type GenerationConfig struct {
	MaxTokens         *int                       `json:"max_tokens,omitempty"`
	MaxTokensField    string                     `json:"max_tokens_field,omitempty"`
	Temperature       *float64                   `json:"temperature,omitempty"`
	TopP              *float64                   `json:"top_p,omitempty"`
	TopK              *int                       `json:"top_k,omitempty"`
	Stop              []string                   `json:"stop,omitempty"`
	N                 *int                       `json:"n,omitempty"`
	CandidateCount    *int                       `json:"candidate_count,omitempty"`
	Seed              *int64                     `json:"seed,omitempty"`
	LogProbs          *bool                      `json:"logprobs,omitempty"`
	TopLogProbs       *int                       `json:"top_logprobs,omitempty"`
	FrequencyPenalty  *float64                   `json:"frequency_penalty,omitempty"`
	PresencePenalty   *float64                   `json:"presence_penalty,omitempty"`
	ResponseFormat    json.RawMessage            `json:"response_format,omitempty"`
	Reasoning         json.RawMessage            `json:"reasoning,omitempty"`
	Thinking          json.RawMessage            `json:"thinking,omitempty"`
	ServiceTier       string                     `json:"service_tier,omitempty"`
	Store             *bool                      `json:"store,omitempty"`
	User              string                     `json:"user,omitempty"`
	ParallelToolCalls *bool                      `json:"parallel_tool_calls,omitempty"`
	Extra             map[string]json.RawMessage `json:"extra,omitempty"`
}

type SafetyConfig struct {
	Settings json.RawMessage            `json:"settings,omitempty"`
	Extra    map[string]json.RawMessage `json:"extra,omitempty"`
}

type CacheConfig struct {
	Enabled       *bool                      `json:"enabled,omitempty"`
	Strategy      string                     `json:"strategy,omitempty"`
	TTL           string                     `json:"ttl,omitempty"`
	CacheControl  json.RawMessage            `json:"cache_control,omitempty"`
	CachedContent string                     `json:"cached_content,omitempty"`
	Extra         map[string]json.RawMessage `json:"extra,omitempty"`
}

type Finish struct {
	Reason           FinishReason   `json:"reason,omitempty"`
	NativeReason     string         `json:"native_reason,omitempty"`
	StopSequence     string         `json:"stop_sequence,omitempty"`
	Status           string         `json:"status,omitempty"`
	IncompleteReason string         `json:"incomplete_reason,omitempty"`
	Error            *Error         `json:"error,omitempty"`
	Native           NativeEnvelope `json:"native,omitempty"`
}

type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishMaxTokens     FinishReason = "max_tokens"
	FinishToolCall      FinishReason = "tool_call"
	FinishSafety        FinishReason = "safety"
	FinishContentFilter FinishReason = "content_filter"
	FinishError         FinishReason = "error"
	FinishInterrupted   FinishReason = "interrupted"
	FinishUnknown       FinishReason = "unknown"
)

type Error struct {
	Code    string         `json:"code,omitempty"`
	Type    string         `json:"type,omitempty"`
	Message string         `json:"message,omitempty"`
	Native  NativeEnvelope `json:"native,omitempty"`
}
