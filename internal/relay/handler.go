package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/httputil"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/modelalias"
	"github.com/AutoCONFIG/uapi/internal/quota"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
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

const defaultStreamIdleTimeout = 300 * time.Second

type Relayer struct {
	db                *gorm.DB
	pools             *PoolManager
	billing           *BillingService
	affinity          *AffinityCache
	concLimiter       *ConcurrencyLimiter
	chCache           *channelCache
	internalSecret    string
	requireInternal   bool
	controlURL        string
	trustedProxies    []string
	streamIdleTimeout time.Duration
	runtimeMu         sync.RWMutex
	runtimeVersion    int64
	runtimeChannels   map[uuid.UUID]db.Channel
	runtimeAccounts   map[uuid.UUID]db.Account
	quotaScheduler    *quota.Scheduler
	oauthRefreshHook  func(uuid.UUID)
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

func WithStreamIdleTimeout(timeout time.Duration) RelayerOption {
	return func(r *Relayer) {
		if timeout > 0 {
			r.streamIdleTimeout = timeout
		}
	}
}

func NewRelayer(database *gorm.DB, pools *PoolManager, billing *BillingService, affinity *AffinityCache, concLimit int, internalSecret string, requireInternal bool, controlURL string, opts ...RelayerOption) *Relayer {
	r := &Relayer{
		db:                database,
		pools:             pools,
		billing:           billing,
		affinity:          affinity,
		concLimiter:       NewConcurrencyLimiter(concLimit),
		chCache:           newChannelCache(database, 30*time.Second),
		internalSecret:    internalSecret,
		requireInternal:   requireInternal,
		controlURL:        strings.TrimRight(controlURL, "/"),
		streamIdleTimeout: defaultStreamIdleTimeout,
		runtimeChannels:   make(map[uuid.UUID]db.Channel),
		runtimeAccounts:   make(map[uuid.UUID]db.Account),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Relayer) SetQuotaScheduler(s *quota.Scheduler) {
	r.quotaScheduler = s
}

func (r *Relayer) SetOAuthRefreshHook(hook func(uuid.UUID)) {
	r.oauthRefreshHook = hook
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
	requestType := detectRelayRequestType(path)

	// Detect client format from request path
	clientFormat := requestType.clientFormat()

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
		tokenKey := httputil.ExtractBearerToken(ctx, true)
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
		if token.IPWhitelist != "" && !httputil.CheckIPWhitelist(ctx, token.IPWhitelist, r.trustedProxies) {
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
			status := fasthttp.StatusTooManyRequests
			if errors.Is(err, ErrNoActiveSubscription) {
				status = fasthttp.StatusPaymentRequired
			}
			ctx.Error(`{"error":"`+httputil.JSONEscape(err.Error())+`"}`, status)
			return
		}
		// Check user status and require an active plan for user-linked tokens.
		if token.UserID != "" {
			if err := r.billing.CheckUserPlan(token.UserID, token.ID.String()); err != nil {
				ctx.Error(`{"error":"`+httputil.JSONEscape(err.Error())+`"}`, 402)
				return
			}
		}
	}

	// 5. Parse request
	var req relayRequest
	body := ctx.PostBody()
	isMediaRequest := requestType.isMedia()
	if !isMediaRequest {
		body = cleanJSONUndefinedPlaceholders(body)
	}
	if isMediaRequest {
		req.Model = httputil.ModelFromBodyOrForm(ctx)
		if req.Model == "" && requestType.isImage() {
			req.Model = "gpt-image-1"
		}
	} else {
		if err := json.Unmarshal(body, &req); err != nil {
			finishGatewayEarlyFailure("", fasthttp.StatusBadRequest)
			ctx.Error(`{"error":"invalid request body"}`, fasthttp.StatusBadRequest)
			return
		}
		req.Model = httputil.ModelFromRequestPath(path, req.Model)
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
	if !gatewayAuthenticated {
		allowedModels := token.Models
		if r.billing != nil {
			policy, hasPolicy, err := r.loadRelayPolicy(token)
			if err != nil {
				ctx.Error(`{"error":"`+httputil.JSONEscape(err.Error())+`"}`, fasthttp.StatusForbidden)
				return
			}
			if hasPolicy {
				allowedModels = policy.AllowedModels
			}
		}
		if allowedModels != "" && !modelInList(req.Model, allowedModels) {
			ctx.Error(`{"error":"model not allowed for token"}`, fasthttp.StatusForbidden)
			return
		}
	}
	permission := permissionForRequest(path, clientFormat)
	if !gatewayAuthenticated && token.Permissions != "" && !httputil.PermissionInList(permission, token.Permissions) {
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
		ctx.Error(`{"error":"`+httputil.JSONEscape(err.Error())+`"}`, fasthttp.StatusNotFound)
		return
	}
	upstreamModel := modelalias.UpstreamName(req.Model, targetChannel.ModelAliases)
	if upstreamModel == "" {
		upstreamModel = req.Model
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
	if !supportsRelayRequestType(targetChannel.Type, requestType) {
		go r.finishFailureUsageWithErrorAndClientIP(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusBadRequest, estimatedTokens, fmt.Sprintf("%s is not supported by %s channels", requestType, targetChannel.Type), httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
		ctx.Error(`{"error":"request type not supported by selected channel"}`, fasthttp.StatusBadRequest)
		return
	}
	if r.billing != nil && (!gatewayAuthenticated || !internalClaims.Precharged) {
		planID, err := r.billing.PreConsume(token.ID.String(), req.Model, estimatedTokens)
		if err != nil {
			logger.Warnf("relay.billing", "pre-consume failed", logger.F("token_id", token.ID.String()), logger.Err(err))
			status := fasthttp.StatusTooManyRequests
			if errors.Is(err, ErrNoActiveSubscription) {
				status = fasthttp.StatusPaymentRequired
			}
			ctx.Error(`{"error":"`+httputil.JSONEscape(err.Error())+`"}`, status)
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
	case "antigravity":
		upstreamFormat = provider.FormatAntigravity
	default:
		upstreamFormat = provider.FormatOpenAIChatCompletions
	}

	sameFormat := clientFormat == upstreamFormat
	rawGeminiSameFormat := sameFormat && clientFormat == provider.FormatGemini
	if !isMediaRequest && !rawGeminiSameFormat {
		if upstreamModel != req.Model {
			body = setRequestModel(body, upstreamModel)
		} else {
			body = injectModelIfMissing(body, req.Model)
		}
	}

	forceStreamActive := (targetChannel.ForceStream || targetChannel.APIFormat == "codex") && !req.Stream
	effectiveStream := req.Stream || forceStreamActive
	if !isMediaRequest && effectiveStream && shouldInjectStreamField(clientFormat, sameFormat, forceStreamActive, rawGeminiSameFormat) {
		body = injectStreamTrue(body)
	}

	// 8. Build upstream request
	adaptor.Init(targetChannel, account)
	adaptor.SetRequestParams(upstreamModel, effectiveStream)
	upstreamURL, err := adaptor.GetRequestURL(path)
	if err != nil {
		go r.finishFailureUsageWithErrorAndClientIP(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusInternalServerError, estimatedTokens, "build url failed", httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
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

	if isMediaRequest {
		streaming = true // media handler owns concurrency release on all paths
		r.handleMediaRequest(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, body, creds, req.Model, upstreamModel, clientFormat, upstreamFormat, requestType, start, estimatedTokens, claims)
		return
	}

	convertedBody := body
	routedModel := upstreamModel
	if !sameFormat {
		var err error
		convertedBody, err = provider.ConvertRequestWithAdaptor(clientFormat, upstreamFormat, body, adaptor)
		if err != nil {
			go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, targetChannel.ID, account.ID, req.Model, routedModel, false, clientFormat, upstreamFormat, start, fasthttp.StatusBadRequest, estimatedTokens, err.Error(), httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
			ctx.Error(`{"error":"convert request failed: `+httputil.JSONEscape(err.Error())+`"}`, fasthttp.StatusBadRequest)
			return
		}
		routedModel = routedModelFromBody(convertedBody, routedModel)
	}
	if targetChannel.APIFormat == "codex" && upstreamFormat == provider.FormatOpenAIResponses {
		convertedBody = normalizeCodexResponsesRequest(convertedBody)
		routedModel = routedModelFromBody(convertedBody, routedModel)
	}
	adaptor.SetRequestParams(routedModel, effectiveStream)

	// 9. Dispatch
	if req.Stream && !forceStreamActive {
		streaming = true // goroutine handles Release
		r.handleStreaming(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, routedModel, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	} else if forceStreamActive {
		streaming = true
		r.handleForceStream(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, routedModel, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	} else {
		streaming = true // handleBuffered manages its own concurrency release
		r.handleBuffered(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, routedModel, clientFormat, upstreamFormat, start, estimatedTokens, claims)
	}

	// 10. Record affinity for non-streaming paths (handleBuffered + handleForceStream are synchronous)
	if !req.Stream && targetChannel.AffinityTTL > 0 && ctx.Response.StatusCode() < 400 {
		r.affinity.Set(token.ID.String(), req.Model, targetChannel.ID.String(), targetChannel.AffinityTTL)
	}
}

// handleStreaming: real-time chunk-by-chunk forwarding using SSEStreamReader.
func (r *Relayer) handleStreaming(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
	r.handleStreamingAttempt(ctx, token, tokenPlanID, ch, acc, adaptor, url, body, creds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false)
}

func (r *Relayer) handleStreamingAttempt(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims, authRetried bool) {
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
	if err := doStreamingRequest(upReq, upResp); err != nil {
		logger.Warnf("relay.upstream", "streaming request failed", logger.Err(err))
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		bodyCopy := readUpstreamErrorBody(upResp)
		if !authRetried {
			if refreshedCreds, ok := r.refreshOAuthCredentialsAfterAuthFailure(ch, acc, statusCode, bodyCopy); ok {
				fasthttp.ReleaseRequest(upReq)
				fasthttp.ReleaseResponse(upResp)
				r.handleStreamingAttempt(ctx, token, tokenPlanID, ch, acc, adaptor, url, body, refreshedCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, true)
				return
			}
		}
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

	inputConvert := newStreamConverterFunc(upstreamFormat, provider.FormatOpenAIChatCompletions)

	var outputConvert func([]byte) []byte
	if clientFormat != provider.FormatOpenAIChatCompletions {
		outputConvert = newStreamConverterFunc(provider.FormatOpenAIChatCompletions, clientFormat)
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

		bodyStream, stopIdleTimeout := httputil.NewIdleTimeoutReader(upResp.BodyStream(), upResp.BodyStream(), r.streamIdleTimeout)
		defer stopIdleTimeout()
		result := streamAndForward(bodyStream, reader, tracker, inputConvert, outputConvert, sendDone)
		if result.err != nil {
			logger.Warnf("relay.stream", "forward failed", logger.Err(result.err))
			if errors.Is(result.err, io.ErrClosedPipe) {
				pt, ct, _ := tracker.Result()
				estimateMissingUsage(&pt, &ct, body, nil, tracker.EstimatedOutputTokens())
				if pt > 0 || ct > 0 {
					go r.finishUsageWithRoutedModelAndFormats(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, pt, ct, start, 499, estTokens, httputil.ClientIPForLog(ctx, r.trustedProxies))
				} else {
					go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, 499, estTokens, "client disconnected", httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
				}
				return
			}
			go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, fasthttp.StatusBadGateway, estTokens, result.err.Error(), httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
			return
		}
		if result.failed {
			logger.Warnf("relay.stream", "upstream stream reported failure")
			go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, fasthttp.StatusBadGateway, estTokens, "upstream stream reported failure", httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
			return
		}
		if !result.finalized {
			logger.Warnf("relay.stream", "stream ended without terminal event")
			go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, fasthttp.StatusBadGateway, estTokens, "stream ended without terminal event", httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
			return
		}
		{
			// Record affinity only on successful stream completion
			if ch.AffinityTTL > 0 {
				r.affinity.Set(token.ID.String(), model, ch.ID.String(), ch.AffinityTTL)
			}
		}
		pt, ct, parseFailed := tracker.Result()
		estimateMissingUsage(&pt, &ct, body, nil, tracker.EstimatedOutputTokens())
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
		go r.finishUsageWithRoutedModelAndFormats(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, pt, ct, start, statusCode, estTokens, httputil.ClientIPForLog(ctx, r.trustedProxies))
	}()
}

// handleForceStream: stream to upstream, buffer all, convert to non-stream for downstream.
func (r *Relayer) handleForceStream(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
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

	if err := doStreamingRequest(upReq, upResp); err != nil {
		logger.Warnf("relay.upstream", "force stream request failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		bodyCopy := readUpstreamErrorBody(upResp)
		if refreshedCreds, ok := r.refreshOAuthCredentialsAfterAuthFailure(ch, acc, statusCode, bodyCopy); ok {
			upReq.Reset()
			upResp.Reset()
			upReq.SetRequestURI(url)
			upReq.Header.SetMethodBytes([]byte("POST"))
			upReq.SetBody(body)
			if err := adaptor.SetupRequestHeader(upReq, refreshedCreds); err != nil {
				r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
				return
			}
			if err := doStreamingRequest(upReq, upResp); err != nil {
				logger.Warnf("relay.upstream", "force stream request failed after oauth refresh", logger.Err(err))
				r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
				return
			}
			statusCode = upResp.StatusCode()
			if statusCode >= 400 {
				bodyCopy = readUpstreamErrorBody(upResp)
			}
		}
		if statusCode >= 400 {
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, false, start, clientFormat, claims, tokenPlanID)
			return
		}
	}

	// Buffer entire stream. Read one byte past the limit so oversized upstream
	// streams fail explicitly instead of being silently truncated.
	bodyStream, stopIdleTimeout := httputil.NewIdleTimeoutReader(upResp.BodyStream(), upResp.BodyStream(), r.streamIdleTimeout)
	defer stopIdleTimeout()
	respBody, err := io.ReadAll(io.LimitReader(bodyStream, int64(maxResponseSize)+1))
	if err != nil {
		logger.Warnf("relay.upstream", "force stream read failed", logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "read upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}
	if len(respBody) > maxResponseSize {
		logger.Warnf("relay.upstream", "force stream response too large", logger.F("limit", maxResponseSize))
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "upstream response too large", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}

	if convert := newStreamConverterFunc(upstreamFormat, provider.FormatOpenAIChatCompletions); convert != nil {
		respBody = convertSSEBufferWithConverter(respBody, convert)
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
	estimateMissingUsage(&pt, &ct, body, respBody, 0)

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
	go r.finishUsageWithRoutedModelAndFormats(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, false, clientFormat, upstreamFormat, pt, ct, start, statusCode, estTokens, httputil.ClientIPForLog(ctx, r.trustedProxies))
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

func (r *Relayer) handleMediaRequest(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, requestType relayRequestType, start time.Time, estTokens int, claims *internalauth.Claims) {
	if !supportsRelayRequestType(ch.Type, requestType) {
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "request type not supported by selected channel", claims, ch, acc, model, start, fasthttp.StatusBadRequest, tokenPlanID)
		return
	}
	responseFormat := imageResponseFormat(ctx, body)
	if ch.Type == "antigravity" {
		converted, err := antigravityImageBody(ctx, body, acc, model, requestType)
		if err != nil {
			r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, err.Error(), claims, ch, acc, model, start, fasthttp.StatusBadRequest, tokenPlanID)
			return
		}
		body = converted
		routedModel = routedModelFromBody(body, routedModel)
	}
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	upReq.SetRequestURI(url)
	if ch.Type == "antigravity" {
		upReq.Header.SetMethod(fasthttp.MethodPost)
	} else {
		upReq.Header.SetMethodBytes(ctx.Method())
	}
	upReq.SetBody(body)
	if ch.Type == "antigravity" {
		upReq.Header.SetContentType("application/json")
	} else if contentType := ctx.Request.Header.ContentType(); len(contentType) > 0 {
		upReq.Header.SetBytesV("Content-Type", contentType)
	}
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
		return
	}
	if err := bufferedClient.Do(upReq, upResp); err != nil {
		logger.Warnf("relay.media", "upstream media request failed", logger.F("request_type", string(requestType)), logger.Err(err))
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
		return
	}
	statusCode := upResp.StatusCode()
	respBody := copyBody(upResp)
	if statusCode >= 400 {
		if refreshedCreds, ok := r.refreshOAuthCredentialsAfterAuthFailure(ch, acc, statusCode, respBody); ok {
			upReq.Reset()
			upResp.Reset()
			upReq.SetRequestURI(url)
			if ch.Type == "antigravity" {
				upReq.Header.SetMethod(fasthttp.MethodPost)
			} else {
				upReq.Header.SetMethodBytes(ctx.Method())
			}
			upReq.SetBody(body)
			if ch.Type == "antigravity" {
				upReq.Header.SetContentType("application/json")
			} else if contentType := ctx.Request.Header.ContentType(); len(contentType) > 0 {
				upReq.Header.SetBytesV("Content-Type", contentType)
			}
			if err := adaptor.SetupRequestHeader(upReq, refreshedCreds); err != nil {
				r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
				return
			}
			if err := bufferedClient.Do(upReq, upResp); err != nil {
				logger.Warnf("relay.media", "upstream media request failed after oauth refresh", logger.F("request_type", string(requestType)), logger.Err(err))
				r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, tokenPlanID)
				return
			}
			statusCode = upResp.StatusCode()
			respBody = copyBody(upResp)
		}
	}
	if statusCode >= 400 {
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, acc, model, false, start, provider.FormatOpenAIResponses, claims, tokenPlanID)
		return
	}
	if ch.Type == "antigravity" {
		converted, err := antigravityImagesOpenAIResponse(respBody, responseFormat)
		if err != nil {
			r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "response conversion failed", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
			return
		}
		respBody = converted
		ctx.Response.Header.Set("Content-Type", "application/json")
	} else {
		copyHeaders(upResp, &ctx.Response.Header)
	}
	ctx.SetStatusCode(statusCode)
	ctx.SetBody(respBody)
	r.releaseLocalConcurrency(token.ID.String(), claims)
	logger.Debugf("relay.media", "media request completed", logger.F("token_id", token.ID.String()), logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("model", model), logger.F("request_type", string(requestType)), logger.F("status", statusCode), logger.F("latency_ms", time.Since(start).Milliseconds()))
	go r.finishUsageWithRoutedModelAndFormats(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, false, clientFormat, upstreamFormat, 0, estTokens, start, statusCode, estTokens, httputil.ClientIPForLog(ctx, r.trustedProxies))
}

func antigravityImageBody(ctx *fasthttp.RequestCtx, body []byte, acc *db.Account, model string, requestType relayRequestType) ([]byte, error) {
	switch requestType {
	case requestTypeImageGeneration:
		return antigravityImageGenerationRequest(body, acc, model)
	case requestTypeImageEdit, requestTypeImageVariation:
		return antigravityImageMultipartRequest(ctx, acc, model, requestType)
	default:
		return nil, fmt.Errorf("antigravity only supports image media requests")
	}
}

func imageResponseFormat(ctx *fasthttp.RequestCtx, body []byte) string {
	var req struct {
		ResponseFormat string `json:"response_format"`
	}
	if json.Unmarshal(body, &req) == nil && strings.TrimSpace(req.ResponseFormat) != "" {
		return strings.TrimSpace(req.ResponseFormat)
	}
	if ctx != nil {
		if value := strings.TrimSpace(string(ctx.FormValue("response_format"))); value != "" {
			return value
		}
	}
	return "b64_json"
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
func (r *Relayer) handleBuffered(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims) {
	var respBody []byte
	var statusCode int
	var respHeaders fasthttp.ResponseHeader
	respAccount := acc
	currentCreds := creds
	currentAccount := acc
	refreshedAuthAccounts := make(map[uuid.UUID]bool)

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
		} else if upResp.StatusCode() == 429 {
			// Trigger quota refresh on 429 so admin can see updated usage
			if r.quotaScheduler != nil && currentAccount != nil && ch != nil {
				r.quotaScheduler.On429(currentAccount.ID, ch.ID)
			}
			respBody429 := copyBody(upResp)
			statusCode = 429
			copyHeaders(upResp, &respHeaders)
			retryDelay := parseRetryDelay(respBody429, upstreamFormat)
			if retryDelay >= 0 && retryDelay <= 3*time.Second && retry < 2 {
				// Short delay: wait and retry same account
				logger.Infof("relay.429", "short retry delay, retrying same account", logger.F("delay", retryDelay), logger.F("retry", retry))
				time.Sleep(retryDelay)
				fasthttp.ReleaseResponse(upResp)
				continue
			}
			// Medium/long delay or unknown: switch account
			shouldRetry = true
			r.markAutoDisable(currentAccount, "quota_exhausted")
		} else if currentAccount != nil && !refreshedAuthAccounts[currentAccount.ID] && isOAuthAuthFailure(currentAccount, upResp.StatusCode(), upResp.Body()) {
			respBody = copyBody(upResp)
			statusCode = upResp.StatusCode()
			copyHeaders(upResp, &respHeaders)
			if refreshedCreds, ok := r.refreshOAuthCredentialsAfterAuthFailure(ch, currentAccount, statusCode, respBody); ok {
				refreshedAuthAccounts[currentAccount.ID] = true
				currentCreds = refreshedCreds
				fasthttp.ReleaseResponse(upResp)
				continue
			}
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
			currentCreds, err = r.ensureCredentials(ch, currentAccount)
			if err != nil {
				logger.Warnf("relay.credentials", "credential error on retry", logger.F("retry", retry), logger.Err(err))
				currentAccount = r.retryNext(ch, currentAccount)
				if currentAccount == nil {
					break
				}
				respAccount = currentAccount
				adaptor.Init(ch, currentAccount)
				currentCreds, err = r.ensureCredentials(ch, currentAccount)
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
		r.clearAutoDisable(currentAccount)
		respAccount = currentAccount
		break
	}

	if respBody == nil {
		r.releaseLocalConcurrency(token.ID.String(), claims)
		go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, ch.ID, respAccount.ID, model, routedModel, false, clientFormat, upstreamFormat, start, fasthttp.StatusServiceUnavailable, estTokens, "all retries exhausted", httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanID)
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
					routedModel = fallbackModel
				}
			}
		}
		if ch.APIFormat == "antigravity" && antigravityTierFallbackEnabled(ch) && isAntigravityTierExhausted(statusCode, respBody) {
			if retryBody, retryStatus, fallbackModel, ok := r.retryAntigravityTierFallback(ctx, ch, adaptor, url, body, currentCreds, model, &respHeaders); ok {
				respBody = retryBody
				statusCode = retryStatus
				routedModel = fallbackModel
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
	responseConverted := upstreamFormat != clientFormat
	if responseConverted {
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
		estimateMissingUsage(&pt, &ct, body, upstreamRespBody, 0)
	} else {
		pt, ct = r.settleAndRefund(token.ID.String(), tokenPlanID, body, upstreamRespBody, adaptor, estTokens, model)
	}

	ctx.SetStatusCode(statusCode)
	respHeaders.VisitAll(func(key, value []byte) {
		ctx.Response.Header.SetBytesKV(key, value)
	})
	if responseConverted {
		sanitizeConvertedResponseHeaders(&ctx.Response.Header)
	}
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
		go r.finishUsageWithRoutedModelAndFormats(claims, token.ID, tokenPlanID, ch.ID, respAccount.ID, model, routedModel, false, clientFormat, upstreamFormat, pt, ct, start, statusCode, estTokens, httputil.ClientIPForLog(ctx, r.trustedProxies))
	} else {
		go r.writeLogWithRoutedModelAndFormats(token.ID, ch.ID, respAccount.ID, model, routedModel, false, clientFormat, upstreamFormat, pt, ct, start, statusCode, httputil.ClientIPForLog(ctx, r.trustedProxies))
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

func (r *Relayer) retryAntigravityTierFallback(ctx *fasthttp.RequestCtx, ch *db.Channel, adaptor provider.Adaptor, url string, body []byte, creds string, model string, headers *fasthttp.ResponseHeader) ([]byte, int, string, bool) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, "", false
	}
	currentModel, _ := payload["model"].(string)
	settings := antigravity.DefaultChannelSettings()
	if ch != nil {
		settings = antigravity.ParseChannelSettings(ch.Settings)
	}
	for _, fallbackModel := range antigravity.FallbackUpstreamModelsWithSettings(model, currentModel, settings) {
		fallbackBody, ok := antigravityTierFallbackBody(payload, fallbackModel)
		if !ok {
			continue
		}
		upReq := fasthttp.AcquireRequest()
		upResp := fasthttp.AcquireResponse()
		upReq.SetRequestURI(url)
		upReq.Header.SetMethodBytes(ctx.Method())
		upReq.SetBody(fallbackBody)
		if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			return nil, 0, "", false
		}
		err := bufferedClient.Do(upReq, upResp)
		fasthttp.ReleaseRequest(upReq)
		if err != nil {
			fasthttp.ReleaseResponse(upResp)
			logger.Warnf("relay.antigravity", "tier fallback upstream error", logger.F("fallback_model", fallbackModel), logger.Err(err))
			continue
		}
		statusCode := upResp.StatusCode()
		respBody := copyBody(upResp)
		if headers != nil {
			copyHeaders(upResp, headers)
		}
		fasthttp.ReleaseResponse(upResp)
		if statusCode < 400 {
			logger.Infof("relay.antigravity", "tier fallback succeeded", logger.F("model", model), logger.F("fallback_model", fallbackModel))
			return respBody, statusCode, fallbackModel, true
		}
		if !isAntigravityTierExhausted(statusCode, respBody) {
			return respBody, statusCode, fallbackModel, true
		}
	}
	return nil, 0, "", false
}

func antigravityTierFallbackBody(payload map[string]interface{}, fallbackModel string) ([]byte, bool) {
	clone := make(map[string]interface{}, len(payload))
	for key, value := range payload {
		clone[key] = value
	}
	clone["model"] = fallbackModel
	clone["requestType"] = antigravityRequestTypeForRelay(fallbackModel)
	if request, ok := clone["request"].(map[string]interface{}); ok {
		requestClone := make(map[string]interface{}, len(request))
		for key, value := range request {
			requestClone[key] = value
		}
		requestClone["model"] = fallbackModel
		clone["request"] = requestClone
	}
	updated, err := json.Marshal(clone)
	return updated, err == nil
}

func antigravityTierFallbackEnabled(ch *db.Channel) bool {
	if ch == nil {
		return false
	}
	settings := antigravity.ParseChannelSettings(ch.Settings)
	return settings.ThinkingRouting && settings.TierFallback
}

func isAntigravityTierExhausted(statusCode int, body []byte) bool {
	if statusCode != fasthttp.StatusTooManyRequests && statusCode != fasthttp.StatusServiceUnavailable {
		return false
	}
	text := strings.ToUpper(string(body))
	return strings.Contains(text, "RESOURCE_EXHAUSTED") ||
		strings.Contains(text, "RESOURCE HAS BEEN EXHAUSTED") ||
		strings.Contains(text, "QUOTA") ||
		strings.Contains(text, "CAPACITY") ||
		strings.Contains(text, "NO CAPACITY")
}

func antigravityRequestTypeForRelay(model string) string {
	if strings.Contains(strings.ToLower(model), "image") {
		return "image_gen"
	}
	return "agent"
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
		creds, err := r.ensureCredentials(ch, acc)
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
	creds, err := r.ensureCredentials(&ch, &acc)
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
	creds, err := r.ensureCredentials(&ch, account)
	if err != nil {
		return nil, nil, "", fmt.Errorf("credential error: %w", err)
	}
	return account, adaptor, creds, nil
}

func (r *Relayer) ensureCredentials(ch *db.Channel, account *db.Account) (string, error) {
	before := oauthAccountSyncSnapshot(account)
	credential, err := EnsureValidCredentialsForChannel(account, ch, r.db)
	if err == nil && account != nil && oauthAccountChanged(before, account) {
		r.notifyOAuthAccountRefreshed(account.ID)
	}
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

func (r *Relayer) refreshOAuthCredentialsAfterAuthFailure(ch *db.Channel, account *db.Account, statusCode int, body []byte) (string, bool) {
	if !isOAuthAuthFailure(account, statusCode, body) {
		return "", false
	}
	before := oauthAccountSyncSnapshot(account)
	credential, err := RefreshOAuthCredentialsForChannel(account, ch, r.db)
	if err != nil {
		logger.Warnf("relay.oauth", "refresh after upstream auth failure failed", logger.F("account_id", account.ID.String()), logger.F("status", statusCode), logger.Err(err))
		return "", false
	}
	if oauthAccountChanged(before, account) {
		r.notifyOAuthAccountRefreshed(account.ID)
	}
	if r.db == nil && oauthAccountChanged(before, account) {
		r.runtimeMu.Lock()
		if _, ok := r.runtimeAccounts[account.ID]; ok {
			r.runtimeAccounts[account.ID] = *account
		}
		r.runtimeMu.Unlock()
		r.pushRuntimeAccountUpdate(account)
	}
	if ch != nil {
		logger.Infof("relay.oauth", "refreshed oauth credentials after upstream auth failure", logger.F("channel_id", ch.ID.String()), logger.F("account_id", account.ID.String()), logger.F("status", statusCode))
	}
	return credential, true
}

func (r *Relayer) notifyOAuthAccountRefreshed(accountID uuid.UUID) {
	if r.oauthRefreshHook != nil {
		r.oauthRefreshHook(accountID)
	}
}

func isOAuthAuthFailure(account *db.Account, statusCode int, body []byte) bool {
	if account == nil || account.CredType != "oauth_token" || strings.TrimSpace(account.RefreshToken) == "" {
		return false
	}
	if statusCode == fasthttp.StatusUnauthorized {
		return true
	}
	if statusCode != fasthttp.StatusForbidden {
		return false
	}
	text := strings.ToLower(string(body))
	for _, marker := range []string{
		"invalid_token",
		"expired token",
		"access token expired",
		"token expired",
		"unauthenticated",
		"unauthorized",
		"invalid authentication",
		"invalid credentials",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
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

func (r *Relayer) markAutoDisable(acc *db.Account, reason string) {
	if acc == nil {
		return
	}
	if acc.Metadata == nil {
		acc.Metadata = make(map[string]interface{})
	}
	// Only mark if not already marked or if reason is more severe
	existingReason, _ := acc.Metadata["auto_disable_reason"].(string)
	if existingReason != "" && existingReason != "quota_exhausted" {
		// Don't overwrite a more severe reason
		return
	}
	acc.Metadata["auto_disable_reason"] = reason
	acc.Metadata["auto_disable_time"] = time.Now().UTC().Format(time.RFC3339)
	r.db.Model(acc).Update("metadata", acc.Metadata)
}

func (r *Relayer) clearAutoDisable(acc *db.Account) {
	if acc == nil || acc.Metadata == nil {
		return
	}
	if _, ok := acc.Metadata["auto_disable_reason"]; !ok {
		return
	}
	delete(acc.Metadata, "auto_disable_reason")
	delete(acc.Metadata, "auto_disable_time")
	r.db.Model(acc).Update("metadata", acc.Metadata)
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
	go r.finishFailureUsageWithErrorAndClientIP(claims, tokenID, ch.ID, acc.ID, model, false, start, statusCode, estTokens, msg, httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanIDs...)
	ctx.Error(`{"error":"`+httputil.JSONEscape(msg)+`"}`, statusCode)
}

func (r *Relayer) finishFailedBuffered(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time, statusCode int, tokenPlanIDs ...uuid.UUID) {
	r.releaseLocalConcurrency(tokenID, claims)
	go r.finishFailureUsageWithErrorAndClientIP(claims, tokenID, ch.ID, acc.ID, model, false, start, statusCode, estTokens, msg, httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanIDs...)
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
	go r.finishFailureUsageWithErrorAndClientIP(claims, tokenID, ch.ID, acc.ID, model, isStream, start, statusCode, estTokens, errorMessageFromResponse(respBody), httputil.ClientIPForLog(ctx, r.trustedProxies), tokenPlanIDs...)
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

func cleanJSONUndefinedPlaceholders(body []byte) []byte {
	var root interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	cleaned, changed := cleanUndefinedValue(root, 0)
	if !changed {
		return body
	}
	result, err := json.Marshal(cleaned)
	if err != nil {
		return body
	}
	return result
}

func cleanUndefinedValue(value interface{}, depth int) (interface{}, bool) {
	if depth > 10 {
		return value, false
	}
	changed := false
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if s, ok := child.(string); ok && s == "[undefined]" {
				delete(v, key)
				changed = true
				continue
			}
			cleaned, childChanged := cleanUndefinedValue(child, depth+1)
			if childChanged {
				v[key] = cleaned
				changed = true
			}
		}
		return v, changed
	case []interface{}:
		kept := make([]interface{}, 0, len(v))
		for _, child := range v {
			if s, ok := child.(string); ok && s == "[undefined]" {
				changed = true
				continue
			}
			cleaned, childChanged := cleanUndefinedValue(child, depth+1)
			if childChanged {
				changed = true
			}
			kept = append(kept, cleaned)
		}
		return kept, changed
	}
	return value, changed
}

func setRequestModel(body []byte, model string) []byte {
	if model == "" {
		return body
	}
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return body
	}
	bodyMap["model"] = model
	result, err := json.Marshal(bodyMap)
	if err != nil {
		return body
	}
	return result
}

func routedModelFromBody(body []byte, fallback string) string {
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return fallback
	}
	if model, ok := bodyMap["model"].(string); ok && strings.TrimSpace(model) != "" {
		return strings.TrimSpace(model)
	}
	return fallback
}

func normalizeCodexResponsesRequest(body []byte) []byte {
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return body
	}
	if _, ok := bodyMap["instructions"]; !ok {
		bodyMap["instructions"] = ""
	}
	bodyMap["store"] = false
	bodyMap["stream"] = true
	bodyMap["parallel_tool_calls"] = true
	bodyMap["include"] = []string{"reasoning.encrypted_content"}
	for _, key := range []string{
		"max_output_tokens",
		"max_completion_tokens",
		"temperature",
		"top_p",
		"top_k",
		"truncation",
		"conversation",
		"context_management",
		"max_tool_calls",
		"metadata",
		"previous_response_id",
		"prompt_cache_key",
		"prompt_cache_retention",
		"safety_identifier",
		"top_logprobs",
		"user",
	} {
		delete(bodyMap, key)
	}
	if serviceTier, ok := bodyMap["service_tier"].(string); ok && serviceTier != "priority" {
		delete(bodyMap, "service_tier")
	}
	if input, ok := bodyMap["input"].(string); ok {
		bodyMap["input"] = []map[string]interface{}{
			{
				"type":    "message",
				"role":    "user",
				"content": []map[string]interface{}{{"type": "input_text", "text": input}},
			},
		}
	}
	normalizeCodexResponsesInputRoles(bodyMap)
	normalizeCodexResponsesTools(bodyMap)
	result, err := json.Marshal(bodyMap)
	if err != nil {
		return body
	}
	return result
}

func normalizeCodexResponsesInputRoles(bodyMap map[string]interface{}) {
	items, ok := bodyMap["input"].([]interface{})
	if !ok {
		return
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := item["role"].(string); role == "system" {
			item["role"] = "developer"
		}
	}
}

func normalizeCodexResponsesTools(bodyMap map[string]interface{}) {
	tools, ok := bodyMap["tools"].([]interface{})
	if !ok {
		return
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]interface{})
		if !ok {
			continue
		}
		if toolType, _ := tool["type"].(string); toolType == "web_search_preview" {
			tool["type"] = "web_search"
		}
	}
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

// parseRetryDelay attempts to extract a retry delay from a 429 response body.
// Returns -1 if no delay can be determined.
// Supports: Gemini (retryDelay/retry_info.retry_delay), Anthropic (error.retry_after), OpenAI (Retry-After header style in body).
func parseRetryDelay(body []byte, apiFormat provider.Format) time.Duration {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return -1
	}

	// Anthropic: error.retry_after (seconds)
	if errObj, ok := parsed["error"].(map[string]interface{}); ok {
		if ra, ok := errObj["retry_after"].(float64); ok && ra > 0 {
			return time.Duration(ra) * time.Second
		}
	}

	// Gemini: retryDelay or retry_info.retry_delay (e.g. "32s")
	if rd, ok := parsed["retryDelay"].(string); ok {
		if d, err := time.ParseDuration(rd); err == nil {
			return d
		}
	}
	if ri, ok := parsed["retry_info"].(map[string]interface{}); ok {
		if rd, ok := ri["retry_delay"].(string); ok {
			if d, err := time.ParseDuration(rd); err == nil {
				return d
			}
		}
	}

	// Gemini v2: error.details[].retryDelay
	if errObj, ok := parsed["error"].(map[string]interface{}); ok {
		if details, ok := errObj["details"].([]interface{}); ok {
			for _, d := range details {
				if dm, ok := d.(map[string]interface{}); ok {
					if rd, ok := dm["retryDelay"].(string); ok {
						if dur, err := time.ParseDuration(rd); err == nil {
							return dur
						}
					}
				}
			}
		}
	}

	return -1
}

// hopByHopHeaders should not be forwarded between proxy hops.
var hopByHopHeaders = map[string]struct{}{
	"Transfer-Encoding":   {},
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Trailer":             {},
	"Upgrade":             {},
	"Content-Length":      {},
}

func copyHeaders(resp *fasthttp.Response, dst *fasthttp.ResponseHeader) {
	resp.Header.VisitAll(func(k, v []byte) {
		if isHopByHopHeader(string(k)) {
			return
		}
		dst.SetBytesKV(k, v)
	})
}

func isHopByHopHeader(key string) bool {
	for header := range hopByHopHeaders {
		if strings.EqualFold(key, header) {
			return true
		}
	}
	return false
}

func sanitizeConvertedResponseHeaders(h *fasthttp.ResponseHeader) {
	h.Del("Content-Length")
	h.Del("Content-Encoding")
	h.Del("Content-Range")
	h.Del("Accept-Ranges")
	h.Set("Content-Type", "application/json")
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

func getAdaptor(channelType string) provider.Adaptor {
	switch channelType {
	case "openai":
		return &openai.OpenAIAdaptor{}
	case "anthropic":
		return &anthropic.AnthropicAdaptor{}
	case "gemini":
		return &gemini.GeminiAdaptor{}
	case "antigravity":
		return &antigravity.AntigravityAdaptor{}
	default:
		return nil
	}
}

func reverseStreamConverterForClient(clientFormat, upstreamFormat provider.Format) func([]byte) []byte {
	return newStreamConverterFunc(provider.FormatOpenAIChatCompletions, clientFormat)
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

func (r *Relayer) loadRelayPolicy(token db.Token) (db.AccessPolicy, bool, error) {
	var row struct {
		PolicyID *uuid.UUID
	}
	if err := r.db.Table("token_plans").
		Select("plans.policy_id").
		Joins("JOIN tokens ON tokens.user_id = token_plans.user_id AND tokens.id = ? AND tokens.enabled = true AND tokens.deleted_at IS NULL", token.ID).
		Joins("JOIN plans ON plans.id = token_plans.plan_id AND plans.enabled = true AND plans.deleted_at IS NULL").
		Where("token_plans.starts_at <= ? AND token_plans.expires_at > ?", time.Now(), time.Now()).
		Order("token_plans.created_at DESC").
		Limit(1).
		Scan(&row).Error; err != nil {
		return db.AccessPolicy{}, false, err
	}
	if row.PolicyID == nil || *row.PolicyID == uuid.Nil {
		return db.AccessPolicy{}, false, nil
	}
	var policy db.AccessPolicy
	if err := r.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", *row.PolicyID).First(&policy).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return db.AccessPolicy{}, true, fmt.Errorf("access policy disabled or not found")
		}
		return db.AccessPolicy{}, true, err
	}
	return policy, true, nil
}

func channelSupportsModel(ch db.Channel, model string) bool {
	if strings.TrimSpace(ch.Models) == "" && isOAuthAPIFormat(ch.APIFormat) {
		return false
	}
	if ch.APIFormat != "antigravity" {
		return modelalias.Supports(model, ch.Models, ch.ModelAliases)
	}
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	for _, public := range antigravityPublicModelsForChannel(ch.Models, ch.Settings) {
		if public == model {
			return true
		}
	}
	return antigravity.SupportsModelInList(model, httputil.CSVList(ch.Models), antigravity.ParseChannelSettings(ch.Settings))
}

func antigravityPublicModelsForChannel(models, settingsRaw string) []string {
	settings := antigravity.ParseChannelSettings(settingsRaw)
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, public := range antigravity.PublicListForSettings(httputil.CSVList(models), settings) {
		if _, ok := seen[public]; ok {
			continue
		}
		seen[public] = struct{}{}
		out = append(out, public)
	}
	return out
}

func isOAuthAPIFormat(format string) bool {
	return format == "codex" || format == "gemini_code" || format == "claude_code" || format == "antigravity"
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
	rt := detectRelayRequestType(path)
	if rt.isMedia() {
		return rt.permission()
	}
	return permissionForFormat(format)
}

func (r *Relayer) writeLog(tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, clientIPs ...string) {
	r.writeLogWithError(tokenID, channelID, accountID, model, isStream, pt, ct, start, statusCode, "", clientIPs...)
}

func (r *Relayer) writeLogWithError(tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, errorMessage string, clientIPs ...string) {
	r.writeLogWithRoutedModelAndError(tokenID, channelID, accountID, model, model, isStream, pt, ct, start, statusCode, errorMessage, clientIPs...)
}

func (r *Relayer) writeLogWithRoutedModel(tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, pt, ct int, start time.Time, statusCode int, clientIPs ...string) {
	r.writeLogWithRoutedModelAndError(tokenID, channelID, accountID, model, routedModel, isStream, pt, ct, start, statusCode, "", clientIPs...)
}

func (r *Relayer) writeLogWithRoutedModelAndError(tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, pt, ct int, start time.Time, statusCode int, errorMessage string, clientIPs ...string) {
	r.writeLogWithRoutedModelFormatsAndError(tokenID, channelID, accountID, model, routedModel, isStream, "", "", pt, ct, start, statusCode, errorMessage, clientIPs...)
}

func (r *Relayer) writeLogWithRoutedModelAndFormats(tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, pt, ct int, start time.Time, statusCode int, clientIPs ...string) {
	r.writeLogWithRoutedModelFormatsAndError(tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, pt, ct, start, statusCode, "", clientIPs...)
}

func (r *Relayer) writeLogWithRoutedModelFormatsAndError(tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, pt, ct int, start time.Time, statusCode int, errorMessage string, clientIPs ...string) {
	if r.db == nil {
		return
	}
	clientIP := ""
	if len(clientIPs) > 0 {
		clientIP = strings.TrimSpace(clientIPs[0])
	}
	logEntry := db.Log{
		TokenID:          toUUID(tokenID),
		ClientIP:         clientIP,
		ChannelID:        toUUID(channelID),
		AccountID:        toUUID(accountID),
		Model:            model,
		RoutedModel:      normalizedRoutedModel(model, routedModel),
		ClientFormat:     string(clientFormat),
		UpstreamFormat:   string(upstreamFormat),
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

func normalizedRoutedModel(model, routedModel string) string {
	routedModel = strings.TrimSpace(routedModel)
	if routedModel == "" {
		return strings.TrimSpace(model)
	}
	return routedModel
}

func (r *Relayer) finishUsage(claims *internalauth.Claims, tokenID, tokenPlanID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int, estTokens int, fallbackClientIPs ...string) {
	r.finishUsageWithRoutedModel(claims, tokenID, tokenPlanID, channelID, accountID, model, model, isStream, pt, ct, start, statusCode, estTokens, fallbackClientIPs...)
}

func (r *Relayer) finishUsageWithRoutedModel(claims *internalauth.Claims, tokenID, tokenPlanID, channelID, accountID interface{}, model, routedModel string, isStream bool, pt, ct int, start time.Time, statusCode int, estTokens int, fallbackClientIPs ...string) {
	r.finishUsageWithRoutedModelAndFormats(claims, tokenID, tokenPlanID, channelID, accountID, model, routedModel, isStream, "", "", pt, ct, start, statusCode, estTokens, fallbackClientIPs...)
}

func (r *Relayer) finishUsageWithRoutedModelAndFormats(claims *internalauth.Claims, tokenID, tokenPlanID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, pt, ct int, start time.Time, statusCode int, estTokens int, fallbackClientIPs ...string) {
	planID := requestTokenPlanID(claims, tokenPlanID)
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, routedModel, clientFormat, upstreamFormat, isStream, pt, ct, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLogWithRoutedModelAndFormats(tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, pt, ct, start, statusCode, firstClientIP(clientIPFromClaims(claims), fallbackClientIPs...))
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
	r.finishFailureUsageWithErrorAndClientIP(claims, tokenID, channelID, accountID, model, isStream, start, statusCode, estTokens, errorMessage, "", tokenPlanIDs...)
}

func (r *Relayer) finishFailureUsageWithErrorAndClientIP(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model string, isStream bool, start time.Time, statusCode int, estTokens int, errorMessage string, fallbackClientIP string, tokenPlanIDs ...uuid.UUID) {
	r.finishFailureUsageWithRoutedModelAndErrorAndClientIP(claims, tokenID, channelID, accountID, model, model, isStream, start, statusCode, estTokens, errorMessage, fallbackClientIP, tokenPlanIDs...)
}

func (r *Relayer) finishFailureUsageWithRoutedModelAndErrorAndClientIP(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, start time.Time, statusCode int, estTokens int, errorMessage string, fallbackClientIP string, tokenPlanIDs ...uuid.UUID) {
	r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, tokenID, channelID, accountID, model, routedModel, isStream, "", "", start, statusCode, estTokens, errorMessage, fallbackClientIP, tokenPlanIDs...)
}

func (r *Relayer) finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, start time.Time, statusCode int, estTokens int, errorMessage string, fallbackClientIP string, tokenPlanIDs ...uuid.UUID) {
	planID := requestTokenPlanID(claims, firstUUID(tokenPlanIDs))
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, routedModel, clientFormat, upstreamFormat, isStream, 0, 0, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLogWithRoutedModelFormatsAndError(tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, 0, 0, start, statusCode, errorMessage, firstClientIP(clientIPFromClaims(claims), fallbackClientIP))
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

func clientIPFromClaims(claims *internalauth.Claims) string {
	if claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.ClientIP)
}

func firstClientIP(primary string, fallbacks ...string) string {
	if primary = strings.TrimSpace(primary); primary != "" {
		return primary
	}
	for _, fallback := range fallbacks {
		if fallback = strings.TrimSpace(fallback); fallback != "" {
			return fallback
		}
	}
	return ""
}

func (r *Relayer) reportUsageEvent(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model, routedModel string, clientFormat, upstreamFormat provider.Format, isStream bool, pt, ct int, start time.Time, statusCode int, estTokens int) error {
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
		"routed_model":      normalizedRoutedModel(model, routedModel),
		"client_format":     string(clientFormat),
		"upstream_format":   string(upstreamFormat),
		"is_stream":         isStream,
		"prompt_tokens":     pt,
		"completion_tokens": ct,
		"estimated_tokens":  estTokens,
		"status_code":       statusCode,
		"latency_ms":        time.Since(start).Milliseconds(),
		"client_ip":         claims.ClientIP,
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

func shouldInjectStreamField(clientFormat provider.Format, sameFormat bool, forceStreamActive bool, rawGeminiSameFormat bool) bool {
	if rawGeminiSameFormat {
		return false
	}
	// Gemini expresses streaming in the method/URL, not as a JSON body field.
	// This matters most for OAuth private Gemini CodeAssist and Antigravity
	// wrappers, where an injected "stream" key is rejected upstream.
	if clientFormat == provider.FormatGemini {
		return false
	}
	return !sameFormat || forceStreamActive
}

func doStreamingRequest(req *fasthttp.Request, resp *fasthttp.Response) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			resp.Reset()
			streamingClient.CloseIdleConnections()
		}
		if err := doStreamingRequestOnce(req, resp); err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "streaming request panic") {
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func doStreamingRequestOnce(req *fasthttp.Request, resp *fasthttp.Response) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			streamingClient.CloseIdleConnections()
			err = fmt.Errorf("streaming request panic: %v", rec)
		}
	}()
	return streamingClient.Do(req, resp)
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

func (r *Relayer) settleAndRefund(tokenID string, tokenPlanID uuid.UUID, reqBody, respBody []byte, adaptor provider.Adaptor, estTokens int, model string) (int, int) {
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
	estimateMissingUsage(&pt, &ct, reqBody, respBody, 0)
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
