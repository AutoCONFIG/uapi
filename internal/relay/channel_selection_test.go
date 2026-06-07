package relay

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

func TestChannelCacheInvalidateClearsSnapshot(t *testing.T) {
	cache := &channelCache{
		channels: []db.Channel{{Name: "cached"}},
		expiry:   time.Now().Add(time.Hour),
	}

	cache.invalidate()

	if cache.channels != nil {
		t.Fatalf("expected channels to be cleared, got %d", len(cache.channels))
	}
	if !cache.expiry.IsZero() {
		t.Fatalf("expected expiry to be zero, got %s", cache.expiry)
	}
}

func TestChannelForceStreamForModelUsesConfiguredModelsOnly(t *testing.T) {
	ch := db.Channel{
		Settings: `{"force_stream_models":["gpt-5.5","upstream-only"]}`,
	}
	if !channelForceStreamForModel(&ch, "gpt-5.5", "gpt-5.5") {
		t.Fatalf("expected public model to enable force stream")
	}
	if !channelForceStreamForModel(&ch, "public-alias", "upstream-only") {
		t.Fatalf("expected upstream model to enable force stream")
	}
	if channelForceStreamForModel(&ch, "gpt-4.1", "gpt-4.1") {
		t.Fatalf("unexpected force stream for unconfigured model")
	}
	legacy := db.Channel{ForceStream: true}
	if channelForceStreamForModel(&legacy, "gpt-4.1", "gpt-4.1") {
		t.Fatalf("legacy channel flag must not enable force stream without model config")
	}
}

func TestChannelCandidatesForModelWeightsWithinPriorityBuckets(t *testing.T) {
	channels := []db.Channel{
		{Name: "high-a", Type: "openai", APIFormat: "standard", Models: "gpt-4", Priority: 10, Weight: 1},
		{Name: "high-b", Type: "openai", APIFormat: "standard", Models: "gpt-4", Priority: 10, Weight: 100},
		{Name: "low-a", Type: "openai", APIFormat: "standard", Models: "gpt-4", Priority: 5, Weight: 1},
		{Name: "low-b", Type: "openai", APIFormat: "standard", Models: "gpt-4", Priority: 5, Weight: 100},
		{Name: "unsupported", Type: "openai", APIFormat: "standard", Models: "other", Priority: 5},
	}
	randomInt := func(n int) int { return n - 1 }

	got := channelCandidatesForModel(channels, "gpt-4", randomInt)
	wantNames := []string{"high-b", "high-a", "low-b", "low-a"}
	if len(got) != len(wantNames) {
		t.Fatalf("candidate count = %d, want %d", len(got), len(wantNames))
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Fatalf("candidate[%d] = %q, want %q; full result: %#v", i, got[i].Name, want, got)
		}
	}
	if got[0].Priority != 10 || got[1].Priority != 10 || got[2].Priority != 5 || got[3].Priority != 5 {
		t.Fatalf("priority order was not preserved: %#v", got)
	}
}

func TestChannelCandidatesForModelSortsPriorityBeforeWeight(t *testing.T) {
	channels := []db.Channel{
		{Name: "low-heavy", Type: "openai", APIFormat: "standard", Models: "gpt-4", Priority: 5, Weight: 1000},
		{Name: "high-light", Type: "openai", APIFormat: "standard", Models: "gpt-4", Priority: 10, Weight: 1},
	}

	got := channelCandidatesForModel(channels, "gpt-4", nil)
	if len(got) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(got))
	}
	if got[0].Name != "high-light" || got[1].Name != "low-heavy" {
		t.Fatalf("priority order = [%s %s], want [high-light low-heavy]", got[0].Name, got[1].Name)
	}
}

func TestRequestAffinityScopeUsesSessionIdentifiers(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "codex prompt cache key",
			body: []byte(`{"model":"gpt-5.5","prompt_cache_key":"thread-1","input":"hi"}`),
			want: "thread-1",
		},
		{
			name: "anthropic metadata user id session",
			body: []byte(`{"model":"claude","metadata":{"user_id":"{\"session_id\":\"sess-1\"}"},"messages":[]}`),
			want: "sess-1",
		},
		{
			name: "gemini session id",
			body: []byte(`{"model":"gemini","session_id":"gemini-session","contents":[]}`),
			want: "gemini-session",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestAffinityScope(nil, tc.body); got != tc.want {
				t.Fatalf("requestAffinityScope = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestAffinityScopeFallsBackToHeaders(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("X-Session-ID", "header-session")
	if got := requestAffinityScope(&ctx, []byte(`{"model":"gpt-5.5","input":"hi"}`)); got != "header-session" {
		t.Fatalf("requestAffinityScope header fallback = %q", got)
	}
}

func TestUpstreamQuotaExhaustedDetection(t *testing.T) {
	if !isUpstreamQuotaExhausted(fasthttp.StatusTooManyRequests, []byte(`{"error":{"message":"rate limited"}}`)) {
		t.Fatalf("429 should be treated as quota/rate exhaustion")
	}
	if !isUpstreamQuotaExhausted(fasthttp.StatusForbidden, []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"quota exceeded"}}`)) {
		t.Fatalf("RESOURCE_EXHAUSTED should be treated as quota exhaustion")
	}
	if isUpstreamQuotaExhausted(fasthttp.StatusBadRequest, []byte(`{"error":{"message":"invalid request"}}`)) {
		t.Fatalf("ordinary user errors must not trigger account failover")
	}
}
