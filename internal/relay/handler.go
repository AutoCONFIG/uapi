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
	ReadTimeout:         120 * time.Second,
	WriteTimeout:        30 * time.Second,
	MaxConnDuration:     180 * time.Second,
	MaxResponseBodySize: maxResponseSize,
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
	trustedProxies  []string
	runtimeMu       sync.RWMutex
	runtimeVersion  int64
	runtimeChannels map[uuid.UUID]db.Channel
	runtimeAccounts map[uuid.UUID]db.Account
}

type RelayerOption func(*Relayer)

// WithConcurrencyLimiter sets an existing ConcurrencyLimiter to share
// (useful when running in "all" mode to share the limiter with Gateway).
func WithConcurrencyLimiter(limiter *ConcurrencyLimiter) RelayerOption {
	return func(r *Relayer) {
		r.concLimiter = limiter
	}
}

// WithTrustedProxies sets the list of trusted proxy IPs that are allowed
// to set X-Forwarded-For / X-Real-IP headers.
func WithTrustedProxies(proxies []string) RelayerOption {
	return func(r *Relayer) {
		r.trustedProxies = proxies
	}
}

func NewRelayer(database *gorm.DB, pools *PoolManager, billing *BillingService, affinity *AffinityCache, concLimit int, internalSecret string, requireInternal bool, controlURL string, opts ...RelayerOption) *Relayer {
	r := &Relayer{
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
	for _, opt := range opts {
		opt(r)
	}
	return r
}

type RuntimeConfig struct {
	NodeID   uuid.UUID        `json:"node_id"`
	Version  int64            `json:"version"`
	Channels []db.Channel     `json:"channels"`
	Accounts []RuntimeAccount `json:"accounts"`
	Bindings []db.NodeChannel `json:"bindings"`
}

type RuntimeAccount struct {
	ID            uuid.UUID              `json:"id"`
	ChannelID     uuid.UUID              `json:"channel_id"`
	Name          string                 `json:"name"`
	Credentials   string                 `json:"credentials"`
	CredType      string                 `json:"cred_type"`
	Endpoint      string                 `json:"endpoint"`
	Weight        int                    `json:"weight"`
	Enabled       bool                   `json:"enabled"`
	CooldownUntil *time.Time             `json:"cooldown_until,omitempty"`
	RefreshToken  string                 `json:"refresh_token"`
	TokenExpiry   *time.Time             `json:"token_expiry,omitempty"`
	ClientID      string                 `json:"client_id"`
	ClientSecret  string                 `json:"client_secret"`
	TokenURL      string                 `json:"token_url"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

func (r *Relayer) StartConfigPuller(nodeID string, interval time.Duration) {
	if r.controlURL == "" || strings.TrimSpace(nodeID) == "" {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		next := time.Duration(0)
		maxInterval := 60 * time.Second
		for {
			if next > 0 {
				time.Sleep(next)
			}
			if r.pullRuntimeConfig(nodeID) {
				next = interval
			} else {
				if next < interval {
					next = interval
				} else {
					next *= 2
					if next > maxInterval {
						next = maxInterval
					}
				}
			}
		}
	}()
}

func (r *Relayer) pullRuntimeConfig(nodeID string) bool {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(r.controlURL + "/internal/relay/config?node_id=" + nodeID)
	req.Header.SetMethod("GET")
	req.Header.Set("X-UAPI-Internal-Secret", r.internalSecret)
	if err := bufferedClient.DoTimeout(req, resp, 10*time.Second); err != nil {
		logger.Warnf("relay.config", "pull failed", logger.Err(err))
		return false
	}
	if resp.StatusCode() >= 300 {
		logger.Warnf("relay.config", "pull rejected", logger.F("status", resp.StatusCode()))
		return false
	}
	var envelope struct {
		Code    int           `json:"code"`
		Data    RuntimeConfig `json:"data"`
		Message string        `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &envelope); err != nil {
		logger.Warnf("relay.config", "decode failed", logger.Err(err))
		return false
	}
	if envelope.Code != 0 {
		logger.Warnf("relay.config", "gateway returned error", logger.F("message", envelope.Message))
		return false
	}
	r.ApplyRuntimeConfig(envelope.Data)
	return true
}

type relayRequest struct {
	Model               string `json:"model"`
	Stream              bool   `json:"stream"`
	MaxTokens           int    `json:"max_tokens,omitempty"`
	MaxOutputTokens     int    `json:"max_output_tokens,omitempty"`
	MaxCompletionTokens int    `json:"max_completion_tokens,omitempty"`
	GenerationConfig    struct {
		MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	} `json:"generationConfig,omitempty"`
}

func (r *Relayer) HandleRelay(ctx *fasthttp.RequestCtx) {
	start := time.Now()
	path := string(ctx.Path())

	// Detect client format from request path
	var clientFormat provider.Format
	switch {
	case strings.HasPrefix(path, "/v1/chat/completions"):
		clientFormat = provider.FormatOpenAIChatCompletions
	case strings.HasPrefix(path, "/v1/responses"):
		clientFormat = provider.FormatOpenAIResponses
	case strings.HasPrefix(path, "/v1/messages"):
		clientFormat = provider.FormatAnthropic
	case strings.HasPrefix(path, "/v1beta/"):
		clientFormat = provider.FormatGemini
	case strings.HasPrefix(path, "/v1/images/"):
		clientFormat = provider.FormatOpenAIResponses
	default:
		clientFormat = provider.FormatOpenAIChatCompletions // backward compat
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
		if token.IPWhitelist != "" && !checkIPWhitelist(ctx, token.IPWhitelist, r.trustedProxies) {
			ctx.Error(`{"error":"ip not whitelisted"}`, fasthttp.StatusForbidden)
			return
		}
	}

	// 3. Concurrency check
	tokenID := token.ID.String()
	var claims *internalauth.Claims
	if gatewayAuthenticated {
		claims = &internalClaims
	}
	earlyEstimatedTokens := internalClaims.EstimatedTokens
	if earlyEstimatedTokens <= 0 {
		earlyEstimatedTokens = 1000
	}
	finishGatewayEarlyFailure := func(model string, status int) {
		if claims == nil || claims.RequestID == "" || !internalClaims.Precharged {
			return
		}
		if model == "" {
			model = internalClaims.Model
		}
		go r.finishFailureUsage(claims, token.ID, internalClaims.ChannelID, internalClaims.AccountID, model, false, start, status, earlyEstimatedTokens)
	}
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
	isImageRequest := strings.HasPrefix(path, "/v1/images/")
	if isImageRequest {
		req.Model = modelFromImageRequest(ctx)
		if req.Model == "" {
			req.Model = "gpt-image-1"
		}
	} else {
		if err := json.Unmarshal(body, &req); err != nil {
			finishGatewayEarlyFailure("", fasthttp.StatusBadRequest)
			ctx.Error(`{"error":"invalid request body"}`, fasthttp.StatusBadRequest)
			return
		}
		req.Model = modelFromRequestPath(path, req.Model)
		if strings.Contains(path, ":streamGenerateContent") {
			req.Stream = true
		}
	}
	if req.Model == "" {
		finishGatewayEarlyFailure("", fasthttp.StatusBadRequest)
		ctx.Error(`{"error":"model is required"}`, fasthttp.StatusBadRequest)
		return
	}
	if gatewayAuthenticated && internalClaims.Model != req.Model {
		finishGatewayEarlyFailure(req.Model, fasthttp.StatusUnauthorized)
		ctx.Error(`{"error":"gateway model mismatch"}`, fasthttp.StatusUnauthorized)
		return
	}
	if !gatewayAuthenticated && token.Models != "" && !modelInList(req.Model, token.Models) {
		ctx.Error(`{"error":"model not allowed for token"}`, fasthttp.StatusForbidden)
		return
	}
	permission := permissionForRequest(path, clientFormat)
	if !gatewayAuthenticated && token.Permissions != "" && !permissionInList(permission, token.Permissions) {
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
		finishGatewayEarlyFailure(req.Model, fasthttp.StatusNotFound)
		ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusNotFound)
		return
	}

	// 7. Pre-consume billing
	estimatedTokens := req.MaxTokens
	if estimatedTokens == 0 {
		estimatedTokens = req.MaxOutputTokens
	}
	if estimatedTokens == 0 {
		estimatedTokens = req.MaxCompletionTokens
	}
	if estimatedTokens == 0 {
		estimatedTokens = req.GenerationConfig.MaxOutputTokens
	}
	if estimatedTokens == 0 {
		estimatedTokens = 1000
	}
	if gatewayAuthenticated && internalClaims.EstimatedTokens > 0 {
		estimatedTokens = internalClaims.EstimatedTokens
	}
	tokenPlanID := toUUID(internalClaims.TokenPlanID)
	if r.billing != nil && (!gatewayAuthenticated || !internalClaims.Precharged) {
		planID, err := r.billing.PreConsume(token.ID.String(), req.Model, estimatedTokens)
		if err != nil {
			logger.Warnf("relay.billing", "pre-consume failed", logger.F("token_id", token.ID.String()), logger.Err(err))
			ctx.Error(`{"error":"pre-consume failed"}`, fasthttp.StatusTooManyRequests)
			return
		}
		tokenPlanID = planID
	}
	// Determine upstream format from channel type
	var upstreamFormat provider.Format
	switch targetChannel.Type {
	case "openai":
		if targetChannel.APIFormat == "responses" || targetChannel.APIFormat == "codex" {
			upstreamFormat = provider.FormatOpenAIResponses
		} else {
			upstreamFormat = provider.FormatOpenAIChatCompletions
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
		upstreamFormat = provider.FormatOpenAIChatCompletions
	}

	sameFormat := clientFormat == upstreamFormat
	rawGeminiSameFormat := sameFormat && clientFormat == provider.FormatGemini
	if !rawGeminiSameFormat {
		body = injectModelIfMissing(body, req.Model)
	}

	forceStreamActive := targetChannel.ForceStream && !req.Stream
	effectiveStream := req.Stream || forceStreamActive
	if effectiveStream && (!sameFormat || forceStreamActive) && !rawGeminiSameFormat {
		body = injectStreamTrue(body)
	}

	// 8. Build upstream request
	adaptor.Init(targetChannel, account)
	adaptor.SetRequestParams(req.Model, effectiveStream)
	upstreamURL, err := adaptor.GetRequestURL(path)
	if err != nil {
		go r.finishFailureUsageWithError(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusInternalServerError, estimatedTokens, "build url failed", tokenPlanID)
		ctx.Error(`{"error":"build url failed"}`, fasthttp.StatusInternalServerError)
		return
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

	if isImageRequest {
		streaming = true // image handler owns concurrency release on all paths
		r.handleImageRequest(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, body, creds, req.Model, start, estimatedTokens, claims)
		return
	}

	convertedBody := body
	if !sameFormat {
		var err error
		convertedBody, err = provider.ConvertRequestWithAdaptor(clientFormat, upstreamFormat, body, adaptor)
		if err != nil {
			go r.finishFailureUsageWithError(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusBadRequest, estimatedTokens, err.Error(), tokenPlanID)
			ctx.Error(`{"error":"convert request failed: `+jsonEscape(err.Error())+`"}`, fasthttp.StatusBadRequest)
			return
		}
	}

	// 9. Dispatch
	if req.Stream && !forceStreamActive {
		streaming = true // goroutine handles Release
		r.handleStreaming(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	} else if forceStreamActive {
		streaming = true
		r.handleForceStream(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	} else {
		streaming = true // handleBuffered manages its own concurrency release
		r.handleBuffered(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	}

	// 10. Record affinity for non-streaming paths (handleBuffered + handleForceStream are synchronous)
	if !req.Stream && targetChannel.AffinityTTL > 0 && ctx.Response.StatusCode() < 400 {
		r.affinity.Set(token.ID.String(), req.Model, targetChannel.ID.String(), targetChannel.AffinityTTL)
	}
}

// handleStreaming: real-time chunk-by-chunk forwarding using SSEStreamReader.
func (r *Relayer) handleStreaming(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
		return
	}

	// streamingClient returns after receiving headers, body streamed via BodyStream
	if err := streamingClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.upstream", "streaming request failed", logger.Err(err))
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		bodyCopy := readUpstreamErrorBody(upResp)
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, false, start, clientFormat, claims, tokenPlanID)
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

	var inputConvert func([]byte) []byte
	if clientFormat == upstreamFormat {
		inputConvert = nil
	} else if upstreamFormat != provider.FormatOpenAIChatCompletions {
		if upstreamFormat == provider.FormatOpenAIResponses {
			inputConvert = openai.NewResponsesToChatStreamConverter()
		} else {
			inputConvert = adaptor.ConvertStreamLine
		}
	}

	var outputConvert func([]byte) []byte
	if clientFormat != upstreamFormat {
		outputConvert = reverseStreamConverterForClient(clientFormat, upstreamFormat)
	}
	if outputConvert == nil && clientFormat == provider.FormatOpenAIChatCompletions && clientFormat != upstreamFormat {
		outputConvert = openai.NewChatStreamNormalizer()
	}
	sendDone := clientFormat == provider.FormatOpenAIChatCompletions

	// Producer goroutine: owns upReq/upResp lifecycle, releases when done
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Default().Panic("relay.stream", "stream goroutine panic", rec)
				if r.billing != nil {
					go r.billing.DBTransactionRefund(token.ID.String(), tokenPlanID, estTokens)
				}
			}
		}()
		defer fasthttp.ReleaseRequest(upReq)
		defer fasthttp.ReleaseResponse(upResp)
		defer r.releaseLocalConcurrency(token.ID.String(), claims)

		result := streamAndForward(upResp.BodyStream(), reader, tracker, inputConvert, outputConvert, sendDone)
		if result.err != nil {
			logger.Warnf("relay.stream", "forward failed", logger.Err(result.err))
			go r.finishFailureUsageWithError(claims, token.ID, ch.ID, acc.ID, model, true, start, fasthttp.StatusBadGateway, estTokens, result.err.Error(), tokenPlanID)
			return
		}
		if result.failed {
			logger.Warnf("relay.stream", "upstream stream reported failure")
			go r.finishFailureUsageWithError(claims, token.ID, ch.ID, acc.ID, model, true, start, fasthttp.StatusBadGateway, estTokens, "upstream stream reported failure", tokenPlanID)
			return
		}
		if !result.finalized {
			logger.Warnf("relay.stream", "stream ended without terminal event")
			go r.finishFailureUsageWithError(claims, token.ID, ch.ID, acc.ID, model, true, start, fasthttp.StatusBadGateway, estTokens, "stream ended without terminal event", tokenPlanID)
			return
		}
		{
			// Record affinity only on successful stream completion
			if ch.AffinityTTL > 0 {
				r.affinity.Set(token.ID.String(), model, ch.ID.String(), ch.AffinityTTL)
			}
		}
		pt, ct, parseFailed := tracker.Result()
		if parseFailed {
			logger.Component("relay.billing").Warn("ParseStreamUsage had errors during streaming, token counts may be inaccurate",
				logger.F("token_id", token.ID.String()),
				logger.F("model", model),
				logger.F("prompt_tokens", pt),
				logger.F("completion_tokens", ct),
			)
		}
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
		go r.finishUsage(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, true, pt, ct, start, statusCode, estTokens)
	}()
}

// handleForceStream: stream to upstream, buffer all, convert to non-stream for downstream.
func (r *Relayer) handleForceStream(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Default().Panic("relay.stream", "force stream panic", rec)
			r.refundAndError(ctx, token.ID.String(), estTokens, "internal error", claims, ch, acc, model, start, tokenPlanID)
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
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
		return
	}

	if err := streamingClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.upstream", "force stream request failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		bodyCopy := readUpstreamErrorBody(upResp)
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, false, start, clientFormat, claims, tokenPlanID)
		return
	}

	// Buffer entire stream (bounded by maxResponseSize)
	respBody, err := io.ReadAll(io.LimitReader(upResp.BodyStream(), int64(maxResponseSize)))
	if err != nil {
		logger.Warnf("relay.upstream", "force stream read failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "read upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}

	// Convert upstream SSE to OpenAI Chat Completions API SSE format before StreamToNonStream.
	if upstreamFormat == provider.FormatOpenAIResponses {
		convert := openai.NewResponsesToChatStreamConverter()
		respBody = convertSSEBufferWithConverter(respBody, convert)
	} else if upstreamFormat != provider.FormatOpenAIChatCompletions {
		respBody = adaptor.ConvertSSEBuffer(respBody)
	}

	// SSE -> non-stream JSON (produces OpenAI Chat Completions API format)
	var complete bool
	respBody, complete = StreamToNonStreamChecked(respBody)
	if isOpenAIErrorResponse(respBody) {
		r.refundOnError(ctx, token.ID.String(), estTokens, fasthttp.StatusBadGateway, respBody, ch, acc, model, false, start, clientFormat, claims, tokenPlanID)
		return
	}
	if !complete {
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "stream ended without terminal event", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}

	// Parse usage from OpenAI JSON BEFORE client-format conversion
	pt, ct := parseNonStreamUsage(respBody)

	// Convert from OpenAI JSON to client format if needed
	if clientFormat != provider.FormatOpenAIChatCompletions {
		if converted, err := provider.ConvertResponse(provider.FormatOpenAIChatCompletions, clientFormat, respBody); err != nil {
			logger.Warnf("relay.convert", "response conversion failed", logger.Err(err))
			r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "response conversion failed", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
			return
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
	go r.finishUsage(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, false, pt, ct, start, statusCode, estTokens)
	r.releaseLocalConcurrency(token.ID.String(), claims)
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

func (r *Relayer) handleImageRequest(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, start time.Time, estTokens int, claims *internalauth.Claims) {
	if ch.Type != "openai" {
		r.refundAndError(ctx, token.ID.String(), estTokens, "image generation is only available on OpenAI-compatible image channels", claims, ch, acc, model, start, tokenPlanID)
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
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
		return
	}
	if err := bufferedClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.images", "upstream image request failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}
	statusCode := upResp.StatusCode()
	respBody := copyBody(upResp)
	if statusCode >= 400 {
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, acc, model, false, start, provider.FormatOpenAIResponses, claims, tokenPlanID)
		return
	}
	copyHeaders(upResp, &ctx.Response.Header)
	ctx.SetStatusCode(statusCode)
	ctx.SetBody(respBody)
	r.releaseLocalConcurrency(token.ID.String(), claims)
	logger.Debugf("relay.images", "image request completed", logger.F("token_id", token.ID.String()), logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("model", model), logger.F("status", statusCode), logger.F("latency_ms", time.Since(start).Milliseconds()))
	go r.finishUsage(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, false, 0, estTokens, start, statusCode, estTokens)
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
func (r *Relayer) handleBuffered(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
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
			r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, currentAccount, model, start, tokenPlanID)
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
		r.releaseLocalConcurrency(token.ID.String(), claims)
		go r.finishFailureUsageWithError(claims, token.ID, ch.ID, respAccount.ID, model, false, start, fasthttp.StatusServiceUnavailable, estTokens, "all retries exhausted", tokenPlanID)
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
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, respAccount, model, false, start, clientFormat, claims, tokenPlanID)
			return
		}
	}

	upstreamRespBody := respBody

	// Response format normalization/conversion. Same-format buffered responses
	// keep the upstream standard JSON intact to preserve provider-native fields.
	if upstreamFormat != clientFormat {
		if converted, err := provider.ConvertResponse(upstreamFormat, clientFormat, respBody); err != nil {
			logger.Warnf("relay.convert", "response conversion failed", logger.Err(err))
			r.finishFailedBuffered(ctx, token.ID.String(), estTokens, "response conversion failed", claims, ch, respAccount, model, start, fasthttp.StatusBadGateway, tokenPlanID)
			ctx.Error(`{"error":"response conversion failed"}`, fasthttp.StatusBadGateway)
			return
		} else {
			respBody = converted
		}
	}

	// Parse usage from upstream-format response after conversion succeeds.
	pt, ct := 0, 0
	if claims != nil && claims.RequestID != "" {
		if parsedPT, parsedCT, err := adaptor.ParseUsage(upstreamRespBody); err == nil {
			pt, ct = parsedPT, parsedCT
		}
	} else {
		pt, ct = r.settleAndRefund(token.ID.String(), tokenPlanID, upstreamRespBody, adaptor, estTokens, model)
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
		// Gateway-authenticated requests did not acquire the Relay-local
		// concurrency limiter; Gateway owns that slot and releases it when this
		// response completes.
		go r.finishUsage(claims, token.ID, tokenPlanID, ch.ID, respAccount.ID, model, false, pt, ct, start, statusCode, estTokens)
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
	text = redactLogSecrets(text)
	if text == "" {
		return "empty response"
	}
	if len(text) > 1200 {
		return text[:1200] + "..."
	}
	return text
}

func redactLogSecrets(text string) string {
	for _, key := range []string{"access_token", "refresh_token", "id_token", "client_secret", "authorization", "api_key", "apikey", "key"} {
		lowerKey := strings.ToLower(key)
		searchFrom := 0
		for searchFrom < len(text) {
			lower := strings.ToLower(text[searchFrom:])
			idx := strings.Index(lower, lowerKey)
			if idx < 0 {
				break
			}
			idx += searchFrom // re-anchor to full string

			sepIdx := -1
			for i := idx + len(key); i < len(text); i++ {
				if text[i] == ' ' || text[i] == '\t' || text[i] == '"' || text[i] == '\'' {
					continue
				}
				if text[i] == ':' || text[i] == '=' || text[i] == '&' {
					sepIdx = i
				}
				break
			}
			if sepIdx < 0 {
				searchFrom = idx + 1
				continue
			}
			start := sepIdx + 1
			for start < len(text) && (text[start] == ' ' || text[start] == '\t' || text[start] == '"' || text[start] == '\'') {
				start++
			}
			end := start
			for end < len(text) && text[end] != ',' && text[end] != '}' && text[end] != '"' && text[end] != '\'' && text[end] != '&' {
				end++
			}
			if end <= start {
				searchFrom = idx + 1
				continue
			}
			if strings.HasPrefix(text[start:], "[redacted]") {
				// Already redacted; skip past this occurrence
				searchFrom = end
				continue
			}
			text = text[:start] + "[redacted]" + text[end:]
			searchFrom = start + len("[redacted]")
		}
	}
	return text
}

func readUpstreamErrorBody(resp *fasthttp.Response) []byte {
	if stream := resp.BodyStream(); stream != nil {
		body, err := io.ReadAll(io.LimitReader(stream, maxResponseSize))
		if closer, ok := stream.(io.Closer); ok {
			_ = closer.Close()
		}
		if err == nil {
			return body
		}
		logger.Warnf("relay.upstream", "read streaming error body failed", logger.Err(err))
	}
	body := resp.Body()
	bodyCopy := make([]byte, len(body))
	copy(bodyCopy, body)
	return bodyCopy
}

func convertSSEBufferWithConverter(sseBody []byte, convert func([]byte) []byte) []byte {
	if convert == nil {
		return sseBody
	}
	lines := strings.Split(string(sseBody), "\n")
	var out []byte
	var event []byte
	flush := func() {
		if len(event) == 0 {
			return
		}
		if len(event) < 2 || string(event[len(event)-2:]) != "\n\n" {
			event = append(event, '\n')
		}
		normalized := normalizeSSEEventForConverterWithEvent(event)
		event = nil
		if len(normalized) == 0 {
			return
		}
		if converted := convert(normalized); converted != nil {
			out = append(out, converted...)
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		event = append(event, []byte(line)...)
		event = append(event, '\n')
	}
	flush()
	if len(out) > 0 && !strings.Contains(string(out), "data: [DONE]") {
		out = append(out, []byte("data: [DONE]\n\n")...)
	}
	return out
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
				if channelSupportsModel(ch, model) {
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
			if channelSupportsModel(channels[i], model) {
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
		if channelSupportsModel(channels[i], model) {
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
		if !channelSupportsModel(*ch, model) {
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
	if !channelSupportsModel(ch, model) {
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
			ChannelID: acc.ChannelID, Name: acc.Name, Credentials: acc.Credentials, CredType: acc.CredType, Endpoint: acc.Endpoint,
			Weight: acc.Weight, Enabled: acc.Enabled, CooldownUntil: acc.CooldownUntil, RefreshToken: acc.RefreshToken,
			TokenExpiry: acc.TokenExpiry, ClientID: acc.ClientID, ClientSecret: acc.ClientSecret, TokenURL: acc.TokenURL,
			Metadata: acc.Metadata,
		}
	}
	r.runtimeMu.Lock()
	if r.db == nil {
		for id, incoming := range accounts {
			if existing, ok := r.runtimeAccounts[id]; ok {
				accounts[id] = preserveFresherRuntimeOAuth(incoming, existing)
			}
		}
	}
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

func preserveFresherRuntimeOAuth(incoming, existing db.Account) db.Account {
	if incoming.CredType != "oauth_token" || existing.CredType != "oauth_token" {
		return incoming
	}
	if incoming.TokenURL != existing.TokenURL || incoming.ChannelID != existing.ChannelID {
		return incoming
	}
	if existing.TokenExpiry == nil {
		return incoming
	}
	if incoming.TokenExpiry != nil && !existing.TokenExpiry.After(*incoming.TokenExpiry) {
		return incoming
	}
	incoming.Credentials = existing.Credentials
	incoming.RefreshToken = existing.RefreshToken
	incoming.TokenExpiry = existing.TokenExpiry
	incoming.Metadata = existing.Metadata
	return incoming
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
	before := oauthAccountSyncSnapshot(account)
	credential, err := EnsureValidCredentials(account, r.db)
	if err == nil && r.db == nil && account != nil && oauthAccountChanged(before, account) {
		r.runtimeMu.Lock()
		if _, ok := r.runtimeAccounts[account.ID]; ok {
			r.runtimeAccounts[account.ID] = *account
		}
		r.runtimeMu.Unlock()
		r.pushRuntimeAccountUpdate(account)
	}
	return credential, err
}

type oauthSyncSnapshot struct {
	credentials  string
	refreshToken string
	expiry       string
	metadata     string
}

func oauthAccountSyncSnapshot(account *db.Account) oauthSyncSnapshot {
	if account == nil || account.CredType != "oauth_token" {
		return oauthSyncSnapshot{}
	}
	expiry := ""
	if account.TokenExpiry != nil {
		expiry = account.TokenExpiry.UTC().Format(time.RFC3339Nano)
	}
	metadata := ""
	if account.Metadata != nil {
		if b, err := json.Marshal(account.Metadata); err == nil {
			metadata = string(b)
		}
	}
	return oauthSyncSnapshot{
		credentials:  account.Credentials,
		refreshToken: account.RefreshToken,
		expiry:       expiry,
		metadata:     metadata,
	}
}

func oauthAccountChanged(before oauthSyncSnapshot, account *db.Account) bool {
	if account == nil || account.CredType != "oauth_token" {
		return false
	}
	return before != oauthAccountSyncSnapshot(account)
}

func (r *Relayer) pushRuntimeAccountUpdate(account *db.Account) {
	if account == nil || account.CredType != "oauth_token" || r.controlURL == "" {
		return
	}
	payload := map[string]interface{}{
		"account_id":    account.ID,
		"channel_id":    account.ChannelID,
		"credentials":   account.Credentials,
		"refresh_token": account.RefreshToken,
		"token_expiry":  account.TokenExpiry,
		"metadata":      account.Metadata,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf("relay.config", "encode account update failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		return
	}
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)
	req.SetRequestURI(r.controlURL + "/internal/relay/account-update")
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/json")
	req.Header.Set("X-UAPI-Internal-Secret", r.internalSecret)
	req.SetBody(body)
	if err := bufferedClient.DoTimeout(req, resp, 10*time.Second); err != nil {
		logger.Warnf("relay.config", "account update push failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		return
	}
	if resp.StatusCode() >= 300 {
		logger.Warnf("relay.config", "account update push rejected", logger.F("account_id", account.ID.String()), logger.F("status", resp.StatusCode()))
		return
	}
	var result struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		logger.Warnf("relay.config", "account update push returned invalid response", logger.F("account_id", account.ID.String()), logger.Err(err))
		return
	}
	if !result.Accepted {
		logger.Warnf("relay.config", "account update push not accepted", logger.F("account_id", account.ID.String()), logger.F("reason", result.Reason))
	}
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

func (r *Relayer) refundAndError(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time, tokenPlanIDs ...uuid.UUID) {
	r.refundAndErrorWithStatus(ctx, tokenID, estTokens, msg, claims, ch, acc, model, start, fasthttp.StatusInternalServerError, tokenPlanIDs...)
}

func (r *Relayer) refundAndErrorWithStatus(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time, statusCode int, tokenPlanIDs ...uuid.UUID) {
	r.releaseLocalConcurrency(tokenID, claims)
	go r.finishFailureUsageWithError(claims, tokenID, ch.ID, acc.ID, model, false, start, statusCode, estTokens, msg, tokenPlanIDs...)
	ctx.Error(`{"error":"`+jsonEscape(msg)+`"}`, statusCode)
}

func (r *Relayer) finishFailedBuffered(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time, statusCode int, tokenPlanIDs ...uuid.UUID) {
	r.releaseLocalConcurrency(tokenID, claims)
	go r.finishFailureUsageWithError(claims, tokenID, ch.ID, acc.ID, model, false, start, statusCode, estTokens, msg, tokenPlanIDs...)
}

// normalizeErrorResponse converts an upstream error body into the client's expected format.
func normalizeErrorResponse(respBody []byte, clientFormat provider.Format, statusCode int) []byte {
	errMsg := errorMessageFromResponse(respBody)
	switch clientFormat {
	case provider.FormatAnthropic:
		return formatAnthropicError(errMsg)
	case provider.FormatGemini:
		return formatGeminiError(errMsg, statusCode)
	default: // OpenAI Chat Completions API / OpenAI Responses API
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

func formatGeminiError(msg string, statusCode int) []byte {
	result, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    statusCode,
			"message": msg,
			"status":  geminiStatusForHTTP(statusCode),
		},
	})
	return result
}

func geminiStatusForHTTP(statusCode int) string {
	switch statusCode {
	case fasthttp.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case fasthttp.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case fasthttp.StatusForbidden:
		return "PERMISSION_DENIED"
	case fasthttp.StatusNotFound:
		return "NOT_FOUND"
	case fasthttp.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case fasthttp.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case fasthttp.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		if statusCode >= 500 {
			return "INTERNAL"
		}
		return "UNKNOWN"
	}
}

func (r *Relayer) refundOnError(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, statusCode int, respBody []byte, ch *db.Channel, acc *db.Account, model string, isStream bool, start time.Time, clientFormat provider.Format, claims *internalauth.Claims, tokenPlanIDs ...uuid.UUID) {
	r.releaseLocalConcurrency(tokenID, claims)
	ctx.SetStatusCode(statusCode)
	normalizedBody := normalizeErrorResponse(respBody, clientFormat, statusCode)
	ctx.SetBody(normalizedBody)
	go r.finishFailureUsageWithError(claims, tokenID, ch.ID, acc.ID, model, isStream, start, statusCode, estTokens, errorMessageFromResponse(respBody), tokenPlanIDs...)
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

func isOpenAIErrorResponse(respBody []byte) bool {
	var obj map[string]interface{}
	if json.Unmarshal(respBody, &obj) != nil {
		return false
	}
	if object, _ := obj["object"].(string); object == "error" {
		return true
	}
	_, ok := obj["error"].(map[string]interface{})
	return ok
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
	if key := strings.TrimSpace(string(ctx.QueryArgs().Peek("key"))); key != "" {
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
// Only trusts X-Forwarded-For / X-Real-IP when the direct connection IP
// matches a configured trusted proxy.
func checkIPWhitelist(ctx *fasthttp.RequestCtx, whitelist string, trustedProxies []string) bool {
	for _, allowedIP := range strings.Split(whitelist, ",") {
		allowedIP = strings.TrimSpace(allowedIP)
		if allowedIP == "" {
			continue
		}
		for _, clientIP := range clientIPCandidates(ctx, trustedProxies) {
			if allowedIP == clientIP {
				return true
			}
		}
	}
	return false
}

// clientIPCandidates returns candidate client IPs for whitelist matching.
// Forwarded headers are only considered when the direct connection IP is a trusted proxy.
func clientIPCandidates(ctx *fasthttp.RequestCtx, trustedProxies []string) []string {
	remoteIP := ctx.RemoteIP().String()
	candidates := []string{remoteIP}

	// Only trust forwarded headers if the direct connection is from a trusted proxy
	if !isTrustedProxy(remoteIP, trustedProxies) {
		return candidates
	}

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

// isTrustedProxy checks if the given IP is in the trusted proxies list.
func isTrustedProxy(ip string, trustedProxies []string) bool {
	for _, trusted := range trustedProxies {
		if strings.TrimSpace(trusted) == ip {
			return true
		}
	}
	return false
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

func reverseStreamConverterForClient(clientFormat, upstreamFormat provider.Format) func([]byte) []byte {
	switch clientFormat {
	case provider.FormatOpenAIResponses:
		return openai.NewResponsesReverseStreamConverter()
	case provider.FormatAnthropic:
		return anthropic.NewReverseStreamConverter()
	case provider.FormatGemini:
		return gemini.NewReverseStreamConverter()
	default:
		return nil
	}
}

func modelInList(model, list string) bool {
	if strings.TrimSpace(list) == "" {
		return true
	}
	for _, m := range strings.Split(list, ",") {
		if strings.TrimSpace(m) == model {
			return true
		}
	}
	return false
}

func channelSupportsModel(ch db.Channel, model string) bool {
	if strings.TrimSpace(ch.Models) == "" && isCodeAPIFormat(ch.APIFormat) {
		return false
	}
	return modelInList(model, ch.Models)
}

func isCodeAPIFormat(format string) bool {
	return format == "codex" || format == "gemini_code" || format == "claude_code"
}

func permissionForFormat(format provider.Format) string {
	switch format {
	case provider.FormatAnthropic:
		return "messages"
	case provider.FormatGemini:
		return "gemini"
	case provider.FormatOpenAIResponses:
		return "responses"
	default:
		return "chat"
	}
}

func permissionForRequest(path string, format provider.Format) string {
	if strings.HasPrefix(path, "/v1/images/") {
		return "images"
	}
	return permissionForFormat(format)
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
		ErrorMessage:     logger.Redact(errorMessage),
	}
	if err := r.db.Create(&logEntry).Error; err != nil {
		logger.Warnf("relay.logs", "write request log failed", logger.Err(err))
	}
}

func (r *Relayer) finishUsage(claims *internalauth.Claims, tokenID, tokenPlanID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, estTokens int) {
	planID := requestTokenPlanID(claims, tokenPlanID)
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, isStream, pt, ct, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLog(tokenID, channelID, accountID, model, isStream, pt, ct, start, statusCode)
	if r.billing != nil {
		go func() {
			if err := r.billing.DBTransactionRefundAndSettle(toUUID(tokenID).String(), planID, estTokens, pt, ct, 0, 0, model); err != nil {
				logger.Component("relay.billing").Warn("refund-and-settle failed",
					logger.F("token_id", toUUID(tokenID).String()),
					logger.F("error", err.Error()),
				)
			}
		}()
	}
}

func (r *Relayer) finishFailureUsage(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, start time.Time, statusCode int, estTokens int) {
	r.finishFailureUsageWithError(claims, tokenID, channelID, accountID, model, isStream, start, statusCode, estTokens, "")
}

func (r *Relayer) finishFailureUsageWithError(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, start time.Time, statusCode int, estTokens int, errorMessage string, tokenPlanIDs ...uuid.UUID) {
	planID := requestTokenPlanID(claims, firstUUID(tokenPlanIDs))
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, isStream, 0, 0, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLogWithError(tokenID, channelID, accountID, model, isStream, 0, 0, start, statusCode, errorMessage)
	if r.billing != nil {
		go func() {
			if err := r.billing.DBTransactionRefund(toUUID(tokenID).String(), planID, estTokens); err != nil {
				logger.Component("relay.billing").Warn("refund failed",
					logger.F("token_id", toUUID(tokenID).String()),
					logger.F("error", err.Error()),
				)
			}
		}()
	}
}

func (r *Relayer) reportUsageEvent(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, estTokens int) error {
	if r.controlURL == "" {
		return fmt.Errorf("control url not configured")
	}
	payload := map[string]interface{}{
		"request_id":        claims.RequestID,
		"token_id":          toUUID(tokenID),
		"token_plan_id":     toUUID(claims.TokenPlanID),
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

func (r *Relayer) settleAndRefund(tokenID string, tokenPlanID uuid.UUID, respBody []byte, adaptor provider.Adaptor, estTokens int, model string) (int, int) {
	if r.billing == nil {
		return 0, 0
	}
	pt, ct, cc, cr := 0, 0, 0, 0
	if adaptor != nil && len(respBody) > 0 {
		if usage, err := adaptor.ParseUsageFull(respBody); err == nil {
			pt, ct = usage.PromptTokens, usage.CompletionTokens
			cc, cr = usage.CacheCreationInputTokens, usage.CacheReadInputTokens
		} else {
			logger.Component("relay.billing").Warn("ParseUsage failed, recording zero tokens",
				logger.F("token_id", tokenID),
				logger.F("model", model),
				logger.F("error", err.Error()),
			)
		}
	}
	go func() {
		if err := r.billing.DBTransactionRefundAndSettle(tokenID, tokenPlanID, estTokens, pt, ct, cc, cr, model); err != nil {
			logger.Component("relay.billing").Warn("refund-and-settle failed",
				logger.F("token_id", tokenID),
				logger.F("error", err.Error()),
			)
		}
	}()
	return pt, ct
}

func requestTokenPlanID(claims *internalauth.Claims, fallback interface{}) uuid.UUID {
	if id := toUUID(fallback); id != uuid.Nil {
		return id
	}
	if claims != nil {
		return toUUID(claims.TokenPlanID)
	}
	return uuid.Nil
}

func firstUUID(ids []uuid.UUID) uuid.UUID {
	if len(ids) > 0 {
		return ids[0]
	}
	return uuid.Nil
}

func (r *Relayer) releaseLocalConcurrency(tokenID string, claims *internalauth.Claims) {
	if claims != nil && claims.RequestID != "" {
		return
	}
	r.concLimiter.Release(tokenID)
}
