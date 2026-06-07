package openai

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

func TestGetRequestURLPassesThroughOpenAIMediaPaths(t *testing.T) {
	adaptor := &OpenAIAdaptor{}
	adaptor.Init(&db.Channel{Type: "openai", APIFormat: "standard", Endpoint: "https://api.openai.com/v1"}, &db.Account{})

	tests := map[string]string{
		"/v1/images/generations":   "https://api.openai.com/v1/images/generations",
		"/v1/audio/transcriptions": "https://api.openai.com/v1/audio/transcriptions",
		"/v1/embeddings":           "https://api.openai.com/v1/embeddings",
		"/v1/realtime/sessions":    "https://api.openai.com/v1/realtime/sessions",
		"/v1/videos":               "https://api.openai.com/v1/videos",
		"/v1/chat/completions":     "https://api.openai.com/v1/chat/completions",
	}

	for path, want := range tests {
		got, err := adaptor.GetRequestURL(path)
		if err != nil {
			t.Fatalf("GetRequestURL(%q): %v", path, err)
		}
		if got != want {
			t.Fatalf("GetRequestURL(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestGetRequestURLKeepsCodexBaseForTextButNotOpenAIPlatformMedia(t *testing.T) {
	adaptor := &OpenAIAdaptor{}
	adaptor.Init(&db.Channel{Type: "openai", APIFormat: "codex", Endpoint: "https://api.openai.com/v1"}, &db.Account{})

	got, err := adaptor.GetRequestURL("/v1/responses")
	if err != nil {
		t.Fatalf("GetRequestURL responses: %v", err)
	}
	if got != CodexAPIBaseURL+"/responses" {
		t.Fatalf("codex responses URL = %q", got)
	}

	got, err = adaptor.GetRequestURL("/v1/images/generations")
	if err != nil {
		t.Fatalf("GetRequestURL images: %v", err)
	}
	if got != CodexAPIBaseURL+"/images/generations" {
		t.Fatalf("codex images URL = %q", got)
	}
}

func TestCodexSetupRequestHeaderUsesNativeContract(t *testing.T) {
	adaptor := &OpenAIAdaptor{}
	adaptor.Init(&db.Channel{Type: "openai", APIFormat: "codex", Endpoint: "https://api.openai.com/v1"}, &db.Account{
		Metadata: map[string]interface{}{
			"chatgpt_account_id":         "acc_123",
			"chatgpt_account_is_fedramp": true,
		},
	})

	var req fasthttp.Request
	if err := adaptor.SetupRequestHeader(&req, "sk-test"); err != nil {
		t.Fatalf("SetupRequestHeader: %v", err)
	}
	wants := map[string]string{
		"Authorization":      "Bearer sk-test",
		"originator":         CodexOriginator,
		"User-Agent":         CodexUserAgent,
		"ChatGPT-Account-ID": "acc_123",
		"X-OpenAI-Fedramp":   "true",
		"Content-Type":       "application/json",
	}
	for header, want := range wants {
		if got := string(req.Header.Peek(header)); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestParseUsageFullNormalizesCacheHitAliases(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want int
	}{
		{
			name: "chat prompt details",
			body: []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}}`),
			want: 4,
		},
		{
			name: "responses input details",
			body: []byte(`{"usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":5}}}`),
			want: 5,
		},
		{
			name: "prompt cache hit alias",
			body: []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_cache_hit_tokens":6}}`),
			want: 6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adaptor := &OpenAIAdaptor{}
			usage, err := adaptor.ParseUsageFull(tc.body)
			if err != nil {
				t.Fatalf("ParseUsageFull: %v", err)
			}
			if usage.CacheReadInputTokens != tc.want {
				t.Fatalf("CacheReadInputTokens = %d, want %d", usage.CacheReadInputTokens, tc.want)
			}
		})
	}
}
