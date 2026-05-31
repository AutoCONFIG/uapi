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

func TestParseUsageFullCapturesGeminiCachedContent(t *testing.T) {
	adaptor := &AntigravityAdaptor{}
	usage, err := adaptor.ParseUsageFull([]byte(`{"usageMetadata":{"promptTokenCount":22,"candidatesTokenCount":4,"cachedContentTokenCount":9}}`))
	if err != nil {
		t.Fatalf("ParseUsageFull: %v", err)
	}
	if usage.PromptTokens != 22 || usage.CompletionTokens != 4 || usage.CacheReadInputTokens != 9 {
		t.Fatalf("usage = %#v, want prompt=22 completion=4 cache_read=9", usage)
	}
}

func TestParseUsageFullCapturesAntigravityEnvelopeCachedContent(t *testing.T) {
	adaptor := &AntigravityAdaptor{}
	usage, err := adaptor.ParseUsageFull([]byte(`{"response":{"usageMetadata":{"promptTokenCount":31,"candidatesTokenCount":6,"cachedContentTokenCount":12}}}`))
	if err != nil {
		t.Fatalf("ParseUsageFull: %v", err)
	}
	if usage.PromptTokens != 31 || usage.CompletionTokens != 6 || usage.CacheReadInputTokens != 12 {
		t.Fatalf("usage = %#v, want prompt=31 completion=6 cache_read=12", usage)
	}
}
