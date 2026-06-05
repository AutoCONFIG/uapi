package relay

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
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
