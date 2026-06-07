package relay

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
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
			want: "body:prompt_cache_key:thread-1",
		},
		{
			name: "anthropic metadata user id session",
			body: []byte(`{"model":"claude","metadata":{"user_id":"{\"session_id\":\"sess-1\"}"},"messages":[]}`),
			want: "claude:sess-1",
		},
		{
			name: "claude code legacy metadata session",
			body: []byte(`{"model":"claude","metadata":{"user_id":"user_xxx_account__session_ac980658-63bd-4fb3-97ba-8da64cb1e344"},"messages":[]}`),
			want: "claude:ac980658-63bd-4fb3-97ba-8da64cb1e344",
		},
		{
			name: "non claude metadata user id fallback",
			body: []byte(`{"model":"claude","metadata":{"user_id":"plain-user"},"messages":[]}`),
			want: "user:plain-user",
		},
		{
			name: "gemini session id",
			body: []byte(`{"model":"gemini","session_id":"gemini-session","contents":[]}`),
			want: "body:session_id:gemini-session",
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
	if got := requestAffinityScope(&ctx, []byte(`{"model":"gpt-5.5","input":"hi"}`)); got != "header:header-session" {
		t.Fatalf("requestAffinityScope header fallback = %q", got)
	}
}

func TestRequestAffinityScopeSupportsCodexAndOpenCodeHeaders(t *testing.T) {
	var codexCtx fasthttp.RequestCtx
	codexCtx.Request.Header.Set("Session-Id", "codex-session")
	if got := requestAffinityScope(&codexCtx, []byte(`{"model":"gpt-5.5","input":"hi"}`)); got != "codex:codex-session" {
		t.Fatalf("Session-Id affinity = %q", got)
	}

	var openCodeCtx fasthttp.RequestCtx
	openCodeCtx.Request.Header.Set("x-session-affinity", "opencode-session")
	if got := requestAffinityScope(&openCodeCtx, []byte(`{"model":"gpt-5.5","input":"hi"}`)); got != "opencode:opencode-session" {
		t.Fatalf("x-session-affinity = %q", got)
	}
}

func TestRequestAffinityScopeClaudeMetadataHasPriorityOverHeaders(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("X-Session-ID", "header-session")
	ctx.Request.Header.Set("Session_id", "codex-session")
	body := []byte(`{"metadata":{"user_id":"user_xxx_account__session_ac980658-63bd-4fb3-97ba-8da64cb1e344"}}`)
	if got := requestAffinityScope(&ctx, body); got != "claude:ac980658-63bd-4fb3-97ba-8da64cb1e344" {
		t.Fatalf("requestAffinityScope priority = %q", got)
	}
}

func TestSessionFromMetadataUserIDRequiresCompleteLegacySessionSuffix(t *testing.T) {
	if got := sessionFromMetadataUserID("user_xxx_account__session_ac980658-63bd-4fb3-97ba-8da64cb1e344_extra"); got != "" {
		t.Fatalf("sessionFromMetadataUserID with trailing junk = %q, want empty", got)
	}
}

func TestRouteLogAdminInfoOnlyMarksAffinityWhenScopeExists(t *testing.T) {
	info := routeLogAdminInfo("", []map[string]interface{}{
		{"source": "priority", "channel_id": "ch-1", "selected": true, "account_id": "acc-1"},
	})
	if _, ok := info["affinity"]; ok {
		t.Fatalf("routeLogAdminInfo without scope must not include affinity: %#v", info)
	}
	if path, ok := info["route_path"].([]map[string]interface{}); !ok || len(path) != 1 {
		t.Fatalf("route_path = %#v, want one attempt", info["route_path"])
	}
}

func TestRouteLogAdminInfoMarksAffinityHitAndFallbackPath(t *testing.T) {
	info := routeLogAdminInfo("codex:session-1234567890", []map[string]interface{}{
		{"source": "affinity", "channel_id": "ch-1", "selected": true, "account_id": "acc-1", "affinity_account_id": "acc-1"},
	})
	affinity, ok := info["affinity"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing affinity info: %#v", info)
	}
	if hit, _ := affinity["hit"].(bool); !hit {
		t.Fatalf("affinity hit = %#v, want true", affinity["hit"])
	}
	if affinity["source"] != "codex" {
		t.Fatalf("affinity source = %#v, want codex", affinity["source"])
	}
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, Name: "codex"}
	from := &db.Account{Base: db.Base{ID: uuid.New()}, Name: "one"}
	to := &db.Account{Base: db.Base{ID: uuid.New()}, Name: "two"}
	appendRouteFallback(info, "buffered", ch, from, to, 429, "quota_exhausted", 1)
	path, ok := info["fallback_path"].([]map[string]interface{})
	if !ok || len(path) != 1 {
		t.Fatalf("fallback_path = %#v, want one item", info["fallback_path"])
	}
	if path[0]["from_account_name"] != "one" || path[0]["to_account_name"] != "two" {
		t.Fatalf("fallback_path item = %#v", path[0])
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

func TestRuntimeResolveUsesSessionAccountAffinity(t *testing.T) {
	if err := crypto.Init("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("crypto init: %v", err)
	}
	cred1, err := crypto.Encrypt("sk-one")
	if err != nil {
		t.Fatalf("encrypt cred1: %v", err)
	}
	cred2, err := crypto.Encrypt("sk-two")
	if err != nil {
		t.Fatalf("encrypt cred2: %v", err)
	}
	chID := uuid.New()
	acc1ID := uuid.New()
	acc2ID := uuid.New()
	relayer := NewRelayer(nil, NewPoolManager(), nil, NewAffinityCache(), 10, "", false, "")
	relayer.ApplyRuntimeConfig(RuntimeConfig{
		Version: 1,
		Channels: []db.Channel{{
			Base:      db.Base{ID: chID},
			Name:      "runtime-openai",
			Type:      "openai",
			APIFormat: "standard",
			Models:    "gpt-5.5",
			Enabled:   true,
		}},
		Accounts: []RuntimeAccount{
			{ID: acc1ID, ChannelID: chID, Name: "one", Credentials: cred1, CredType: "api_key", Weight: 1, Enabled: true},
			{ID: acc2ID, ChannelID: chID, Name: "two", Credentials: cred2, CredType: "api_key", Weight: 1, Enabled: true},
		},
	})
	relayer.affinity.Set("token-1", "gpt-5.5", "codex:thread-1", chID.String(), acc2ID.String(), 60)

	_, acc, _, creds, err := relayer.resolveChannelAndAccountWithAttempts("token-1", "gpt-5.5", "codex:thread-1", nil)
	if err != nil {
		t.Fatalf("resolve with runtime affinity: %v", err)
	}
	if acc.ID != acc2ID {
		t.Fatalf("account = %s, want affinity account %s", acc.ID, acc2ID)
	}
	if creds != "sk-two" {
		t.Fatalf("creds = %q, want sk-two", creds)
	}
}

func TestRuntimeResolveIgnoresDisabledAffinityChannel(t *testing.T) {
	if err := crypto.Init("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("crypto init: %v", err)
	}
	enabledCred, err := crypto.Encrypt("sk-enabled")
	if err != nil {
		t.Fatalf("encrypt enabled cred: %v", err)
	}
	disabledCred, err := crypto.Encrypt("sk-disabled")
	if err != nil {
		t.Fatalf("encrypt disabled cred: %v", err)
	}
	enabledChID := uuid.New()
	disabledChID := uuid.New()
	enabledAccID := uuid.New()
	disabledAccID := uuid.New()
	relayer := NewRelayer(nil, NewPoolManager(), nil, NewAffinityCache(), 10, "", false, "")
	relayer.ApplyRuntimeConfig(RuntimeConfig{
		Version: 1,
		Channels: []db.Channel{
			{
				Base:      db.Base{ID: enabledChID},
				Name:      "enabled",
				Type:      "openai",
				APIFormat: "standard",
				Models:    "gpt-5.5",
				Enabled:   true,
			},
			{
				Base:      db.Base{ID: disabledChID},
				Name:      "disabled",
				Type:      "openai",
				APIFormat: "standard",
				Models:    "gpt-5.5",
				Enabled:   false,
			},
		},
		Accounts: []RuntimeAccount{
			{ID: enabledAccID, ChannelID: enabledChID, Name: "enabled", Credentials: enabledCred, CredType: "api_key", Weight: 1, Enabled: true},
			{ID: disabledAccID, ChannelID: disabledChID, Name: "disabled", Credentials: disabledCred, CredType: "api_key", Weight: 1, Enabled: true},
		},
	})
	relayer.affinity.Set("token-1", "gpt-5.5", "codex:thread-1", disabledChID.String(), disabledAccID.String(), 60)

	ch, acc, _, creds, err := relayer.resolveChannelAndAccountWithAttempts("token-1", "gpt-5.5", "codex:thread-1", nil)
	if err != nil {
		t.Fatalf("resolve with disabled runtime affinity: %v", err)
	}
	if ch.ID != enabledChID || acc.ID != enabledAccID || creds != "sk-enabled" {
		t.Fatalf("selected (%s,%s,%q), want enabled (%s,%s,sk-enabled)", ch.ID, acc.ID, creds, enabledChID, enabledAccID)
	}
}
