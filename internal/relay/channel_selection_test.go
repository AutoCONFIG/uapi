package relay

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
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

type bufferedSuccessAdaptor struct{}

func (bufferedSuccessAdaptor) Init(*db.Channel, *db.Account) {}
func (bufferedSuccessAdaptor) SetRequestParams(string, bool) {}
func (bufferedSuccessAdaptor) GetRequestURL(string) (string, error) {
	return "http://upstream.test/v1/chat/completions", nil
}
func (bufferedSuccessAdaptor) SetupRequestHeader(*fasthttp.Request, string) error { return nil }
func (bufferedSuccessAdaptor) FromIR(*relayir.Request) ([]byte, error) {
	return nil, errors.New("not implemented")
}
func (bufferedSuccessAdaptor) ParseUsage([]byte) (int, int, error)       { return 1, 1, nil }
func (bufferedSuccessAdaptor) ParseStreamUsage([]byte) (int, int, error) { return 0, 0, nil }
func (bufferedSuccessAdaptor) ParseUsageFull([]byte) (provider.InternalUsage, error) {
	return provider.InternalUsage{PromptTokens: 1, CompletionTokens: 1}, nil
}
func (bufferedSuccessAdaptor) GetChannelType() string { return "test" }
func (bufferedSuccessAdaptor) DoHTTPRequest(req *fasthttp.Request, resp *fasthttp.Response) error {
	resp.SetStatusCode(fasthttp.StatusOK)
	resp.Header.SetContentType("application/json")
	resp.SetBodyString(`{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	return nil
}

type bufferedRetryAdaptor struct {
	calls int
}

func (a *bufferedRetryAdaptor) Init(*db.Channel, *db.Account) {}
func (a *bufferedRetryAdaptor) SetRequestParams(string, bool) {}
func (a *bufferedRetryAdaptor) GetRequestURL(string) (string, error) {
	return "http://upstream.test/v1/chat/completions", nil
}
func (a *bufferedRetryAdaptor) SetupRequestHeader(*fasthttp.Request, string) error { return nil }
func (a *bufferedRetryAdaptor) FromIR(*relayir.Request) ([]byte, error) {
	return nil, errors.New("not implemented")
}
func (a *bufferedRetryAdaptor) ParseUsage([]byte) (int, int, error)       { return 1, 1, nil }
func (a *bufferedRetryAdaptor) ParseStreamUsage([]byte) (int, int, error) { return 0, 0, nil }
func (a *bufferedRetryAdaptor) ParseUsageFull([]byte) (provider.InternalUsage, error) {
	return provider.InternalUsage{PromptTokens: 1, CompletionTokens: 1}, nil
}
func (a *bufferedRetryAdaptor) GetChannelType() string { return "test" }
func (a *bufferedRetryAdaptor) DoHTTPRequest(req *fasthttp.Request, resp *fasthttp.Response) error {
	a.calls++
	resp.Header.SetContentType("application/json")
	if a.calls == 1 {
		resp.SetStatusCode(fasthttp.StatusTooManyRequests)
		resp.SetBodyString(`{"error":{"message":"rate limit"}}`)
		return nil
	}
	resp.SetStatusCode(fasthttp.StatusOK)
	resp.SetBodyString(`{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	return nil
}

func TestHandleBufferedSuccessReleasesLocalConcurrency(t *testing.T) {
	tokenID := uuid.New()
	token := db.Token{Base: db.Base{ID: tokenID}}
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, APIFormat: "openai"}
	acc := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1}
	limiter := NewConcurrencyLimiter(1)
	if status := limiter.AcquireDetailed(context.Background(), tokenID.String()); status != AcquireOK {
		t.Fatalf("AcquireDetailed = %v, want AcquireOK", status)
	}
	relayer := &Relayer{
		concLimiter:       limiter,
		pools:             NewPoolManager(),
		cooldownPolicy:    NewCooldownPolicy(),
		channelModelBlock: NewChannelModelBlocklist(0),
	}
	defer relayer.cooldownPolicy.Close()
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)

	relayer.handleBuffered(&ctx, token, uuid.Nil, ch, acc, bufferedSuccessAdaptor{}, "/v1/chat/completions",
		[]byte(`{"model":"gpt-test","messages":[]}`),
		"http://upstream.test/v1/chat/completions",
		[]byte(`{"model":"gpt-test","messages":[]}`),
		"sk-test", "gpt-test", "gpt-test",
		provider.FormatOpenAIChatCompletions, provider.FormatOpenAIChatCompletions,
		time.Now(), 100, nil, nil, map[string]interface{}{}, "", requestTypeChatCompletion, 0)

	if got := limiter.ActiveCount(tokenID.String()); got != 0 {
		t.Fatalf("buffered success leaked local concurrency slot, active = %d", got)
	}
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status = %d, want 200", ctx.Response.StatusCode())
	}
}

func TestHandleBufferedRetrySwitchAccountReleasesFinalInFlight(t *testing.T) {
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
	tokenID := uuid.New()
	token := db.Token{Base: db.Base{ID: tokenID}}
	ch := &db.Channel{Base: db.Base{ID: uuid.New()}, Type: "openai", APIFormat: "standard"}
	acc1 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, Credentials: cred1, CredType: "api_key"}
	acc2 := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Enabled: true, Weight: 1, Credentials: cred2, CredType: "api_key"}
	pool := NewAccountPool([]*db.Account{acc1, acc2})
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	limiter := NewConcurrencyLimiter(1)
	if status := limiter.AcquireDetailed(context.Background(), tokenID.String()); status != AcquireOK {
		t.Fatalf("AcquireDetailed = %v, want AcquireOK", status)
	}
	relayer := &Relayer{
		concLimiter:       limiter,
		pools:             pools,
		affinity:          NewAffinityCache(),
		cooldownPolicy:    NewCooldownPolicy(),
		channelModelBlock: NewChannelModelBlocklist(0),
	}
	defer relayer.cooldownPolicy.Close()
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	adaptor := &bufferedRetryAdaptor{}

	relayer.handleBuffered(&ctx, token, uuid.Nil, ch, acc1, adaptor, "/v1/chat/completions",
		[]byte(`{"model":"gpt-test","messages":[]}`),
		"http://upstream.test/v1/chat/completions",
		[]byte(`{"model":"gpt-test","messages":[]}`),
		"sk-test", "gpt-test", "gpt-test",
		provider.FormatOpenAIChatCompletions, provider.FormatOpenAIChatCompletions,
		time.Now(), 100, nil, nil, map[string]interface{}{}, "header:scope", requestTypeChatCompletion, 0)

	if adaptor.calls != 2 {
		t.Fatalf("calls = %d, want 2", adaptor.calls)
	}
	if got := pool.InFlight(acc1.ID.String()); got != 0 {
		t.Fatalf("first account in-flight = %d, want 0", got)
	}
	if got := pool.InFlight(acc2.ID.String()); got != 0 {
		t.Fatalf("final account in-flight = %d, want 0", got)
	}
	if got := limiter.ActiveCount(tokenID.String()); got != 0 {
		t.Fatalf("buffered retry leaked local concurrency slot, active = %d", got)
	}
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status = %d, want 200", ctx.Response.StatusCode())
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

func TestNormalStreamingPathPropagatesAffinityScope(t *testing.T) {
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "routeAdminInfo, affinityScope, requestType)") {
		t.Fatalf("normal streaming dispatch must pass affinityScope to handleStreaming")
	}
	if !strings.Contains(text, "trace, adminInfo, affinityScope, requestType)") {
		t.Fatalf("handleStreaming must pass affinityScope to handleStreamingAttempt")
	}
	if strings.Contains(text, "trace, adminInfo, \"\", requestType)") {
		t.Fatalf("handleStreaming must not drop affinityScope when starting first streaming attempt")
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
			want: "",
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

func TestRequestAffinityScopeDoesNotUseTokenFallback(t *testing.T) {
	var ctx fasthttp.RequestCtx
	if got := requestAffinityScope(&ctx, []byte(`{"model":"gpt-5.5","input":"hi"}`)); got != "" {
		t.Fatalf("requestAffinityScope without session = %q, want empty", got)
	}
}

func TestRequestAffinityScopeDoesNotUseRequestIDAsSession(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.Set("X-Client-Request-Id", "single-request")
	if got := requestAffinityScope(&ctx, []byte(`{"model":"gpt-5.5","input":"hi"}`)); got != "" {
		t.Fatalf("requestAffinityScope with request id = %q, want empty", got)
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

func TestAccountSideRouteFallbackClassification(t *testing.T) {
	info := map[string]interface{}{}
	appendRouteFallback(info, "stream", nil, nil, nil, fasthttp.StatusBadGateway, "channel_failover", 0)
	if hasAccountSideRouteFallback(info) {
		t.Fatalf("server-side channel failover must not force affinity migration")
	}

	appendRouteFallback(info, "stream", nil, nil, nil, fasthttp.StatusTooManyRequests, "quota_exhausted", 1)
	if !hasAccountSideRouteFallback(info) {
		t.Fatalf("account-side fallback must force affinity migration")
	}
}

func TestRouteLogAdminInfoMarksAffinityYieldWithoutHit(t *testing.T) {
	info := routeLogAdminInfo("codex:session-1234567890", []map[string]interface{}{
		{
			"source":                     "affinity",
			"channel_id":                 "ch-1",
			"selected":                   true,
			"account_id":                 "acc-selected",
			"affinity_account_id":        "acc-bound",
			"affinity_account_yielded":   true,
			"affinity_yield_reason":      "bound_account_unavailable_or_overloaded",
			"account_in_flight":          2,
			"affinity_account_in_flight": accountInFlightSoftCap,
		},
	})
	affinity, ok := info["affinity"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing affinity info: %#v", info)
	}
	if hit, _ := affinity["hit"].(bool); hit {
		t.Fatalf("affinity hit = true, want false when bound account yielded")
	}
	if yielded, _ := affinity["yielded"].(bool); !yielded {
		t.Fatalf("affinity yielded = %#v, want true", affinity["yielded"])
	}
	if affinity["selected_account_id"] != "acc-selected" {
		t.Fatalf("selected_account_id = %#v, want acc-selected", affinity["selected_account_id"])
	}
	selected, _ := info["selected"].(map[string]interface{})
	if selected["account_in_flight"] != 2 || selected["affinity_account_in_flight"] != accountInFlightSoftCap {
		t.Fatalf("selected route did not preserve load debug: %#v", selected)
	}
	if !hasAffinityYielded(info) {
		t.Fatalf("hasAffinityYielded = false, want true")
	}
}

func TestAffinityYieldForcesSuccessfulAffinityMigration(t *testing.T) {
	oldCh := &db.Channel{Base: db.Base{ID: uuid.New()}, AffinityTTL: 60}
	oldAcc := &db.Account{Base: db.Base{ID: uuid.New()}}
	newCh := &db.Channel{Base: db.Base{ID: uuid.New()}, AffinityTTL: 60}
	newAcc := &db.Account{Base: db.Base{ID: uuid.New()}}
	relayer := &Relayer{affinity: NewAffinityCache()}
	relayer.affinity.Set("token", "model", "scope", oldCh.ID.String(), oldAcc.ID.String(), 60)

	adminInfo := routeLogAdminInfo("header:scope", []map[string]interface{}{
		{
			"source":                   "affinity",
			"selected":                 true,
			"channel_id":               oldCh.ID.String(),
			"account_id":               newAcc.ID.String(),
			"affinity_account_id":      oldAcc.ID.String(),
			"affinity_account_yielded": true,
		},
	})
	result := relayer.recordSuccessfulAffinity("token", "model", "scope", newCh, newAcc, hasAccountSideRouteFallback(adminInfo) || hasAffinityYielded(adminInfo))

	if !result.Force || result.Action != "force_set" {
		t.Fatalf("affinity yield record result = %#v, want force_set", result)
	}
	if !result.Migrated || result.PreviousAccountID != oldAcc.ID.String() || result.BoundAccountID != newAcc.ID.String() {
		t.Fatalf("affinity migration result = %#v, want migrated old->new", result)
	}
	gotCh, gotAcc := relayer.affinity.Get("token", "model", "scope")
	if gotCh != newCh.ID.String() || gotAcc != newAcc.ID.String() {
		t.Fatalf("affinity = (%q,%q), want (%q,%q)", gotCh, gotAcc, newCh.ID.String(), newAcc.ID.String())
	}
}

func TestSuccessfulAffinityPreservesExistingWhenNotForced(t *testing.T) {
	oldCh := &db.Channel{Base: db.Base{ID: uuid.New()}, AffinityTTL: 60}
	oldAcc := &db.Account{Base: db.Base{ID: uuid.New()}}
	newCh := &db.Channel{Base: db.Base{ID: uuid.New()}, AffinityTTL: 60}
	newAcc := &db.Account{Base: db.Base{ID: uuid.New()}}
	relayer := &Relayer{affinity: NewAffinityCache()}
	relayer.affinity.Set("token", "model", "scope", oldCh.ID.String(), oldAcc.ID.String(), 60)

	result := relayer.recordSuccessfulAffinity("token", "model", "scope", newCh, newAcc, false)

	if result.Force || result.Action != "preserved_existing" {
		t.Fatalf("recordSuccessfulAffinity = %#v, want preserved_existing without force", result)
	}
	gotCh, gotAcc := relayer.affinity.Get("token", "model", "scope")
	if gotCh != oldCh.ID.String() || gotAcc != oldAcc.ID.String() {
		t.Fatalf("affinity = (%q,%q), want existing (%q,%q)", gotCh, gotAcc, oldCh.ID.String(), oldAcc.ID.String())
	}
}

func TestRouteSelectionErrorDistinguishesNoAccountFromNoChannel(t *testing.T) {
	attempts := []map[string]interface{}{
		{
			"supports_model":      true,
			"supports_capability": true,
			"skip_reason":         "no available account",
		},
	}
	if err := routeSelectionError(&attempts); err != errNoAccount {
		t.Fatalf("routeSelectionError = %v, want %v", err, errNoAccount)
	}

	attempts = []map[string]interface{}{
		{
			"supports_model":      false,
			"supports_capability": true,
			"skip_reason":         "unsupported_model",
		},
	}
	if err := routeSelectionError(&attempts); err != errNoChannel {
		t.Fatalf("routeSelectionError = %v, want %v", err, errNoChannel)
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

func TestUpstreamAccountFailoverReason(t *testing.T) {
	reason, isQuota, ok := upstreamAccountFailoverReason(fasthttp.StatusForbidden, []byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"quota exceeded"}}`))
	if !ok || !isQuota || reason != "quota_exhausted" {
		t.Fatalf("quota failover = (%q, %v, %v), want quota_exhausted true true", reason, isQuota, ok)
	}

	reason, isQuota, ok = upstreamAccountFailoverReason(fasthttp.StatusForbidden, []byte(`{"error":{"status":"PERMISSION_DENIED","message":"permission denied"}}`))
	if !ok || isQuota || reason != "permission_denied" {
		t.Fatalf("permission failover = (%q, %v, %v), want permission_denied false true", reason, isQuota, ok)
	}

	if reason, isQuota, ok = upstreamAccountFailoverReason(fasthttp.StatusBadRequest, []byte(`{"error":{"message":"invalid request"}}`)); ok {
		t.Fatalf("ordinary request error failover = (%q, %v, %v), want disabled", reason, isQuota, ok)
	}

	if reason, isQuota, ok = upstreamAccountFailoverReason(fasthttp.StatusBadRequest, []byte(`{"error":{"code":"invalid_api_key","message":"invalid api key field in request body"}}`)); ok {
		t.Fatalf("400 invalid_api_key field failover = (%q, %v, %v), want disabled", reason, isQuota, ok)
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
				Base:        db.Base{ID: enabledChID},
				Name:        "enabled",
				Type:        "openai",
				APIFormat:   "standard",
				Models:      "gpt-5.5",
				Enabled:     true,
				AffinityTTL: 60,
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

	var attempts []map[string]interface{}
	ch, acc, _, creds, err := relayer.resolveChannelAndAccountWithAttempts("token-1", "gpt-5.5", "codex:thread-1", &attempts)
	if err != nil {
		t.Fatalf("resolve with disabled runtime affinity: %v", err)
	}
	if ch.ID != enabledChID || acc.ID != enabledAccID || creds != "sk-enabled" {
		t.Fatalf("selected (%s,%s,%q), want enabled (%s,%s,sk-enabled)", ch.ID, acc.ID, creds, enabledChID, enabledAccID)
	}
	info := routeLogAdminInfo("codex:thread-1", attempts)
	if !hasAffinitySelectionFailed(info) {
		t.Fatalf("hasAffinitySelectionFailed = false, want true; info=%#v", info)
	}
	result := relayer.recordSuccessfulAffinity("token-1", "gpt-5.5", "codex:thread-1", ch, acc, hasAffinitySelectionFailed(info))
	if !result.Force || result.Action != "force_set" {
		t.Fatalf("recordSuccessfulAffinity = %#v, want force_set", result)
	}
	gotCh, gotAcc := relayer.affinity.Get("token-1", "gpt-5.5", "codex:thread-1")
	if gotCh != enabledChID.String() || gotAcc != enabledAccID.String() {
		t.Fatalf("affinity = (%q,%q), want enabled (%s,%s)", gotCh, gotAcc, enabledChID, enabledAccID)
	}
}

func TestRuntimeResolveSkipsExcludedAffinityChannelWithoutEvicting(t *testing.T) {
	if err := crypto.Init("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("crypto init: %v", err)
	}
	affinityCred, err := crypto.Encrypt("sk-affinity")
	if err != nil {
		t.Fatalf("encrypt affinity cred: %v", err)
	}
	fallbackCred, err := crypto.Encrypt("sk-fallback")
	if err != nil {
		t.Fatalf("encrypt fallback cred: %v", err)
	}
	affinityChID := uuid.New()
	fallbackChID := uuid.New()
	affinityAccID := uuid.New()
	fallbackAccID := uuid.New()
	relayer := NewRelayer(nil, NewPoolManager(), nil, NewAffinityCache(), 10, "", false, "")
	relayer.ApplyRuntimeConfig(RuntimeConfig{
		Version: 1,
		Channels: []db.Channel{
			{
				Base:      db.Base{ID: affinityChID},
				Name:      "affinity",
				Type:      "openai",
				APIFormat: "standard",
				Models:    "gpt-5.5",
				Enabled:   true,
				Priority:  10,
			},
			{
				Base:      db.Base{ID: fallbackChID},
				Name:      "fallback",
				Type:      "openai",
				APIFormat: "standard",
				Models:    "gpt-5.5",
				Enabled:   true,
				Priority:  10,
			},
		},
		Accounts: []RuntimeAccount{
			{ID: affinityAccID, ChannelID: affinityChID, Name: "affinity", Credentials: affinityCred, CredType: "api_key", Weight: 1, Enabled: true},
			{ID: fallbackAccID, ChannelID: fallbackChID, Name: "fallback", Credentials: fallbackCred, CredType: "api_key", Weight: 1, Enabled: true},
		},
	})
	relayer.affinity.Set("token-1", "gpt-5.5", "codex:thread-1", affinityChID.String(), affinityAccID.String(), 60)

	excluded := map[string]bool{affinityChID.String(): true}
	var attempts []map[string]interface{}
	ch, acc, _, creds, err := relayer.resolveChannelAndAccountWithAttemptsExcluded("token-1", "gpt-5.5", "codex:thread-1", &attempts, excluded)
	if err != nil {
		t.Fatalf("resolve with excluded affinity: %v", err)
	}
	if ch.ID != fallbackChID || acc.ID != fallbackAccID || creds != "sk-fallback" {
		t.Fatalf("selected (%s,%s,%q), want fallback (%s,%s,sk-fallback)", ch.ID, acc.ID, creds, fallbackChID, fallbackAccID)
	}
	gotCh, gotAcc := relayer.affinity.Get("token-1", "gpt-5.5", "codex:thread-1")
	if gotCh != affinityChID.String() || gotAcc != affinityAccID.String() {
		t.Fatalf("excluded affinity must remain cached, got (%q,%q)", gotCh, gotAcc)
	}
	info := routeLogAdminInfo("codex:thread-1", attempts)
	if hasAffinitySelectionFailed(info) {
		t.Fatalf("excluded affinity must not count as selection failure: %#v", info)
	}
}

func TestRuntimeResolveNewCodexSessionPrefersLowerSessionLoadAccount(t *testing.T) {
	if err := crypto.Init("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("crypto init: %v", err)
	}
	cred1, err := crypto.Encrypt("sk-one")
	if err != nil {
		t.Fatalf("encrypt one: %v", err)
	}
	cred2, err := crypto.Encrypt("sk-two")
	if err != nil {
		t.Fatalf("encrypt two: %v", err)
	}
	chID := uuid.New()
	acc1ID := uuid.New()
	acc2ID := uuid.New()
	relayer := NewRelayer(nil, NewPoolManager(), nil, NewAffinityCache(), 10, "", false, "")
	relayer.ApplyRuntimeConfig(RuntimeConfig{
		Version: 1,
		Channels: []db.Channel{{
			Base:        db.Base{ID: chID},
			Name:        "codex",
			Type:        "openai",
			APIFormat:   "codex",
			Models:      "gpt-5.5",
			Enabled:     true,
			AffinityTTL: 60,
		}},
		Accounts: []RuntimeAccount{
			{ID: acc1ID, ChannelID: chID, Name: "one", Credentials: cred1, CredType: "api_key", Weight: 100, Enabled: true},
			{ID: acc2ID, ChannelID: chID, Name: "two", Credentials: cred2, CredType: "api_key", Weight: 100, Enabled: true},
		},
	})
	relayer.affinity.Set("token-1", "gpt-5.5", "codex:session-a", chID.String(), acc1ID.String(), 60)
	relayer.affinity.Set("token-1", "gpt-5.5", "codex:session-b", chID.String(), acc1ID.String(), 60)

	var attempts []map[string]interface{}
	_, acc, _, creds, err := relayer.resolveChannelAndAccountWithAttempts("token-1", "gpt-5.5", "codex:session-new", &attempts)
	if err != nil {
		t.Fatalf("resolve new codex session: %v", err)
	}
	if acc.ID != acc2ID || creds != "sk-two" {
		t.Fatalf("selected (%s,%q), want lower-load account %s/sk-two", acc.ID, creds, acc2ID)
	}
	info := routeLogAdminInfo("codex:session-new", attempts)
	selected, _ := info["selected"].(map[string]interface{})
	if selected["account_session_affinity_load"] != 0 {
		t.Fatalf("selected session load = %#v, want 0; info=%#v", selected["account_session_affinity_load"], info)
	}
}

func TestPickAccountForAffinityYieldsWhenBoundAccountOverSoftCap(t *testing.T) {
	if err := crypto.Init("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("crypto init: %v", err)
	}
	boundCred, err := crypto.Encrypt("sk-bound")
	if err != nil {
		t.Fatalf("encrypt bound cred: %v", err)
	}
	otherCred, err := crypto.Encrypt("sk-other")
	if err != nil {
		t.Fatalf("encrypt other cred: %v", err)
	}
	ch := db.Channel{
		Base:      db.Base{ID: uuid.New()},
		Type:      "openai",
		APIFormat: "standard",
		Models:    "gpt-5.5",
		Enabled:   true,
	}
	bound := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Name: "bound", Credentials: boundCred, CredType: "api_key", Enabled: true, Weight: 100}
	other := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Name: "other", Credentials: otherCred, CredType: "api_key", Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{bound, other})
	for i := 0; i < accountInFlightSoftCap; i++ {
		pool.Begin(bound.ID.String())
	}
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	relayer := &Relayer{pools: pools}

	got, _, creds, err := relayer.pickAccountForAffinity(ch, bound.ID.String(), "gpt-5.5", false)
	if err != nil {
		t.Fatalf("pickAccountForAffinity: %v", err)
	}
	if got.ID != other.ID || creds != "sk-other" {
		t.Fatalf("selected (%s,%q), want other %s/sk-other", got.ID, creds, other.ID)
	}
}

func TestPickAccountForStrictAffinityKeepsBoundAccountOverSoftCap(t *testing.T) {
	if err := crypto.Init("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("crypto init: %v", err)
	}
	boundCred, err := crypto.Encrypt("sk-bound")
	if err != nil {
		t.Fatalf("encrypt bound cred: %v", err)
	}
	otherCred, err := crypto.Encrypt("sk-other")
	if err != nil {
		t.Fatalf("encrypt other cred: %v", err)
	}
	ch := db.Channel{
		Base:      db.Base{ID: uuid.New()},
		Type:      "openai",
		APIFormat: "standard",
		Models:    "gpt-5.5",
		Enabled:   true,
	}
	bound := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Name: "bound", Credentials: boundCred, CredType: "api_key", Enabled: true, Weight: 100}
	other := &db.Account{Base: db.Base{ID: uuid.New()}, ChannelID: ch.ID, Name: "other", Credentials: otherCred, CredType: "api_key", Enabled: true, Weight: 1}
	pool := NewAccountPool([]*db.Account{bound, other})
	for i := 0; i < accountInFlightSoftCap; i++ {
		pool.Begin(bound.ID.String())
	}
	pools := NewPoolManager()
	pools.SetPool(ch.ID.String(), pool)
	relayer := &Relayer{pools: pools}

	got, _, creds, err := relayer.pickAccountForAffinity(ch, bound.ID.String(), "gpt-5.5", true)
	if err != nil {
		t.Fatalf("pickAccountForAffinity: %v", err)
	}
	if got.ID != bound.ID || creds != "sk-bound" {
		t.Fatalf("selected (%s,%q), want bound %s/sk-bound", got.ID, creds, bound.ID)
	}
}

func TestStrictAffinityScopeOnlyAppliesToCodex(t *testing.T) {
	if !strictAffinityScope("codex:session-1") {
		t.Fatalf("codex scope should be strict")
	}
	for _, scope := range []string{"header:session-1", "opencode:session-1", "claude:session-1", "thread:session-1", ""} {
		if strictAffinityScope(scope) {
			t.Fatalf("scope %q should not be strict", scope)
		}
	}
}
