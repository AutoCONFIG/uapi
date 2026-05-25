package antigravity

import (
	"strings"
	"testing"
)

func TestAntigravityUserAgentUsesCurrentFallback(t *testing.T) {
	ua := AntigravityUserAgent()
	if ua != "antigravity/2.0.1 darwin/arm64" {
		t.Fatalf("AntigravityUserAgent() = %q", ua)
	}
	if got := LoadCodeAssistUserAgent(); !strings.HasPrefix(got, ua+" ") || !strings.Contains(got, NodeAPIClientUA) {
		t.Fatalf("LoadCodeAssistUserAgent() = %q, want %q plus node client", got, ua)
	}
}
