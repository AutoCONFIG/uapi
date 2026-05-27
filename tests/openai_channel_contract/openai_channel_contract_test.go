package openai_channel_contract_test

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
)

func TestCodexOAuthUsesChatGPTBackendWhenConfiguredAsOpenAIPlatform(t *testing.T) {
	adaptor := &openai.OpenAIAdaptor{}
	adaptor.Init(&db.Channel{APIFormat: "codex", Endpoint: "https://api.openai.com/v1"}, &db.Account{})

	got, err := adaptor.GetRequestURL("/v1/chat/completions")
	if err != nil {
		t.Fatalf("GetRequestURL: %v", err)
	}
	want := "https://chatgpt.com/backend-api/codex/responses"
	if got != want {
		t.Fatalf("codex request url = %q, want %q", got, want)
	}
}

func TestOpenAIResponsesKeepsOpenAIPlatformEndpoint(t *testing.T) {
	adaptor := &openai.OpenAIAdaptor{}
	adaptor.Init(&db.Channel{APIFormat: "responses", Endpoint: "https://api.openai.com/v1"}, &db.Account{})

	got, err := adaptor.GetRequestURL("/v1/chat/completions")
	if err != nil {
		t.Fatalf("GetRequestURL: %v", err)
	}
	want := "https://api.openai.com/v1/responses"
	if got != want {
		t.Fatalf("responses request url = %q, want %q", got, want)
	}
}

func TestDefaultEndpointSeparatesCodexOAuthFromOpenAIAPIKey(t *testing.T) {
	if got := upstreamconfig.DefaultEndpoint("openai", "codex"); got != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("codex endpoint = %q", got)
	}
	if got := upstreamconfig.DefaultEndpoint("openai", "responses"); got != "https://api.openai.com/v1" {
		t.Fatalf("openai responses endpoint = %q", got)
	}
	if got := upstreamconfig.DefaultEndpoint("openai", "standard"); got != "https://api.openai.com/v1" {
		t.Fatalf("openai standard endpoint = %q", got)
	}
}
