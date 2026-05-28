package openai

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
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
