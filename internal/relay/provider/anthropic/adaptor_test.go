package anthropic

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
)

func TestGetRequestURLAlwaysUsesMessagesEndpoint(t *testing.T) {
	a := &AnthropicAdaptor{}
	a.Init(&db.Channel{Endpoint: "https://api.anthropic.com"}, nil)

	for _, path := range []string{
		"/v1/chat/completions",
		"/v1/responses",
		"/v1beta/models/test:generateContent",
		"/v1/messages",
	} {
		got, err := a.GetRequestURL(path)
		if err != nil {
			t.Fatalf("GetRequestURL(%q): %v", path, err)
		}
		if got != "https://api.anthropic.com/v1/messages" {
			t.Fatalf("GetRequestURL(%q) = %q", path, got)
		}
	}
}
