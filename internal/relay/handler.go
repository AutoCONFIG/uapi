package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// streamingClient is configured for real-time streaming:
// no timeouts, streaming body response enabled.
var streamingClient = &fasthttp.Client{
	ReadTimeout:        0,
	WriteTimeout:       0,
	MaxConnDuration:    0,
	StreamResponseBody: true,
}

// bufferedClient is for non-streaming upstream requests with reasonable timeouts.
var bufferedClient = &fasthttp.Client{
	ReadTimeout:     120 * time.Second,
	WriteTimeout:    30 * time.Second,
	MaxConnDuration: 180 * time.Second,
}

// maxResponseSize limits how much data we buffer from upstream (100 MB).
const maxResponseSize = 100 * 1024 * 1024

type Relayer struct {
	db              *gorm.DB
	pools           *PoolManager
	billing         *BillingService
	affinity        *AffinityCache
	concLimiter     *ConcurrencyLimiter
	chCache         *channelCache
	internalSecret  string
	requireInternal bool
	controlURL      string
	runtimeMu       sync.RWMutex
	runtimeVersion  int64
	runtimeChannels map[uuid.UUID]db.Channel
	runtimeAccounts map[uuid.UUID]db.Account
}

func NewRelayer(database *gorm.DB, pools *PoolManager, billing *BillingService, affinity *AffinityCache, concLimit int, internalSecret string, requireInternal bool, controlURL string) *Relayer {
	return &Relayer{
		db:              database,
		pools:           pools,
		billing:         billing,
		affinity:        affinity,
		concLimiter:     NewConcurrencyLimiter(concLimit),
		chCache:         newChannelCache(database, 30*time.Second),
		internalSecret:  internalSecret,
		requireInternal: requireInternal,
		controlURL:      strings.TrimRight(controlURL, "/"),
		runtimeChannels: make(map[uuid.UUID]db.Channel),
		runtimeAccounts: make(map[uuid.UUID]db.Account),
	}
}

type RuntimeConfig struct {
	NodeID   uuid.UUID        `json:"node_id"`
	Version  int64            `json:"version"`
	Channels []db.Channel     `json:"channels"`
	Accounts []RuntimeAccount `json:"accounts"`
	Bindings []db.NodeAccount `json:"bindings"`
}

type RuntimeAccount struct {
	ID            uuid.UUID  `json:"id"`
	ChannelID     uuid.UUID  `json:"channel_id"`
	Name          string     `json:"name"`
	Credentials   string     `json:"credentials"`
	CredType      string     `json:"cred_type"`
	Weight        int        `json:"weight"`
	Enabled       bool       `json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	RefreshToken  string     `json:"refresh_token"`
	TokenExpiry   *time.Time `json:"token_expiry,omitempty"`
	ClientID      string     `json:"client_id"`
	ClientSecret  string     `json:"client_secret"`
	TokenURL      string     `json:"token_url"`
}

func (r *Relayer) StartConfigPuller(nodeID string, interval time.Duration) {
	if r.controlURL == "" || strings.TrimSpace(nodeID) == "" {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			r.pullRuntimeConfig(nodeID)
			<-ticker.C
		}
	}()
}

func (r *Relayer) pullRuntimeConfig(nodeID string) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(r.controlURL + "/internal/relay/config?node_id=" + nodeID)
	req.Header.SetMethod("GET")
	req.Header.Set("X-UAPI-Internal-Secret", r.internalSecret)
	if err := bufferedClient.DoTimeout(req, resp, 10*time.Second); err != nil {
		logger.Warnf("relay.config", "pull failed", logger.Err(err))
		return
	}
	if resp.StatusCode() >= 300 {
		logger.Warnf("relay.config", "pull rejected", logger.F("status", resp.StatusCode()))
		return
	}
	var envelope struct {
		Code    int           `json:"code"`
		Data    RuntimeConfig `json:"data"`
		Message string        `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &envelope); err != nil {
		logger.Warnf("relay.config", "decode failed", logger.Err(err))
		return
	}
	if envelope.Code != 0 {
		logger.Warnf("relay.config", "gateway returned error", logger.F("message", envelope.Message))
		return
	}
	r.ApplyRuntimeConfig(envelope.Data)
}

type relayRequest struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

func (r *Relayer) HandleRelay(ctx *fasthttp.RequestCtx) {
	start := time.Now()
	path := string(ctx.Path())

	// Detect client format from request path
	var clientFormat provider.Format
	switch {
	case strings.HasPrefix(path, "/v1/chat/completions"):
		clientFormat = provider.FormatOpenAIChat
	case strings.HasPrefix(path, "/v1/responses"):
		clientFormat = provider.FormatOpenAIResp
	case strings.HasPrefix(path, "/v1/messages"):
		clientFormat = provider.FormatAnthropic
	case strings.HasPrefix(path, "/v1beta/"):
		clientFormat = provider.FormatGemini
	case strings.HasPrefix(path, "/v1/images/"):
		clientFormat = provider.FormatOpenAIResp
	default:
		clientFormat = provider.FormatOpenAIChat // backward compat
	}

	var token db.Token
	internalClaims, gatewayAuthenticated := internalauth.VerifyRequest(ctx, r.internalSecret, time.Now())
	if gatewayAuthenticated {
		tokenID, err := uuid.Parse(internalClaims.TokenID)
		if err != nil {
			ctx.Error(`{"error":"invalid gateway token id"}`, fasthttp.StatusUnauthorized)
			return
		}
		token.ID = tokenID
		token.UserID = internalClaims.UserID
	} else {
		if r.requireInternal {
			ctx.Error(`{"error":"gateway signature required"}`, fasthttp.StatusUnauthorized)
			return
		}
		// 1. Auth
		tokenKey := extractBearerToken(ctx)
		if tokenKey == "" {
			ctx.Error(`{"error":"missing authorization"}`, fasthttp.StatusUnauthorized)
			return
		}
		if err := r.db.Where("key = ? AND enabled = true AND deleted_at IS NULL", tokenKey).First(&token).Error; err != nil {
			ctx.Error(`{"error":"invalid token"}`, fasthttp.StatusUnauthorized)
			return
		}
		if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
			ctx.Error(`{"error":"token expired"}`, fasthttp.StatusUnauthorized)
			return
		}

		// 2. IP whitelist check
		if token.IPWhitelist != "" && !checkIPWhitelist(ctx, token.IPWhitelist) {
			ctx.Error(`{"error":"ip not whitelisted"}`, fasthttp.StatusForbidden)
			return
		}
	}

	// 3. Concurrency check
	tokenID := token.ID.String()
	if !gatewayAuthenticated {
		if !r.concLimiter.Acquire(tokenID) {
			ctx.Error(`{"error":"concurrent request limit exceeded"}`, 429)
			return
		}
	}
	// Concurrency slot is released:
	//   - streaming: deferred inside the streaming goroutine (after stream completes)
	//   - non-streaming & early returns: deferred below (fires when HandleRelay returns)
	streaming := false
	defer func() {
		if !streaming && !gatewayAuthenticated {
			r.concLimiter.Release(tokenID)
		}
	}()
	if r.billing != nil && !gatewayAuthenticated {
		if err := r.billing.CheckLimit(token.ID.String()); err != nil {
			ctx.Error(`{"error":"rate limit exceeded"}`, 429)
			return
		}
		// Check user balance if token is linked to a user
		if token.UserID != "" {
			if err := r.billing.CheckUserBalance(token.UserID, token.ID.String()); err != nil {
				ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, 402)
				return
			}
		}
	}

	// 5. Parse request
	var req relayRequest
	body := ctx.PostBody()
	if err := json.Unmarshal(body, &req); err != nil {
		ctx.Error(`{"error":"invalid request body"}`, fasthttp.StatusBadRequest)
		return
	}
	req.Model = modelFromRequestPath(path, req.Model)
	if req.Model == "" && strings.HasPrefix(path, "/v1/images/") {
		req.Model = modelFromImageRequest(ctx)
	}
	if req.Model == "" && strings.HasPrefix(path, "/v1/images/") {
		req.Model = "gpt-image-1"
	}
	if req.Model == "" {
		ctx.Error(`{"error":"model is required"}`, fasthttp.StatusBadRequest)
		return
	}
	body = injectModelIfMissing(body, req.Model)
	if gatewayAuthenticated && internalClaims.Model != req.Model {
		ctx.Error(`{"error":"gateway model mismatch"}`, fasthttp.StatusUnauthorized)
		return
	}
	if !gatewayAuthenticated && token.Models != "" && !modelInList(req.Model, token.Models) {
		ctx.Error(`{"error":"model not allowed for token"}`, fasthttp.StatusForbidden)
		return
	}
	if !gatewayAuthenticated && token.Permissions != "" && !permissionInList(permissionForFormat(clientFormat), token.Permissions) {
		ctx.Error(`{"error":"permission not allowed for token"}`, fasthttp.StatusForbidden)
		return
	}

	// 6. Find channel + account
	var targetChannel *db.Channel
	var account *db.Account
	var adaptor provider.Adaptor
	var creds string
	var err error
	if gatewayAuthenticated && internalClaims.ChannelID != "" && internalClaims.AccountID != "" {
		targetChannel, account, adaptor, creds, err = r.resolveSelectedChannelAndAccount(internalClaims.ChannelID, internalClaims.AccountID, req.Model)
	} else {
		targetChannel, account, adaptor, creds, err = r.resolveChannelAndAccount(token.ID.String(), req.Model)
	}
	if err != nil {
		ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusNotFound)
		return
	}

	// 7. Pre-consume billing
	estimatedTokens := req.MaxTokens
	if estimatedTokens == 0 {
		estimatedTokens = 1000
	}
	if gatewayAuthenticated && internalClaims.EstimatedTokens > 0 {
		estimatedTokens = internalClaims.EstimatedTokens
	}
	if r.billing != nil && (!gatewayAuthenticated || !internalClaims.Precharged) {
		if err := r.billing.PreConsume(token.ID.String(), req.Model, estimatedTokens); err != nil {
			logger.Warnf("relay.billing", "pre-consume failed", logger.F("token_id", token.ID.String()), logger.Err(err))
		}
	}
	var claims *internalauth.Claims
	if gatewayAuthenticated {
		claims = &internalClaims
	}

	// 8. Build upstream request
	adaptor.Init(targetChannel, account)
	adaptor.SetRequestParams(req.Model, req.Stream)
	upstreamURL, err := adaptor.GetRequestURL(path)
	if err != nil {
		go r.finishFailureUsage(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusInternalServerError, estimatedTokens)
		ctx.Error(`{"error":"build url failed"}`, fasthttp.StatusInternalServerError)
		return
	}

	forceStreamActive := targetChannel.ForceStream && !req.Stream
	if forceStreamActive {
		body = injectStreamTrue(body)
	}

	// Determine upstream format from channel type
	var upstreamFormat provider.Format
	switch targetChannel.Type {
	case "openai":
		if targetChannel.APIFormat == "responses" || targetChannel.APIFormat == "codex" {
			upstreamFormat = provider.FormatOpenAIResp
		} else {
			upstreamFormat = provider.FormatOpenAIChat
		}
	case "anthropic":
		upstreamFormat = provider.FormatAnthropic
	case "gemini":
		if targetChannel.APIFormat == "gemini_code" {
			upstreamFormat = provider.FormatGeminiCode
		} else {
			upstreamFormat = provider.FormatGemini
		}
	default:
		upstreamFormat = provider.FormatOpenAIChat
	}

	logger.Debugf("relay.route", "request routed",
		logger.F("token_id", token.ID.String()),
		logger.F("model", req.Model),
		logger.F("stream", req.Stream),
		logger.F("force_stream", forceStreamActive),
		logger.F("client_format", string(clientFormat)),
		logger.F("upstream_format", string(upstreamFormat)),
		logger.F("channel_id", targetChannel.ID.String()),
		logger.F("channel_type", targetChannel.Type),
		logger.F("api_format", targetChannel.APIFormat),
		logger.F("account_id", account.ID.String()),
		logger.F("account_cred_type", account.CredType),
		logger.F("gateway_authenticated", gatewayAuthenticated),
	)

	if strings.HasPrefix(path, "/v1/images/") {
		r.handleImageRequest(ctx, token, targetChannel, account, adaptor, upstreamURL, body, creds, req.Model, start, estimatedTokens, claims)
		return
	}

	convertedBody, err := provider.ConvertRequestWithAdaptor(clientFormat, upstreamFormat, body, adaptor)
	if err != nil {
		go r.finishFailureUsage(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusBadRequest, estimatedTokens)
		ctx.Error(`{"error":"convert request failed: `+jsonEscape(err.Error())+`"}`, fasthttp.StatusBadRequest)
		return
	}

	// 9. Dispatch
	if req.Stream && !forceStreamActive {
		streaming = true // goroutine handles Release
		r.handleStreaming(ctx, token, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	} else if forceStreamActive {
		streaming = true
		r.handleForceStream(ctx, token, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	} else {
		streaming = true // handleBuffered manages its own concurrency release
		r.handleBuffered(ctx, token, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	}

	// 10. Record affinity for non-streaming paths (handleBuffered + handleForceStream are synchronous)
	if !req.Stream && targetChannel.AffinityTTL > 0 && ctx.Response.StatusCode() < 400 {
		r.affinity.Set(token.ID.String(), req.Model, targetChannel.ID.String(), targetChannel.AffinityTTL)
	}
}

// handleStreaming: real-time chunk-by-chunk forwarding using SSEStreamReader.
func (r *Relayer) handleStreaming(ctx *fasthttp.RequestCtx, token db.Token, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start)
		return
	}

	// streamingClient returns after receiving headers, body streamed via BodyStream
	if err := streamingClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.upstream", "streaming request failed", logger.Err(err))
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start)
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		respBody := upResp.Body()
		bodyCopy := make([]byte, len(respBody))
		copy(bodyCopy, respBody)
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, false, start, clientFormat, claims)
		return
	}

	// SSE headers for downstream
	ctx.SetStatusCode(statusCode)
	ctx.Response.Header.Set("Content-Type", "text/event-stream")
	ctx.Response.Header.Set("Cache-Control", "no-cache")
	ctx.Response.Header.Set("Connection", "keep-alive")
	ctx.Response.Header.Set("X-Accel-Buffering", "no")

	reader := NewSSEStreamReader()
	ctx.Response.SetBodyStream(reader, -1)

	tracker := newStreamTracker(adaptor)

	// Determine if we need line conversion (Anthropic/Gemini need format conversion)
	var inputConvert func([]byte) []byte
	if adaptor.GetChannelType() != "openai" {
		inputConvert = adaptor.ConvertStreamLine
	}

	// Determine if we need reverse conversion (client expects non-OpenAI SSE format)
	var outputConvert func([]byte) []byte
	if clientFormat != provider.FormatOpenAIChat && clientFormat != provider.FormatOpenAIResp {
		outputConvert = adaptor.CreateReverseStreamConverter()
	}

	// Producer goroutine: owns upReq/upResp lifecycle, releases when done
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Default().Panic("relay.stream", "stream goroutine panic", rec)
				if r.billing != nil {
					go r.billing.Refund(token.ID.String(), estTokens)
				}
			}
		}()
		defer fasthttp.ReleaseRequest(upReq)
		defer fasthttp.ReleaseResponse(upResp)
		defer r.concLimiter.Release(token.ID.String())

		result := streamAndForward(upResp.BodyStream(), reader, tracker, inputConvert, outputConvert)
		if result.err != nil {
			logger.Warnf("relay.stream", "forward failed", logger.Err(result.err))
		} else {
			// Record affinity only on successful stream completion
			if ch.AffinityTTL > 0 {
				r.affinity.Set(token.ID.String(), model, ch.ID.String(), ch.AffinityTTL)
			}
		}
		pt, ct := tracker.Result()
		logger.Debugf("relay.stream", "stream request completed",
			logger.F("token_id", token.ID.String()),
			logger.F("channel_id", ch.ID.String()),
			logger.F("account_id", acc.ID.String()),
			logger.F("model", model),
			logger.F("status", statusCode),
			logger.F("prompt_tokens", pt),
			logger.F("completion_tokens", ct),
			logger.F("latency_ms", time.Since(start).Milliseconds()),
		)
		go r.finishUsage(claims, token.ID, ch.ID, acc.ID, model, true, pt, ct, start, statusCode, estTokens)
	}()
}

// handleForceStream: stream to upstream, buffer all, convert to non-stream for downstream.
func (r *Relayer) handleForceStream(ctx *fasthttp.RequestCtx, token db.Token, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Default().Panic("relay.stream", "force stream panic", rec)
			r.refundAndError(ctx, token.ID.String(), estTokens, "internal error", claims, ch, acc, model, start)
			r.concLimiter.Release(token.ID.String())
		}
	}()

	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start)
		r.concLimiter.Release(token.ID.String())
		return
	}

	if err := streamingClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.upstream", "force stream request failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start)
		r.concLimiter.Release(token.ID.String())
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		respBody := upResp.Body()
		bodyCopy := make([]byte, len(respBody))
		copy(bodyCopy, respBody)
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, false, start, clientFormat, claims)
		r.concLimiter.Release(token.ID.String())
		return
	}

	// Buffer entire stream (bounded by maxResponseSize)
	respBody, err := io.ReadAll(io.LimitReader(upResp.BodyStream(), int64(maxResponseSize)))
	if err != nil {
		logger.Warnf("relay.upstream", "force stream read failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "read upstream error", claims, ch, acc, model, start)
		r.concLimiter.Release(token.ID.String())
		return
	}

	// Convert upstream SSE to OpenAI SSE format if needed (required for StreamToNonStream)
	if adaptor.GetChannelType() != "openai" {
		respBody = adaptor.ConvertSSEBuffer(respBody)
	}

	// SSE -> non-stream JSON (produces OpenAI Chat format)
	respBody = StreamToNonStream(respBody)

	// Parse usage from OpenAI JSON BEFORE client-format conversion
	pt, ct := parseNonStreamUsage(respBody)

	// Convert from OpenAI JSON to client format if needed
	if clientFormat != provider.FormatOpenAIChat {
		if converted, err := provider.ConvertResponse(provider.FormatOpenAIChat, clientFormat, respBody); err != nil {
			logger.Warnf("relay.convert", "response conversion failed", logger.Err(err))
		} else {
			respBody = converted
		}
	}

	ctx.SetStatusCode(statusCode)
	ctx.Response.Header.Set("Content-Type", "application/json")
	ctx.SetBody(respBody)

	logger.Debugf("relay.stream", "force stream request completed",
		logger.F("token_id", token.ID.String()),
		logger.F("channel_id", ch.ID.String()),
		logger.F("account_id", acc.ID.String()),
		logger.F("model", model),
		logger.F("status", statusCode),
		logger.F("prompt_tokens", pt),
		logger.F("completion_tokens", ct),
		logger.F("latency_ms", time.Since(start).Milliseconds()),
	)
	go r.finishUsage(claims, token.ID, ch.ID, acc.ID, model, false, pt, ct, start, statusCode, estTokens)
	r.concLimiter.Release(token.ID.String())
}

// --- Channel cache ---

type channelCache struct {
	mu       sync.RWMutex
	channels []db.Channel
	expiry   time.Time
	ttl      time.Duration
	db       *gorm.DB
}

func newChannelCache(database *gorm.DB, ttl time.Duration) *channelCache {
	return &channelCache{db: database, ttl: ttl}
}

func (r *Relayer) handleImageRequest(ctx *fasthttp.RequestCtx, token db.Token, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, start time.Time, estTokens int, claims *internalauth.Claims) {
	if ch.Type != "openai" {
		r.refundAndError(ctx, token.ID.String(), estTokens, "image generation is only available on OpenAI-compatible image channels", claims, ch, acc, model, start)
		return
	}
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes(ctx.Method())
	upReq.SetBody(body)
	if contentType := ctx.Request.Header.ContentType(); len(contentType) > 0 {
		upReq.Header.SetBytesV("Content-Type", contentType)
	}
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start)
		return
	}
	if err := bufferedClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.images", "upstream image request failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start)
		return
	}
	statusCode := upResp.StatusCode()
	respBody := copyBody(upResp)
	if statusCode >= 400 {
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, acc, model, false, start, provider.FormatOpenAIResp, claims)
		return
	}
	copyHeaders(upResp, &ctx.Response.Header)
	ctx.SetStatusCode(statusCode)
	ctx.SetBody(respBody)
	r.concLimiter.Release(token.ID.String())
	logger.Debugf("relay.images", "image request completed", logger.F("token_id", token.ID.String()), logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("model", model), logger.F("status", statusCode), logger.F("latency_ms", time.Since(start).Milliseconds()))
	go r.finishUsage(claims, token.ID, ch.ID, acc.ID, model, false, 0, 0, start, statusCode, estTokens)
}

func (c *channelCache) get() []db.Channel {
	c.mu.RLock()
	if time.Now().Before(c.expiry) && c.channels != nil {
		result := c.channels
		c.mu.RUnlock()
		return result
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Double-check after acquiring write lock
	if time.Now().Before(c.expiry) && c.channels != nil {
		return c.channels
	}

	var channels []db.Channel
	if err := c.db.Where("enabled = true AND deleted_at IS NULL").Order("priority DESC").Find(&channels).Error; err != nil {
		logger.Warnf("relay.channel_cache", "db query failed", logger.Err(err))
		return []db.Channel{}
	}
	c.channels = channels
	c.expiry = time.Now().Add(c.ttl)
	return channels
}

// handleBuffered: standard buffered request with retry.
func (r *Relayer) handleBuffered(ctx *fasthttp.RequestCtx, token db.Token, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
	var respBody []byte
	var statusCode int
	var respHeaders fasthttp.ResponseHeader
	respAccount := acc
	currentCreds := creds
	currentAccount := acc

	for retry := 0; retry < 3; retry++ {
		upReq := fasthttp.AcquireRequest()
		upResp := fasthttp.AcquireResponse()

		upReq.SetRequestURI(url)
		upReq.Header.SetMethodBytes(ctx.Method())
		upReq.SetBody(body)
		if err := adaptor.SetupRequestHeader(upReq, currentCreds); err != nil {
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, currentAccount, model, start)
			return
		}

		err := bufferedClient.Do(upReq, upResp)
		fasthttp.ReleaseRequest(upReq)

		shouldRetry := false
		if err != nil {
			logger.Warnf("relay.upstream", "buffered request failed", logger.F("retry", retry), logger.Err(err))
			shouldRetry = true
		} else if upResp.StatusCode() >= 500 {
			logger.Warnf("relay.upstream", "retryable upstream status", logger.F("status", upResp.StatusCode()), logger.F("retry", retry))
			respBody = copyBody(upResp)
			statusCode = upResp.StatusCode()
			copyHeaders(upResp, &respHeaders)
			shouldRetry = true
		}

		if shouldRetry {
			fasthttp.ReleaseResponse(upResp)
			r.cooldownAndEvict(ch, currentAccount)
			currentAccount = r.pickNext(ch, poolFromChannel(r.pools, ch))
			if currentAccount == nil {
				break
			}
			respAccount = currentAccount
			adaptor.Init(ch, currentAccount)
			currentCreds, err = r.ensureCredentials(currentAccount)
			if err != nil {
				logger.Warnf("relay.credentials", "credential error on retry", logger.F("retry", retry), logger.Err(err))
				currentAccount = r.retryNext(ch, currentAccount)
				if currentAccount == nil {
					break
				}
				respAccount = currentAccount
				adaptor.Init(ch, currentAccount)
				currentCreds, err = r.ensureCredentials(currentAccount)
				if err != nil {
					logger.Warnf("relay.credentials", "credential error on replacement retry", logger.F("retry", retry), logger.Err(err))
					break
				}
			}
			continue
		}

		// Success
		respBody = copyBody(upResp)
		statusCode = upResp.StatusCode()
		copyHeaders(upResp, &respHeaders)
		fasthttp.ReleaseResponse(upResp)
		respAccount = currentAccount
		break
	}

	if respBody == nil {
		r.concLimiter.Release(token.ID.String())
		go r.finishFailureUsage(claims, token.ID, ch.ID, respAccount.ID, model, false, start, fasthttp.StatusServiceUnavailable, estTokens)
		ctx.Error(`{"error":"all retries exhausted"}`, fasthttp.StatusServiceUnavailable)
		return
	}

	if statusCode >= 400 {
		if ch.APIFormat == "gemini_code" {
			logGeminiCodeUpstreamError(ch, respAccount, statusCode, body, respBody)
		}
		if ch.APIFormat == "gemini_code" && isGeminiQuotaExhausted(statusCode, respBody) {
			if fallbackModel, fallbackBody, ok := geminiCodeFallbackBody(model, body); ok {
				if retryBody, retryStatus, ok := r.retryGeminiCodeFallback(ctx, adaptor, url, fallbackBody, currentCreds, &respHeaders); ok {
					respBody = retryBody
					statusCode = retryStatus
					model = fallbackModel
				}
			}
		}
		if statusCode >= 400 {
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, respAccount, model, false, start, clientFormat, claims)
			return
		}
	}

	// Parse usage from upstream-format response BEFORE conversion
	pt, ct := 0, 0
	if claims != nil && claims.RequestID != "" {
		if parsedPT, parsedCT, err := adaptor.ParseUsage(respBody); err == nil {
			pt, ct = parsedPT, parsedCT
		}
	} else {
		pt, ct = r.settleAndRefund(token.ID.String(), respBody, adaptor, estTokens, model)
	}

	// Response format conversion
	if clientFormat != upstreamFormat {
		if converted, err := provider.ConvertResponse(upstreamFormat, clientFormat, respBody); err != nil {
			logger.Warnf("relay.convert", "response conversion failed", logger.Err(err))
		} else {
			respBody = converted
		}
	}

	ctx.SetStatusCode(statusCode)
	respHeaders.VisitAll(func(key, value []byte) {
		ctx.Response.Header.SetBytesKV(key, value)
	})
	ctx.SetBody(respBody)

	logger.Debugf("relay.buffered", "buffered request completed",
		logger.F("token_id", token.ID.String()),
		logger.F("channel_id", ch.ID.String()),
		logger.F("account_id", respAccount.ID.String()),
		logger.F("model", model),
		logger.F("status", statusCode),
		logger.F("prompt_tokens", pt),
		logger.F("completion_tokens", ct),
		logger.F("latency_ms", time.Since(start).Milliseconds()),
	)
	if claims != nil && claims.RequestID != "" {
		go r.finishUsage(claims, token.ID, ch.ID, respAccount.ID, model, false, pt, ct, start, statusCode, estTokens)
	} else {
		go r.writeLog(token.ID, ch.ID, respAccount.ID, model, false, pt, ct, start, statusCode)
	}
}

func logGeminiCodeUpstreamError(ch *db.Channel, acc *db.Account, statusCode int, reqBody, respBody []byte) {
	var summary struct {
		Model              string   `json:"model"`
		Project            string   `json:"project"`
		EnabledCreditTypes []string `json:"enabled_credit_types"`
	}
	if err := json.Unmarshal(reqBody, &summary); err != nil {
		logger.Warnf("relay.gemini_code", "upstream error", logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("status", statusCode), logger.Err(err), logger.F("response", compactLogBody(respBody)))
		return
	}
	logger.Warnf("relay.gemini_code", "upstream error", logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("status", statusCode), logger.F("model", summary.Model), logger.F("project", summary.Project), logger.F("enabled_credit_types", summary.EnabledCreditTypes), logger.F("response", compactLogBody(respBody)))
}

func compactLogBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "empty response"
	}
	if len(text) > 1200 {
		return text[:1200] + "..."
	}
	return text
}

func (r *Relayer) retryGeminiCodeFallback(ctx *fasthttp.RequestCtx, adaptor provider.Adaptor, url string, body []byte, creds string, headers *fasthttp.ResponseHeader) ([]byte, int, bool) {
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes(ctx.Method())
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		return nil, 0, false
	}
	if err := bufferedClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.gemini_code", "fallback upstream error", logger.Err(err))
		return nil, 0, false
	}
	if headers != nil {
		copyHeaders(upResp, headers)
	}
	return copyBody(upResp), upResp.StatusCode(), true
}

func isGeminiQuotaExhausted(statusCode int, body []byte) bool {
	if statusCode != fasthttp.StatusTooManyRequests {
		return false
	}
	text := strings.ToUpper(string(body))
	return strings.Contains(text, "RESOURCE_EXHAUSTED") || strings.Contains(text, "RESOURCE HAS BEEN EXHAUSTED") || strings.Contains(text, "QUOTA")
}

func geminiCodeFallbackBody(model string, body []byte) (string, []byte, bool) {
	fallback := ""
	switch model {
	case "", "auto", "pro", "auto-gemini-2.5", "gemini-2.5-pro", "gemini-3-pro-preview", "gemini-3.1-pro-preview":
		fallback = "gemini-2.5-flash"
	case "flash", "gemini-2.5-flash", "gemini-3-flash-preview":
		fallback = "gemini-2.5-flash-lite"
	default:
		return "", nil, false
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, false
	}
	payload["model"] = fallback
	updated, err := json.Marshal(payload)
	if err != nil {
		return "", nil, false
	}
	return fallback, updated, true
}

// --- Helpers ---

func (r *Relayer) resolveChannelAndAccount(tokenID, model string) (*db.Channel, *db.Account, provider.Adaptor, string, error) {
	// Try affinity cache first
	if r.db != nil {
		if chID := r.affinity.Get(tokenID, model); chID != "" {
			var ch db.Channel
			if err := r.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", chID).First(&ch).Error; err == nil {
				if modelInList(model, ch.Models) {
					if acc, adaptor, creds, err := r.pickAccount(ch); err == nil {
						return &ch, acc, adaptor, creds, nil
					}
				}
			}
		}
	}

	if r.db == nil {
		r.runtimeMu.RLock()
		channels := make([]db.Channel, 0, len(r.runtimeChannels))
		for _, ch := range r.runtimeChannels {
			channels = append(channels, ch)
		}
		r.runtimeMu.RUnlock()
		for i := range channels {
			if modelInList(model, channels[i].Models) {
				if acc, adaptor, creds, err := r.pickAccount(channels[i]); err == nil {
					return &channels[i], acc, adaptor, creds, nil
				}
			}
		}
		return nil, nil, nil, "", errNoChannel
	}

	// Priority-based selection (with caching)
	channels := r.chCache.get()
	for i := range channels {
		if modelInList(model, channels[i].Models) {
			if acc, adaptor, creds, err := r.pickAccount(channels[i]); err == nil {
				return &channels[i], acc, adaptor, creds, nil
			}
		}
	}
	return nil, nil, nil, "", errNoChannel
}

var errNoChannel = fmt.Errorf("no available channel for model")

func (r *Relayer) resolveSelectedChannelAndAccount(channelID, accountID, model string) (*db.Channel, *db.Account, provider.Adaptor, string, error) {
	chUUID, err := uuid.Parse(channelID)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("invalid selected channel")
	}
	accUUID, err := uuid.Parse(accountID)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("invalid selected account")
	}
	if ch, acc, ok := r.runtimeSelected(chUUID, accUUID); ok {
		if !modelInList(model, ch.Models) {
			return nil, nil, nil, "", fmt.Errorf("selected channel does not support model")
		}
		adaptor := getAdaptor(ch.Type)
		if adaptor == nil {
			return nil, nil, nil, "", fmt.Errorf("unsupported channel type")
		}
		creds, err := r.ensureCredentials(acc)
		if err != nil {
			return nil, nil, nil, "", fmt.Errorf("credential error: %w", err)
		}
		return ch, acc, adaptor, creds, nil
	}
	if r.db == nil {
		return nil, nil, nil, "", fmt.Errorf("selected account not in runtime config")
	}
	var ch db.Channel
	if err := r.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", chUUID).First(&ch).Error; err != nil {
		return nil, nil, nil, "", fmt.Errorf("selected channel not available")
	}
	if !modelInList(model, ch.Models) {
		return nil, nil, nil, "", fmt.Errorf("selected channel does not support model")
	}
	var acc db.Account
	if err := r.db.Where("id = ? AND channel_id = ? AND enabled = true AND deleted_at IS NULL", accUUID, chUUID).First(&acc).Error; err != nil {
		return nil, nil, nil, "", fmt.Errorf("selected account not available")
	}
	adaptor := getAdaptor(ch.Type)
	if adaptor == nil {
		return nil, nil, nil, "", fmt.Errorf("unsupported channel type")
	}
	creds, err := r.ensureCredentials(&acc)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("credential error: %w", err)
	}
	return &ch, &acc, adaptor, creds, nil
}

func (r *Relayer) runtimeSelected(channelID, accountID uuid.UUID) (*db.Channel, *db.Account, bool) {
	r.runtimeMu.RLock()
	defer r.runtimeMu.RUnlock()
	ch, ok := r.runtimeChannels[channelID]
	if !ok {
		return nil, nil, false
	}
	acc, ok := r.runtimeAccounts[accountID]
	if !ok || acc.ChannelID != channelID {
		return nil, nil, false
	}
	return &ch, &acc, true
}

func (r *Relayer) ApplyRuntimeConfig(cfg RuntimeConfig) {
	r.runtimeMu.RLock()
	currentVersion := r.runtimeVersion
	r.runtimeMu.RUnlock()
	if cfg.Version > 0 && currentVersion >= cfg.Version {
		return
	}

	channels := make(map[uuid.UUID]db.Channel, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		channels[ch.ID] = ch
	}
	accounts := make(map[uuid.UUID]db.Account, len(cfg.Accounts))
	for _, acc := range cfg.Accounts {
		accounts[acc.ID] = db.Account{
			Base:      db.Base{ID: acc.ID},
			ChannelID: acc.ChannelID, Name: acc.Name, Credentials: acc.Credentials, CredType: acc.CredType,
			Weight: acc.Weight, Enabled: acc.Enabled, CooldownUntil: acc.CooldownUntil, RefreshToken: acc.RefreshToken,
			TokenExpiry: acc.TokenExpiry, ClientID: acc.ClientID, ClientSecret: acc.ClientSecret, TokenURL: acc.TokenURL,
		}
	}
	r.runtimeMu.Lock()
	r.runtimeVersion = cfg.Version
	r.runtimeChannels = channels
	r.runtimeAccounts = accounts
	r.runtimeMu.Unlock()

	for channelID := range r.pools.Snapshot() {
		if _, ok := channels[channelID]; !ok {
			r.pools.RemovePool(channelID.String())
		}
	}
	grouped := make(map[uuid.UUID][]*db.Account)
	for id := range channels {
		grouped[id] = nil
	}
	for id := range accounts {
		acc := accounts[id]
		if _, ok := channels[acc.ChannelID]; ok {
			accCopy := acc
			grouped[acc.ChannelID] = append(grouped[acc.ChannelID], &accCopy)
		}
	}
	for channelID, list := range grouped {
		r.pools.SetPool(channelID.String(), NewAccountPool(list))
	}
}

func (r *Relayer) pickAccount(ch db.Channel) (*db.Account, provider.Adaptor, string, error) {
	adaptor := getAdaptor(ch.Type)
	if adaptor == nil {
		return nil, nil, "", fmt.Errorf("unsupported channel type")
	}
	pool, ok := r.pools.GetPool(ch.ID.String())
	if !ok {
		return nil, nil, "", fmt.Errorf("channel pool not initialized")
	}
	account, ok := pool.Pick()
	if !ok {
		return nil, nil, "", fmt.Errorf("no available account")
	}
	creds, err := r.ensureCredentials(account)
	if err != nil {
		return nil, nil, "", fmt.Errorf("credential error: %w", err)
	}
	return account, adaptor, creds, nil
}

func (r *Relayer) ensureCredentials(account *db.Account) (string, error) {
	return EnsureValidCredentials(account, r.db)
}

func (r *Relayer) cooldownAndEvict(ch *db.Channel, acc *db.Account) {
	if pool, ok := r.pools.GetPool(ch.ID.String()); ok {
		pool.Cooldown(acc.ID.String(), 5*time.Minute)
	}
	r.affinity.EvictChannel(ch.ID.String())
}

func (r *Relayer) retryNext(ch *db.Channel, failed *db.Account) *db.Account {
	r.cooldownAndEvict(ch, failed)
	pool, _ := r.pools.GetPool(ch.ID.String())
	return r.pickNext(ch, pool)
}

func (r *Relayer) pickNext(ch *db.Channel, pool *AccountPool) *db.Account {
	if pool == nil {
		return nil
	}
	acc, ok := pool.Pick()
	if !ok {
		return nil
	}
	return acc
}

func poolFromChannel(pm *PoolManager, ch *db.Channel) *AccountPool {
	p, _ := pm.GetPool(ch.ID.String())
	return p
}

func (r *Relayer) refundAndError(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time) {
	r.concLimiter.Release(tokenID)
	go r.finishFailureUsageWithError(claims, tokenID, ch.ID, acc.ID, model, false, start, fasthttp.StatusInternalServerError, estTokens, msg)
	ctx.Error(`{"error":"`+jsonEscape(msg)+`"}`, fasthttp.StatusInternalServerError)
}

// normalizeErrorResponse converts an upstream error body into the client's expected format.
func normalizeErrorResponse(respBody []byte, clientFormat provider.Format) []byte {
	errMsg := errorMessageFromResponse(respBody)
	switch clientFormat {
	case provider.FormatAnthropic:
		return formatAnthropicError(errMsg)
	case provider.FormatGemini:
		return formatGeminiError(errMsg)
	default: // OpenAI Chat / Responses
		return formatOpenAIError(errMsg)
	}
}

func errorMessageFromResponse(respBody []byte) string {
	// Try OpenAI/Anthropic style first: {"error": {"message": "..."}}
	var openaiStyle struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"` // Gemini style top-level message
		Detail  string `json:"detail"`  // some providers
	}

	errMsg := "upstream error"
	if json.Unmarshal(respBody, &openaiStyle) == nil {
		if openaiStyle.Error != nil && openaiStyle.Error.Message != "" {
			errMsg = openaiStyle.Error.Message
		} else if openaiStyle.Message != "" {
			errMsg = openaiStyle.Message
		} else if openaiStyle.Detail != "" {
			errMsg = openaiStyle.Detail
		}
	}

	// If the message is still the default, try Gemini nested error: {"error": {"message": "...", "status": "..."}}
	if errMsg == "upstream error" {
		var geminiStyle struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &geminiStyle) == nil && geminiStyle.Error.Message != "" {
			errMsg = geminiStyle.Error.Message
		}
	}

	errMsg = stripProviderInfo(errMsg)
	return errMsg
}

func stripProviderInfo(msg string) string {
	// Remove common provider-specific prefixes/identifiers that leak upstream info
	// e.g. "org-xxx:", "req_xxx:", model paths like "/v1/models/xxx"
	for _, prefix := range []string{"org-", "req_"} {
		if strings.HasPrefix(msg, prefix) {
			msg = msg[len(prefix):]
		}
	}
	return msg
}

func formatOpenAIError(msg string) []byte {
	result, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    "relay_error",
		},
	})
	return result
}

func formatAnthropicError(msg string) []byte {
	result, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "api_error",
			"message": msg,
		},
	})
	return result
}

func formatGeminiError(msg string) []byte {
	result, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    500,
			"message": msg,
			"status":  "INTERNAL",
		},
	})
	return result
}

func (r *Relayer) refundOnError(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, statusCode int, respBody []byte, ch *db.Channel, acc *db.Account, model string, isStream bool, start time.Time, clientFormat provider.Format, claims *internalauth.Claims) {
	r.concLimiter.Release(tokenID)
	ctx.SetStatusCode(statusCode)
	normalizedBody := normalizeErrorResponse(respBody, clientFormat)
	ctx.SetBody(normalizedBody)
	go r.finishFailureUsageWithError(claims, tokenID, ch.ID, acc.ID, model, isStream, start, statusCode, estTokens, errorMessageFromResponse(respBody))
}

func injectStreamTrue(body []byte) []byte {
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return body
	}
	bodyMap["stream"] = true
	result, err := json.Marshal(bodyMap)
	if err != nil {
		return body
	}
	return result
}

func injectModelIfMissing(body []byte, model string) []byte {
	if model == "" {
		return body
	}
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return body
	}
	if existing, ok := bodyMap["model"].(string); ok && strings.TrimSpace(existing) != "" {
		return body
	}
	bodyMap["model"] = model
	result, err := json.Marshal(bodyMap)
	if err != nil {
		return body
	}
	return result
}

func copyBody(resp *fasthttp.Response) []byte {
	b := resp.Body()
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// hopByHopHeaders should not be forwarded between proxy hops.
var hopByHopHeaders = map[string]struct{}{
	"Transfer-Encoding": {},
	"Connection":        {},
	"Keep-Alive":        {},
	"Upgrade":           {},
}

func copyHeaders(resp *fasthttp.Response, dst *fasthttp.ResponseHeader) {
	resp.Header.VisitAll(func(k, v []byte) {
		if _, skip := hopByHopHeaders[string(k)]; skip {
			return
		}
		dst.SetBytesKV(k, v)
	})
}

func parseNonStreamUsage(respBody []byte) (int, int) {
	var resp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(respBody, &resp) == nil && (resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0) {
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	return 0, 0
}

func extractBearerToken(ctx *fasthttp.RequestCtx) string {
	auth := string(ctx.Request.Header.Peek("Authorization"))
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	if key := strings.TrimSpace(string(ctx.Request.Header.Peek("x-api-key"))); key != "" {
		return key
	}
	if key := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Goog-Api-Key"))); key != "" {
		return key
	}
	return ""
}

func modelFromRequestPath(path, bodyModel string) string {
	if strings.TrimSpace(bodyModel) != "" {
		return bodyModel
	}
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return ""
	}
	if idx := strings.Index(rest, ":"); idx >= 0 {
		rest = rest[:idx]
	}
	if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[:idx]
	}
	return strings.TrimSpace(rest)
}

func modelFromImageRequest(ctx *fasthttp.RequestCtx) string {
	var body struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(ctx.PostBody(), &body) == nil && strings.TrimSpace(body.Model) != "" {
		return strings.TrimSpace(body.Model)
	}
	if model := strings.TrimSpace(string(ctx.FormValue("model"))); model != "" {
		return model
	}
	return ""
}

// checkIPWhitelist verifies the client IP against the whitelist.
// It checks both the direct connection IP and forwarded headers (X-Real-IP, X-Forwarded-For).
// CAUTION: X-Forwarded-For and X-Real-IP headers can be spoofed by clients.
// Only trust these headers when behind a trusted reverse proxy that strips untrusted headers.
func checkIPWhitelist(ctx *fasthttp.RequestCtx, whitelist string) bool {
	for _, allowedIP := range strings.Split(whitelist, ",") {
		allowedIP = strings.TrimSpace(allowedIP)
		if allowedIP == "" {
			continue
		}
		for _, clientIP := range clientIPCandidates(ctx) {
			if allowedIP == clientIP {
				return true
			}
		}
	}
	return false
}

func clientIPCandidates(ctx *fasthttp.RequestCtx) []string {
	candidates := []string{ctx.RemoteIP().String()}
	if xRealIP := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Real-IP"))); xRealIP != "" {
		candidates = append(candidates, xRealIP)
	}
	if forwardedFor := strings.TrimSpace(string(ctx.Request.Header.Peek("X-Forwarded-For"))); forwardedFor != "" {
		firstHop := strings.TrimSpace(strings.Split(forwardedFor, ",")[0])
		if firstHop != "" {
			candidates = append(candidates, firstHop)
		}
	}
	return candidates
}

func getAdaptor(channelType string) provider.Adaptor {
	switch channelType {
	case "openai":
		return &openai.OpenAIAdaptor{}
	case "anthropic":
		return &anthropic.AnthropicAdaptor{}
	case "gemini":
		return &gemini.GeminiAdaptor{}
	default:
		return nil
	}
}

func modelInList(model, list string) bool {
	for _, m := range strings.Split(list, ",") {
		if strings.TrimSpace(m) == model {
			return true
		}
	}
	return false
}

func permissionForFormat(format provider.Format) string {
	switch format {
	case provider.FormatAnthropic:
		return "messages"
	case provider.FormatGemini:
		return "gemini"
	case provider.FormatOpenAIResp:
		return "responses"
	default:
		return "chat"
	}
}

func permissionInList(permission, list string) bool {
	for _, item := range strings.Split(list, ",") {
		if strings.TrimSpace(item) == permission {
			return true
		}
	}
	return false
}

func (r *Relayer) writeLog(tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int) {
	r.writeLogWithError(tokenID, channelID, accountID, model, isStream, pt, ct, start, statusCode, "")
}

func (r *Relayer) writeLogWithError(tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, errorMessage string) {
	if r.db == nil {
		return
	}
	logEntry := db.Log{
		TokenID:          toUUID(tokenID),
		ChannelID:        toUUID(channelID),
		AccountID:        toUUID(accountID),
		Model:            model,
		IsStream:         isStream,
		PromptTokens:     int64(pt),
		CompletionTokens: int64(ct),
		TotalTokens:      int64(pt + ct),
		LatencyMs:        time.Since(start).Milliseconds(),
		StatusCode:       statusCode,
		ErrorMessage:     errorMessage,
	}
	if err := r.db.Create(&logEntry).Error; err != nil {
		logger.Warnf("relay.logs", "write request log failed", logger.Err(err))
	}
}

func (r *Relayer) finishUsage(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, estTokens int) {
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, isStream, pt, ct, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLog(tokenID, channelID, accountID, model, isStream, pt, ct, start, statusCode)
	if r.billing != nil {
		go r.billing.RefundAndSettle(toUUID(tokenID).String(), estTokens, pt, ct, model)
	}
}

func (r *Relayer) finishFailureUsage(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, start time.Time, statusCode int, estTokens int) {
	r.finishFailureUsageWithError(claims, tokenID, channelID, accountID, model, isStream, start, statusCode, estTokens, "")
}

func (r *Relayer) finishFailureUsageWithError(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, start time.Time, statusCode int, estTokens int, errorMessage string) {
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, isStream, 0, 0, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLogWithError(tokenID, channelID, accountID, model, isStream, 0, 0, start, statusCode, errorMessage)
	if r.billing != nil {
		go r.billing.Refund(toUUID(tokenID).String(), estTokens)
	}
}

func (r *Relayer) reportUsageEvent(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, estTokens int) error {
	if r.controlURL == "" {
		return fmt.Errorf("control url not configured")
	}
	payload := map[string]interface{}{
		"request_id":        claims.RequestID,
		"token_id":          toUUID(tokenID),
		"channel_id":        toUUID(channelID),
		"account_id":        toUUID(accountID),
		"model":             model,
		"is_stream":         isStream,
		"prompt_tokens":     pt,
		"completion_tokens": ct,
		"estimated_tokens":  estTokens,
		"status_code":       statusCode,
		"latency_ms":        time.Since(start).Milliseconds(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(r.controlURL + "/internal/relay/usage-events")
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/json")
	req.Header.Set("X-UAPI-Internal-Secret", r.internalSecret)
	req.SetBody(body)
	if err := bufferedClient.DoTimeout(req, resp, 10*time.Second); err != nil {
		return err
	}
	if resp.StatusCode() >= 300 {
		return fmt.Errorf("usage event rejected: %d", resp.StatusCode())
	}
	return nil
}

func toUUID(v interface{}) uuid.UUID {
	switch id := v.(type) {
	case uuid.UUID:
		return id
	case string:
		parsed, err := uuid.Parse(id)
		if err == nil {
			return parsed
		}
	}
	return uuid.UUID{}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// Strip surrounding quotes
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}

func (r *Relayer) settleAndRefund(tokenID string, respBody []byte, adaptor provider.Adaptor, estTokens int, model string) (int, int) {
	r.concLimiter.Release(tokenID)
	if r.billing == nil {
		return 0, 0
	}
	pt, ct := 0, 0
	if adaptor != nil && len(respBody) > 0 {
		if p, c, err := adaptor.ParseUsage(respBody); err == nil {
			pt, ct = p, c
		}
	}
	go r.billing.RefundAndSettle(tokenID, estTokens, pt, ct, model)
	return pt, ct
}
