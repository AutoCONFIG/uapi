package relay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/channelcap"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/httputil"
	"github.com/AutoCONFIG/uapi/internal/internalauth"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/modelalias"
	"github.com/AutoCONFIG/uapi/internal/quota"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/chatgptreverse"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// bufferedClient is for non-streaming upstream requests with reasonable timeouts.
var bufferedClient = &fasthttp.Client{
	ReadTimeout:         120 * time.Second,
	WriteTimeout:        30 * time.Second,
	MaxConnDuration:     180 * time.Second,
	MaxResponseBodySize: maxResponseSize,
}

// maxResponseSize limits how much data we buffer from upstream (100 MB).
const maxResponseSize = 100 * 1024 * 1024

// largePayloadThresholdBytes is the threshold above which JSON cleanup is skipped
// to avoid request body size changes from JSON parse→re-serialize cycle.
// Set to 256MB to support large files (PDFs up to 64MB, videos up to 256MB).
const largePayloadThresholdBytesDefault = 256 * 1024 * 1024

const defaultStreamIdleTimeout = 300 * time.Second

var claudeCodeSessionPattern = regexp.MustCompile(`_session_([a-f0-9-]+)$`)

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
	largePayloadBytes int64 // threshold in bytes, defaults to 256MB
	runtimeMu         sync.RWMutex
	runtimeVersion    int64
	runtimeChannels   map[uuid.UUID]db.Channel
	runtimeAccounts   map[uuid.UUID]db.Account
	quotaScheduler    *quota.Scheduler
	cooldownPolicy    *CooldownPolicy
	channelModelBlock *ChannelModelBlocklist
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

// WithLargePayloadThreshold sets the threshold in bytes above which JSON cleanup is skipped.
func WithLargePayloadThreshold(thresholdBytes int64) RelayerOption {
	return func(r *Relayer) {
		if thresholdBytes > 0 {
			r.largePayloadBytes = thresholdBytes
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
		largePayloadBytes: largePayloadThresholdBytesDefault,
		runtimeChannels:   make(map[uuid.UUID]db.Channel),
		runtimeAccounts:   make(map[uuid.UUID]db.Account),
		cooldownPolicy:    NewCooldownPolicy(),
		channelModelBlock: NewChannelModelBlocklist(0),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Relayer) SetQuotaScheduler(s *quota.Scheduler) {
	r.quotaScheduler = s
}

// SetLargePayloadThreshold updates the large payload threshold at runtime.
func (r *Relayer) SetLargePayloadThreshold(thresholdMB int) {
	if thresholdMB > 0 {
		r.largePayloadBytes = int64(thresholdMB) * 1024 * 1024
	}
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
	if requestType == requestTypeUnsupported {
		ctx.Error(`{"error":"unsupported route"}`, fasthttp.StatusBadRequest)
		return
	}

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
		if !r.concLimiter.Acquire(ctx, tokenID) {
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
	var originalBody []byte
	if relayDebugDumpEnabled() {
		originalBody = append([]byte(nil), body...)
	}
	isMediaRequest := requestType.isMedia()
	// Skip JSON cleanup for large payloads to avoid request body size changes
	// from JSON parse→re-serialize cycle (matches Bifrost's large payload handling).
	if !isMediaRequest && int64(len(body)) <= r.largePayloadBytes {
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
	var routeAttempts []map[string]interface{}
	capabilityReq := channelcap.AnalyzeJSON(string(requestType), body)
	affinityScope := requestAffinityScope(ctx, body)
	if gatewayAuthenticated && internalClaims.ChannelID != "" && internalClaims.AccountID != "" {
		targetChannel, account, adaptor, creds, err = r.resolveSelectedChannelAndAccount(internalClaims.ChannelID, internalClaims.AccountID, req.Model)
	} else {
		targetChannel, account, adaptor, creds, err = r.resolveChannelAndAccountWithAttempts(token.ID.String(), req.Model, affinityScope, &routeAttempts, capabilityReq)
	}
	if err != nil {
		if relayDebugDumpEnabled() {
			trace := startRelayRequestDebugDump(originalBody, body, token, nil, nil, claims, clientFormat, "", requestType, req.Model, req.Model, req.Stream)
			trace.Event("route_failed",
				logger.F("status", fasthttp.StatusNotFound),
				logger.Err(err),
				logger.F("model", req.Model),
				logger.F("request_type", string(requestType)),
				logger.F("route_attempts", routeAttempts),
				logger.F("route_diagnostics", r.routeFailureDiagnostics(req.Model, capabilityReq)),
			)
		}
		finishGatewayEarlyFailure(req.Model, fasthttp.StatusNotFound)
		ctx.Error(`{"error":"`+httputil.JSONEscape(err.Error())+`"}`, fasthttp.StatusNotFound)
		return
	}
	routeAdminInfo := routeLogAdminInfo(affinityScope, routeAttempts)
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
	if !supportsRelayChannelRequest(targetChannel, channelcap.AnalyzeJSON(string(requestType), body)) {
		go r.finishFailureUsageWithErrorAndClientIP(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusBadRequest, estimatedTokens, fmt.Sprintf("%s is not supported by %s channels", requestType, targetChannel.Type), r.clientIPForDirectRequest(ctx, claims), tokenPlanID)
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
	// Affinity is now recorded on success only (see recordSelectedAffinity calls
	// in handleStreaming/handleBuffered completion paths). Pre-write affinity was
	// removed to prevent zombie bindings when upstream requests fail.
	// Determine upstream format from channel type
	var upstreamFormat provider.Format
	switch targetChannel.Type {
	case "openai":
		if targetChannel.APIFormat == "chatgpt_reverse" {
			upstreamFormat = provider.FormatChatGPTReverse
		} else if isCodexAPIFormat(targetChannel.APIFormat) {
			upstreamFormat = provider.FormatCodexResponses
		} else if targetChannel.APIFormat == "responses" {
			upstreamFormat = provider.FormatOpenAIResponses
		} else {
			upstreamFormat = provider.FormatOpenAIChatCompletions
		}
	case "anthropic":
		if targetChannel.APIFormat == "claude_code" {
			upstreamFormat = provider.FormatClaudeCode
		} else {
			upstreamFormat = provider.FormatAnthropic
		}
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

	nativeCodexPassthrough := isCodexAPIFormat(targetChannel.APIFormat) && isLikelyNativeCodexClientRequest(ctx)
	if nativeCodexPassthrough {
		clientFormat = provider.FormatCodexResponses
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

	forceStreamActive := channelForceStreamForModel(targetChannel, req.Model, upstreamModel) && !req.Stream
	effectiveStream := req.Stream || forceStreamActive
	if !isMediaRequest && effectiveStream && shouldInjectStreamField(clientFormat, sameFormat, forceStreamActive, rawGeminiSameFormat) {
		body = injectStreamTrue(body)
	}

	// 8. Build upstream request
	adaptor.Init(targetChannel, account)
	adaptor.SetRequestParams(upstreamModel, effectiveStream)
	upstreamURL, err := adaptor.GetRequestURL(path)
	if err != nil {
		go r.finishFailureUsageWithErrorAndClientIP(claims, token.ID, targetChannel.ID, account.ID, req.Model, false, start, fasthttp.StatusInternalServerError, estimatedTokens, "build url failed", r.clientIPForDirectRequest(ctx, claims), tokenPlanID)
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
		logger.F("account_name", account.Name),
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
	if sameFormat {
		convertedBody, err = provider.NormalizeRequestSameProtocol(upstreamFormat, body)
		if err != nil {
			go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, targetChannel.ID, account.ID, req.Model, routedModel, false, clientFormat, upstreamFormat, start, fasthttp.StatusBadRequest, estimatedTokens, err.Error(), r.clientIPForDirectRequest(ctx, claims), tokenPlanID)
			ctx.Error(`{"error":"normalize request failed: `+httputil.JSONEscape(err.Error())+`"}`, fasthttp.StatusBadRequest)
			return
		}
	} else {
		convertedBody, err = provider.ConvertRequestWithAdaptor(clientFormat, upstreamFormat, body, adaptor)
		if err != nil {
			go r.finishFailureUsageWithRoutedModelFormatsAndErrorAndClientIP(claims, token.ID, targetChannel.ID, account.ID, req.Model, routedModel, false, clientFormat, upstreamFormat, start, fasthttp.StatusBadRequest, estimatedTokens, err.Error(), r.clientIPForDirectRequest(ctx, claims), tokenPlanID)
			ctx.Error(`{"error":"convert request failed: `+httputil.JSONEscape(err.Error())+`"}`, fasthttp.StatusBadRequest)
			return
		}
	}
	routedModel = routedModelFromBody(convertedBody, routedModel)
	if isCodexAPIFormat(targetChannel.APIFormat) && upstreamFormat == provider.FormatCodexResponses && !nativeCodexPassthrough {
		convertedBody = normalizeCodexResponsesRequest(convertedBody, effectiveStream, codexClientMetadataSeed(targetChannel, account, token.ID.String()))
		routedModel = routedModelFromBody(convertedBody, routedModel)
	}
	if effectiveStream && shouldInjectConvertedStreamField(upstreamFormat) && (!sameFormat || forceStreamActive) {
		convertedBody = injectStreamTrue(convertedBody)
	}
	if bodyWithCachePolicy, changed, policyErr := upstreamconfig.ApplyCachePassthroughPolicy(targetChannel, upstreamFormat, convertedBody); policyErr != nil {
		logger.Warnf("relay.cache", "apply upstream cache policy failed", logger.Err(policyErr), logger.F("channel_id", targetChannel.ID.String()), logger.F("upstream_format", string(upstreamFormat)))
	} else if changed {
		convertedBody = bodyWithCachePolicy
	}
	adaptor.SetRequestParams(routedModel, effectiveStream)
	var debugTrace *relayDebugTrace
	if relayDebugDumpEnabled() {
		debugTrace = startRelayRequestDebugDump(originalBody, convertedBody, token, targetChannel, account, claims, clientFormat, upstreamFormat, requestType, req.Model, routedModel, effectiveStream)
		debugTrace.Event("route_attempts",
			logger.F("model", req.Model),
			logger.F("request_type", string(requestType)),
			logger.F("attempts", routeAttempts),
		)
		debugTrace.Event("route_selected",
			logger.F("token_id", token.ID.String()),
			logger.F("model", req.Model),
			logger.F("routed_model", routedModel),
			logger.F("stream", req.Stream),
			logger.F("effective_stream", effectiveStream),
			logger.F("force_stream", forceStreamActive),
			logger.F("client_format", string(clientFormat)),
			logger.F("upstream_format", string(upstreamFormat)),
			logger.F("channel_id", targetChannel.ID.String()),
			logger.F("channel_type", targetChannel.Type),
			logger.F("api_format", targetChannel.APIFormat),
			logger.F("account_id", account.ID.String()),
			logger.F("account_name", account.Name),
			logger.F("account_cred_type", account.CredType),
			logger.F("gateway_authenticated", gatewayAuthenticated),
			logger.F("upstream_url", upstreamURL),
		)
	}

	// 9. Dispatch
	if debugTrace != nil {
		debugTrace.SetRoutingInfo(routeAdminInfo)
	}
	if req.Stream && !forceStreamActive {
		streaming = true // goroutine handles Release
		r.handleStreaming(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, routedModel, clientFormat, upstreamFormat, start, estimatedTokens, claims, debugTrace, routeAdminInfo, requestType)
	} else if forceStreamActive {
		streaming = true
		r.handleForceStream(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, routedModel, clientFormat, upstreamFormat, start, estimatedTokens, claims, debugTrace, routeAdminInfo, affinityScope, requestType, nil)
	} else {
		streaming = true // handleBuffered manages its own concurrency release
		r.handleBuffered(ctx, token, tokenPlanID, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, routedModel, clientFormat, upstreamFormat, start, estimatedTokens, claims, debugTrace, routeAdminInfo, affinityScope, requestType, 0)
	}

}

// handleStreaming: real-time chunk-by-chunk forwarding using SSEStreamReader.
func (r *Relayer) handleStreaming(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims, trace *relayDebugTrace, adminInfo map[string]interface{}, requestType relayRequestType) {
	r.handleStreamingAttempt(ctx, token, tokenPlanID, ch, acc, adaptor, url, body, creds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false, 0, nil, trace, adminInfo, "", requestType)
}

func (r *Relayer) handleStreamingAttempt(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims, authRetried bool, quotaAttempts int, transientExcluded map[string]bool, trace *relayDebugTrace, adminInfo map[string]interface{}, affinityScope string, requestType relayRequestType) {
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		trace.Event("setup_headers_failed", logger.Err(err))
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
		return
	}
	applyCodexMetadataHeaders(upReq, upstreamFormat, body)

	trace.Event("upstream_request_started",
		logger.F("mode", "stream"),
		logger.F("auth_retried", authRetried),
		logger.F("headers", relayDebugRequestHeaders(upReq)),
		logger.F("body_stream", requestJSONBool(body, "stream")),
	)
	// The streaming request returns after receiving headers; the body is read from BodyStream.
	if err := doUpstreamStreaming(adaptor, upReq, upResp); err != nil {
		trace.Event("upstream_request_failed", logger.Err(err), logger.F("auth_retried", authRetried))
		logger.Warnf("relay.upstream", "streaming request failed", logger.Err(err))
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}

	statusCode := upResp.StatusCode()
	trace.Event("upstream_headers_received",
		logger.F("status", statusCode),
		logger.F("auth_retried", authRetried),
		logger.F("content_type", string(upResp.Header.Peek("Content-Type"))),
		logger.F("transfer_encoding", string(upResp.Header.Peek("Transfer-Encoding"))),
	)
	if statusCode >= 400 {
		bodyCopy := readUpstreamErrorBody(upResp)
		trace.Event("upstream_error_body",
			logger.F("status", statusCode),
			logger.F("body_bytes", len(bodyCopy)),
			logger.F("body_preview", compactLogBody(bodyCopy)),
		)
		if !authRetried {
			if refreshedCreds, ok := r.refreshOAuthCredentialsAfterAuthFailure(ch, acc, statusCode, bodyCopy); ok {
				trace.Event("oauth_refreshed_after_upstream_error", logger.F("status", statusCode))
				fasthttp.ReleaseRequest(upReq)
				fasthttp.ReleaseResponse(upResp)
				appendRouteFallback(adminInfo, "stream", ch, acc, acc, statusCode, "oauth_refresh", quotaAttempts)
				r.handleStreamingAttempt(ctx, token, tokenPlanID, ch, acc, adaptor, url, body, refreshedCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, true, quotaAttempts, transientExcluded, trace, adminInfo, affinityScope, requestType)
				return
			}
		}
		errClass := ClassifyUpstreamError(statusCode, bodyCopy)
		isQuota := isUpstreamQuotaExhausted(statusCode, bodyCopy)
		failoverReason := fmt.Errorf("status_%d", statusCode).Error()
		trace.Event("upstream_error_classified",
			logger.F("status", statusCode),
			logger.F("error_class", errClass.String()),
			logger.F("is_quota", isQuota),
			logger.F("from_channel_id", ch.ID.String()),
			logger.F("from_account_id", acc.ID.String()),
		)

		switch errClass {
		case ErrServerSide, ErrConfigSide:
			// Gateway/config error: channel failover, NO account cooldown
			if quotaAttempts < r.channelAttemptLimit() {
				r.prepareChannelFailover(ch, statusCode, bodyCopy, model)
				nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
					token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
				if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
					trace.Event("stream_retry_switch_channel",
						logger.F("status", statusCode),
						logger.F("reason", failoverReason),
						logger.F("from_channel_id", ch.ID.String()),
						logger.F("channel_id", nextCh.ID.String()),
						logger.F("account_id", nextAcc.ID.String()),
					)
					fasthttp.ReleaseRequest(upReq)
					fasthttp.ReleaseResponse(upResp)
					appendRouteFallback(adminInfo, "stream", ch, acc, nextAcc, statusCode, "channel_failover", 0)
					// Cross-channel: reset attempt counter and exclusion map
					r.handleStreamingAttempt(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false, 0, nil, trace, adminInfo, affinityScope, requestType)
					return
				}
			}
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return

		case ErrAccountSide:
			// Account error: try next account on same channel, then escalate to channel
			r.prepareAccountFailover(ch, acc, statusCode, bodyCopy, isQuota)
			if quotaAttempts < r.accountAttemptLimit(ch) {
				next := r.pickNextExcluding(ch, poolFromChannel(r.pools, ch), transientExcluded)
				if next != nil {
					nextCreds, credErr := r.ensureCredentials(ch, next)
					if credErr == nil {
						trace.Event("stream_retry_switch_account",
							logger.F("status", statusCode),
							logger.F("reason", failoverReason),
							logger.F("from_account_id", acc.ID.String()),
							logger.F("account_id", next.ID.String()),
							logger.F("quota_attempts", quotaAttempts+1),
						)
						fasthttp.ReleaseRequest(upReq)
						fasthttp.ReleaseResponse(upResp)
						adaptor.Init(ch, next)
						if transientExcluded == nil {
							transientExcluded = make(map[string]bool)
						}
						transientExcluded[acc.ID.String()] = true
						appendRouteFallback(adminInfo, "stream", ch, acc, next, statusCode, failoverReason, quotaAttempts+1)
						r.handleStreamingAttempt(ctx, token, tokenPlanID, ch, next, adaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false, quotaAttempts+1, transientExcluded, trace, adminInfo, affinityScope, requestType)
						return
					}
					trace.Event("stream_retry_credentials_failed", logger.Err(credErr), logger.F("account_id", next.ID.String()))
				}
			}
			// Account retries exhausted → escalate to channel failover
			if quotaAttempts < r.channelAttemptLimit() {
				r.prepareChannelFailover(ch, statusCode, bodyCopy, model)
				nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
					token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
				if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
					trace.Event("stream_retry_escalate_channel",
						logger.F("status", statusCode),
						logger.F("reason", failoverReason),
						logger.F("from_channel_id", ch.ID.String()),
						logger.F("channel_id", nextCh.ID.String()),
						logger.F("account_id", nextAcc.ID.String()),
					)
					fasthttp.ReleaseRequest(upReq)
					fasthttp.ReleaseResponse(upResp)
					appendRouteFallback(adminInfo, "stream", ch, acc, nextAcc, statusCode, "channel_escalation", 0)
					// Cross-channel: reset attempt counter and exclusion map
					r.handleStreamingAttempt(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false, 0, nil, trace, adminInfo, affinityScope, requestType)
					return
				}
			}
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return

		case ErrAccountTerminal:
			// Terminal auth error → skip retries, surface error to client
			trace.Event("stream_terminal_account_error",
				logger.F("status", statusCode),
				logger.F("account_id", acc.ID.String()),
			)
			r.prepareAccountFailover(ch, acc, statusCode, bodyCopy, false)
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return

		default:
			// Client/unknown errors: no retry
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return
		}
	}

	rawBodyStream := upResp.BodyStream()
	if rawBodyStream == nil {
		trace.Event("stream_bootstrap_missing_body", logger.F("status", fasthttp.StatusBadGateway))
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "upstream stream body missing", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}
	bodyStream, stopIdleTimeout := httputil.NewIdleTimeoutReader(rawBodyStream, rawBodyStream, r.streamIdleTimeout)
	peekedBodyStream, bootstrapMessage, bootstrapFailed, peekErr := peekStreamBootstrapError(bodyStream)
	if peekErr != nil {
		trace.Event("stream_bootstrap_peek_failed", logger.Err(peekErr), logger.F("status", fasthttp.StatusBadGateway))
		_ = bodyStream.Close()
		stopIdleTimeout()
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "read upstream stream failed", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}
	if bootstrapFailed {
		bootstrapStatus := streamErrorHTTPStatus(bootstrapMessage)
		if bootstrapStatus == 0 {
			bootstrapStatus = fasthttp.StatusBadGateway
		}
		bodyCopy := formatOpenAIError(bootstrapMessage)
		trace.Event("stream_bootstrap_error",
			logger.F("status", bootstrapStatus),
			logger.F("body_preview", compactLogBody(bodyCopy)),
			logger.F("quota_attempts", quotaAttempts),
		)
		errClassBoot := ClassifyUpstreamError(bootstrapStatus, bodyCopy)
		isQuotaBoot := isUpstreamQuotaExhausted(bootstrapStatus, bodyCopy)

		switch errClassBoot {
		case ErrServerSide, ErrConfigSide:
			if quotaAttempts < r.channelAttemptLimit() {
				r.prepareChannelFailover(ch, bootstrapStatus, bodyCopy, model)
				nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
					token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
				if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
					trace.Event("stream_bootstrap_retry_switch_channel",
						logger.F("status", bootstrapStatus),
						logger.F("from_channel_id", ch.ID.String()),
						logger.F("channel_id", nextCh.ID.String()),
					)
					_ = bodyStream.Close()
					stopIdleTimeout()
					fasthttp.ReleaseRequest(upReq)
					fasthttp.ReleaseResponse(upResp)
					appendRouteFallback(adminInfo, "stream_bootstrap", ch, acc, nextAcc, bootstrapStatus, "channel_failover", 0)
					r.handleStreamingAttempt(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false, 0, nil, trace, adminInfo, affinityScope, requestType)
					return
				}
			}
			_ = bodyStream.Close()
			stopIdleTimeout()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, fasthttp.StatusBadGateway, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return

		case ErrAccountSide:
			r.prepareAccountFailover(ch, acc, bootstrapStatus, bodyCopy, isQuotaBoot)
			if quotaAttempts < r.accountAttemptLimit(ch) {
				next := r.pickNextExcluding(ch, poolFromChannel(r.pools, ch), transientExcluded)
				if next != nil {
					nextCreds, credErr := r.ensureCredentials(ch, next)
					if credErr == nil {
						trace.Event("stream_bootstrap_retry_switch_account",
							logger.F("status", bootstrapStatus),
							logger.F("from_account_id", acc.ID.String()),
							logger.F("account_id", next.ID.String()),
						)
						_ = bodyStream.Close()
						stopIdleTimeout()
						fasthttp.ReleaseRequest(upReq)
						fasthttp.ReleaseResponse(upResp)
						adaptor.Init(ch, next)
						if transientExcluded == nil {
							transientExcluded = make(map[string]bool)
						}
						transientExcluded[acc.ID.String()] = true
						appendRouteFallback(adminInfo, "stream_bootstrap", ch, acc, next, bootstrapStatus, "account_failover", quotaAttempts+1)
						r.handleStreamingAttempt(ctx, token, tokenPlanID, ch, next, adaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false, quotaAttempts+1, transientExcluded, trace, adminInfo, affinityScope, requestType)
						return
					}
					trace.Event("stream_bootstrap_retry_credentials_failed", logger.Err(credErr))
				}
			}
			// Account retries exhausted → escalate to channel
			if quotaAttempts < r.channelAttemptLimit() {
				r.prepareChannelFailover(ch, bootstrapStatus, bodyCopy, model)
				nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
					token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
				if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
					trace.Event("stream_bootstrap_retry_escalate_channel",
						logger.F("status", bootstrapStatus),
						logger.F("from_channel_id", ch.ID.String()),
						logger.F("channel_id", nextCh.ID.String()),
					)
					_ = bodyStream.Close()
					stopIdleTimeout()
					fasthttp.ReleaseRequest(upReq)
					fasthttp.ReleaseResponse(upResp)
					appendRouteFallback(adminInfo, "stream_bootstrap", ch, acc, nextAcc, bootstrapStatus, "channel_escalation", 0)
					r.handleStreamingAttempt(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, false, 0, nil, trace, adminInfo, affinityScope, requestType)
					return
				}
			}
			_ = bodyStream.Close()
			stopIdleTimeout()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, bootstrapStatus, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return

		case ErrAccountTerminal:
			trace.Event("stream_bootstrap_terminal_account_error",
				logger.F("status", bootstrapStatus),
				logger.F("account_id", acc.ID.String()),
			)
			r.prepareAccountFailover(ch, acc, bootstrapStatus, bodyCopy, false)
			_ = bodyStream.Close()
			stopIdleTimeout()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, bootstrapStatus, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return

		default:
			_ = bodyStream.Close()
			stopIdleTimeout()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundOnError(ctx, token.ID.String(), estTokens, bootstrapStatus, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return
		}
	}

	// SSE headers for downstream
	ctx.SetStatusCode(statusCode)
	ctx.Response.Header.Set("Content-Type", "text/event-stream")
	ctx.Response.Header.Set("Cache-Control", "no-cache")
	ctx.Response.Header.Set("Connection", "keep-alive")
	ctx.Response.Header.Set("X-Accel-Buffering", "no")

	reader := NewSSEStreamReaderWithTrace(trace)
	ctx.Response.SetBodyStream(reader, -1)

	tracker := newStreamTracker(adaptor)

	inputConvert, outputConvert, reverseConverter := chatGPTReverseOutputConverter(upstreamFormat, clientFormat, routedModel)
	sendDone := clientFormat == provider.FormatOpenAIChatCompletions

	// Producer goroutine: owns upReq/upResp lifecycle, releases when done
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Default().Panic("relay.stream", "stream goroutine panic", rec)
				if r.billing != nil {
					go r.billing.DBTransactionRefundPreConsume(token.ID.String(), tokenPlanID, estTokens, model)
				}
			}
		}()
		defer fasthttp.ReleaseRequest(upReq)
		defer releaseStreamingResponse(upResp)
		defer r.releaseLocalConcurrency(token.ID.String(), claims)
		defer stopIdleTimeout()
		defer trace.WriteRoutingInfo()
		result := streamAndForwardWithTrace(peekedBodyStream, reader, tracker, inputConvert, outputConvert, sendDone, trace)
		if result.err != nil {
			if errors.Is(result.err, io.ErrClosedPipe) {
				pt, ct, _ := tracker.Result()
				cacheCreationTokens := tracker.CacheCreationTokens()
				cacheReadTokens := tracker.CacheReadTokens()
				estimateMissingUsage(&pt, &ct, body, nil, tracker.EstimatedOutputTokens())
				trace.Event("stream_result_client_closed",
					logger.F("status", 499),
					logger.F("prompt_tokens", pt),
					logger.F("completion_tokens", ct),
					logger.F("cache_creation_tokens", cacheCreationTokens),
					logger.F("cache_read_tokens", cacheReadTokens),
					logger.F("estimated_output_tokens", tracker.EstimatedOutputTokens()),
				)
				if pt > 0 || ct > 0 {
					go r.finishUsageWithRoutedModelFormatsCacheAndAdmin(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, 499, estTokens, adminInfo, r.clientIPForDirectRequest(ctx, claims))
				} else {
					go r.finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, 499, estTokens, "client disconnected", r.clientIPForDirectRequest(ctx, claims), adminInfo, tokenPlanID)
				}
				return
			}
			trace.Event("stream_result_error",
				logger.F("status", fasthttp.StatusBadGateway),
				logger.Err(result.err),
			)
			logger.Warnf("relay.stream", "forward failed", logger.Err(result.err))
			go r.finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, fasthttp.StatusBadGateway, estTokens, result.err.Error(), r.clientIPForDirectRequest(ctx, claims), adminInfo, tokenPlanID)
			return
		}
		if result.failed {
			trace.Event("stream_result_upstream_failure", logger.F("status", fasthttp.StatusBadGateway))
			logger.Warnf("relay.stream", "upstream stream reported failure")
			go r.finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, fasthttp.StatusBadGateway, estTokens, "upstream stream reported failure", r.clientIPForDirectRequest(ctx, claims), adminInfo, tokenPlanID)
			return
		}
		if result.emptyStream {
			trace.Event("stream_result_empty",
				logger.F("status", fasthttp.StatusBadGateway),
				logger.F("prompt_tokens", result.promptTokens),
				logger.F("completion_tokens", result.completionTokens),
			)
			logger.Warnf("relay.stream", "upstream stream completed without payload events")
			go r.finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, fasthttp.StatusBadGateway, estTokens, "upstream stream completed without payload events", r.clientIPForDirectRequest(ctx, claims), adminInfo, tokenPlanID)
			return
		}
		if !result.finalized {
			trace.Event("stream_result_missing_terminal", logger.F("status", fasthttp.StatusBadGateway))
			logger.Warnf("relay.stream", "stream ended without terminal event")
			go r.finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims, token.ID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, start, fasthttp.StatusBadGateway, estTokens, "stream ended without terminal event", r.clientIPForDirectRequest(ctx, claims), adminInfo, tokenPlanID)
			return
		}
		{
			// Record affinity only on successful stream completion
			if scope := requestAffinityScope(ctx, body); scope != "" && ch.AffinityTTL > 0 {
				r.affinity.Set(token.ID.String(), model, scope, ch.ID.String(), acc.ID.String(), ch.AffinityTTL)
			}
		}
		pt, ct, parseFailed := tracker.Result()
		cacheCreationTokens := tracker.CacheCreationTokens()
		cacheReadTokens := tracker.CacheReadTokens()
		estimateMissingUsage(&pt, &ct, body, nil, tracker.EstimatedOutputTokens())
		if parseFailed {
			logger.Component("relay.billing").Warn("ParseStreamUsage had errors during streaming, token counts may be inaccurate",
				logger.F("token_id", token.ID.String()),
				logger.F("model", model),
				logger.F("prompt_tokens", pt),
				logger.F("completion_tokens", ct),
			)
		}
		trace.Event("stream_result_success",
			logger.F("status", statusCode),
			logger.F("prompt_tokens", pt),
			logger.F("completion_tokens", ct),
			logger.F("cache_creation_tokens", cacheCreationTokens),
			logger.F("cache_read_tokens", cacheReadTokens),
			logger.F("parse_failed", parseFailed),
			logger.F("latency_ms", time.Since(start).Milliseconds()),
		)
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
		// Update conversation_id in account metadata for ChatGPT Reverse channel
		if reverseConverter != nil && reverseConverter.ConversationID() != "" {
			if acc.Metadata == nil {
				acc.Metadata = make(map[string]interface{})
			}
			acc.Metadata["last_conversation_id"] = reverseConverter.ConversationID()
			acc.Metadata["last_conversation_timestamp"] = time.Now().Format(time.RFC3339)
			go func() {
				if err := r.db.Model(&db.Account{}).Where("id = ?", acc.ID).Update("metadata", acc.Metadata).Error; err != nil {
					logger.Warnf("relay.stream", "failed to update conversation_id in account metadata", logger.Err(err))
				}
			}()
		}
		go r.finishUsageWithRoutedModelFormatsCacheAndAdmin(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, true, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, estTokens, adminInfo, r.clientIPForDirectRequest(ctx, claims))
	}()
}

// handleForceStream: stream to upstream, buffer all, convert to non-stream for downstream.
func (r *Relayer) handleForceStream(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims, trace *relayDebugTrace, adminInfo map[string]interface{}, affinityScope string, requestType relayRequestType, transientExcluded map[string]bool, quotaAttempts ...int) {
	quotaAttempt := firstInt(quotaAttempts...)
	defer func() {
		if rec := recover(); rec != nil {
			logger.Default().Panic("relay.stream", "force stream panic", rec)
			r.refundAndError(ctx, token.ID.String(), estTokens, "internal error", claims, ch, acc, model, start, tokenPlanID)
		}
	}()
	defer trace.WriteRoutingInfo()

	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		trace.Event("setup_headers_failed", logger.Err(err), logger.F("mode", "force_stream"))
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
		return
	}
	applyCodexMetadataHeaders(upReq, upstreamFormat, body)

	trace.Event("upstream_request_started",
		logger.F("mode", "force_stream"),
		logger.F("headers", relayDebugRequestHeaders(upReq)),
		logger.F("body_stream", requestJSONBool(body, "stream")),
	)
	if err := doUpstreamStreaming(adaptor, upReq, upResp); err != nil {
		trace.Event("upstream_request_failed", logger.Err(err), logger.F("mode", "force_stream"))
		logger.Warnf("relay.upstream", "force stream request failed", logger.Err(err))
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}

	statusCode := upResp.StatusCode()
	trace.Event("upstream_headers_received",
		logger.F("mode", "force_stream"),
		logger.F("status", statusCode),
		logger.F("content_type", string(upResp.Header.Peek("Content-Type"))),
		logger.F("transfer_encoding", string(upResp.Header.Peek("Transfer-Encoding"))),
	)
	if statusCode >= 400 {
		bodyCopy := readUpstreamErrorBody(upResp)
		trace.Event("upstream_error_body",
			logger.F("mode", "force_stream"),
			logger.F("status", statusCode),
			logger.F("body_bytes", len(bodyCopy)),
			logger.F("body_preview", compactLogBody(bodyCopy)),
		)
		if refreshedCreds, ok := r.refreshOAuthCredentialsAfterAuthFailure(ch, acc, statusCode, bodyCopy); ok {
			trace.Event("oauth_refreshed_after_upstream_error", logger.F("mode", "force_stream"), logger.F("status", statusCode))
			upReq.Reset()
			upResp.Reset()
			upReq.SetRequestURI(url)
			upReq.Header.SetMethodBytes([]byte("POST"))
			upReq.SetBody(body)
			if err := adaptor.SetupRequestHeader(upReq, refreshedCreds); err != nil {
				trace.Event("setup_headers_failed", logger.Err(err), logger.F("mode", "force_stream_retry"))
				r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, acc, model, start, tokenPlanID)
				return
			}
			applyCodexMetadataHeaders(upReq, upstreamFormat, body)
			trace.Event("upstream_request_started",
				logger.F("mode", "force_stream_retry"),
			)
			if err := doUpstreamStreaming(adaptor, upReq, upResp); err != nil {
				trace.Event("upstream_request_failed", logger.Err(err), logger.F("mode", "force_stream_retry"))
				logger.Warnf("relay.upstream", "force stream request failed after oauth refresh", logger.Err(err))
				r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "upstream error", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
				return
			}
			statusCode = upResp.StatusCode()
			trace.Event("upstream_headers_received",
				logger.F("mode", "force_stream_retry"),
				logger.F("status", statusCode),
				logger.F("content_type", string(upResp.Header.Peek("Content-Type"))),
				logger.F("transfer_encoding", string(upResp.Header.Peek("Transfer-Encoding"))),
			)
			if statusCode >= 400 {
				bodyCopy = readUpstreamErrorBody(upResp)
				trace.Event("upstream_error_body",
					logger.F("mode", "force_stream_retry"),
					logger.F("status", statusCode),
					logger.F("body_bytes", len(bodyCopy)),
					logger.F("body_preview", compactLogBody(bodyCopy)),
				)
			}
		}
		if statusCode >= 400 {
			failoverReason, isQuota, accountFailover := upstreamAccountFailoverReason(statusCode, bodyCopy)
			transientFailover := false
			if !accountFailover && (statusCode >= 500 || statusCode == fasthttp.StatusRequestTimeout) {
				failoverReason = "upstream_status"
				transientFailover = true
			}
			// Gateway-level error (5xx/408): skip the entire channel
			if transientFailover {
				if quotaAttempt < r.channelAttemptLimit() {
					r.prepareChannelFailover(ch, statusCode, bodyCopy, model)
					nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
						token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
					if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
						trace.Event("force_stream_retry_switch_channel",
							logger.F("status", statusCode),
							logger.F("reason", failoverReason),
							logger.F("from_channel_id", ch.ID.String()),
							logger.F("channel_id", nextCh.ID.String()),
							logger.F("account_id", nextAcc.ID.String()),
							logger.F("quota_attempts", quotaAttempt+1),
						)
						appendRouteFallback(adminInfo, "force_stream", ch, acc, nextAcc, statusCode, "channel_failover", quotaAttempt+1)
						r.handleForceStream(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, trace, adminInfo, affinityScope, requestType, transientExcluded, quotaAttempt+1)
						return
					}
				}
				r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
				return
			}
			// Account-level error: try up to N accounts on same channel
			if accountFailover && quotaAttempt < r.accountAttemptLimit(ch) {
				r.prepareAccountFailover(ch, acc, statusCode, bodyCopy, isQuota)
				next := r.pickNextExcluding(ch, poolFromChannel(r.pools, ch), transientExcluded)
				if next != nil {
					nextCreds, credErr := r.ensureCredentials(ch, next)
					if credErr == nil {
						trace.Event("force_stream_retry_switch_account",
							logger.F("status", statusCode),
							logger.F("reason", failoverReason),
							logger.F("from_account_id", acc.ID.String()),
							logger.F("account_id", next.ID.String()),
							logger.F("quota_attempts", quotaAttempt+1),
						)
						adaptor.Init(ch, next)
						appendRouteFallback(adminInfo, "force_stream", ch, acc, next, statusCode, failoverReason, quotaAttempt+1)
						r.handleForceStream(ctx, token, tokenPlanID, ch, next, adaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, trace, adminInfo, affinityScope, requestType, transientExcluded, quotaAttempt+1)
						return
					}
					trace.Event("force_stream_retry_credentials_failed", logger.Err(credErr), logger.F("account_id", next.ID.String()))
				}
			}
			// Account retries exhausted: escalate to channel failover
			if accountFailover && quotaAttempt < r.channelAttemptLimit() {
				nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
					token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
				if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
					trace.Event("force_stream_retry_escalate_channel",
						logger.F("status", statusCode),
						logger.F("reason", failoverReason),
						logger.F("from_channel_id", ch.ID.String()),
						logger.F("channel_id", nextCh.ID.String()),
						logger.F("account_id", nextAcc.ID.String()),
						logger.F("quota_attempts", quotaAttempt+1),
					)
					appendRouteFallback(adminInfo, "force_stream", ch, acc, nextAcc, statusCode, "channel_escalation", quotaAttempt+1)
					r.handleForceStream(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, trace, adminInfo, affinityScope, requestType, transientExcluded, quotaAttempt+1)
					return
				}
			}
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
			return
		}
	}

	// Buffer entire stream. Read one byte past the limit so oversized upstream
	// streams fail explicitly instead of being silently truncated.
	bodyStream, stopIdleTimeout := httputil.NewIdleTimeoutReader(upResp.BodyStream(), upResp.BodyStream(), r.streamIdleTimeout)
	defer stopIdleTimeout()
	respBody, err := io.ReadAll(io.LimitReader(bodyStream, int64(maxResponseSize)+1))
	if err != nil {
		trace.Event("force_stream_read_failed", logger.Err(err), logger.F("status", fasthttp.StatusBadGateway))
		logger.Warnf("relay.upstream", "force stream read failed", logger.Err(err))
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "read upstream error", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}
	if len(respBody) > maxResponseSize {
		trace.Event("force_stream_response_too_large", logger.F("limit", maxResponseSize), logger.F("body_bytes", len(respBody)))
		logger.Warnf("relay.upstream", "force stream response too large", logger.F("limit", maxResponseSize))
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "upstream response too large", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}
	trace.StreamChunk("stream.upstream.sse", respBody)

	// Force-stream: aggregate upstream SSE into the upstream protocol's own
	// non-stream response, then convert once into the client protocol.
	if upstreamFormat == provider.FormatChatGPTReverse {
		respBody = convertSSEBufferWithConverter(respBody, newChatGPTReverseInputConverter(routedModel))
		upstreamFormat = provider.FormatOpenAIChatCompletions
	} else if forceStreamAggregationFormat(upstreamFormat) == provider.FormatOpenAIChatCompletions {
		if convert := newStreamConverterFunc(upstreamFormat, provider.FormatOpenAIChatCompletions); convert != nil {
			respBody = convertSSEBufferWithConverter(respBody, convert)
		}
		upstreamFormat = provider.FormatOpenAIChatCompletions
	} else if convert := forceStreamPreAggregationConverter(upstreamFormat); convert != nil {
		respBody = convertSSEBufferWithConverter(respBody, convert)
	}
	trace.StreamChunk("stream.normalized.sse", respBody)

	var complete bool
	var responseFormat provider.Format
	respBody, complete, responseFormat = StreamToNonStreamForFormat(upstreamFormat, respBody)
	if isOpenAIErrorResponse(respBody) {
		trace.Event("force_stream_openai_error_response", logger.F("body_preview", compactLogBody(respBody)))
		r.refundOnError(ctx, token.ID.String(), estTokens, fasthttp.StatusBadGateway, respBody, ch, acc, model, true, start, clientFormat, claims, tokenPlanID)
		return
	}
	if !complete {
		trace.Event("force_stream_missing_terminal")
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "stream ended without terminal event", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}

	// Parse usage from upstream JSON BEFORE client-format conversion.
	pt, ct, cacheCreationTokens, cacheReadTokens := parseNonStreamUsageFull(respBody)
	estimateMissingUsage(&pt, &ct, body, respBody, 0)

	// Convert from OpenAI JSON to client format if needed
	if clientFormat != responseFormat {
		if converted, err := provider.ConvertResponse(responseFormat, clientFormat, respBody); err != nil {
			trace.Event("response_conversion_failed", logger.Err(err), logger.F("mode", "force_stream"))
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
	trace.StreamChunk("response.downstream.json", respBody)
	if scope := requestAffinityScope(ctx, body); scope != "" && ch.AffinityTTL > 0 {
		r.affinity.Set(token.ID.String(), model, scope, ch.ID.String(), acc.ID.String(), ch.AffinityTTL)
	}

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
	trace.Event("force_stream_result_success",
		logger.F("status", statusCode),
		logger.F("prompt_tokens", pt),
		logger.F("completion_tokens", ct),
		logger.F("cache_creation_tokens", cacheCreationTokens),
		logger.F("cache_read_tokens", cacheReadTokens),
		logger.F("response_bytes", len(respBody)),
		logger.F("latency_ms", time.Since(start).Milliseconds()),
	)
	go r.finishUsageWithRoutedModelFormatsCacheAndAdmin(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, false, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, estTokens, adminInfo, r.clientIPForDirectRequest(ctx, claims))
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
	if !supportsRelayChannelRequest(ch, channelcap.AnalyzeJSON(string(requestType), body)) {
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "request type not supported by selected channel", claims, ch, acc, model, start, fasthttp.StatusBadRequest, tokenPlanID)
		return
	}
	responseFormat := imageResponseFormat(ctx, body)
	if ch.APIFormat == "chatgpt_reverse" {
		r.handleChatGPTReverseImageGeneration(ctx, token, tokenPlanID, ch, acc, adaptor, body, creds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, requestType)
		return
	}
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
	if err := doUpstreamBuffered(adaptor, bufferedClient, upReq, upResp); err != nil {
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
			if err := doUpstreamBuffered(adaptor, bufferedClient, upReq, upResp); err != nil {
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
	go r.finishUsageWithRoutedModelAndFormats(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, false, clientFormat, upstreamFormat, 0, estTokens, start, statusCode, estTokens, r.clientIPForDirectRequest(ctx, claims))
}

type chatGPTReverseImageGenerator interface {
	GenerateImage(body []byte, credentials string) ([]byte, int, error)
}

func (r *Relayer) handleChatGPTReverseImageGeneration(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims, requestType relayRequestType) {
	if requestType != requestTypeImageGeneration {
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "request type not supported by selected channel", claims, ch, acc, model, start, fasthttp.StatusBadRequest, tokenPlanID)
		return
	}
	generator, ok := adaptor.(chatGPTReverseImageGenerator)
	if !ok {
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, "chatgpt reverse image adaptor unavailable", claims, ch, acc, model, start, fasthttp.StatusBadGateway, tokenPlanID)
		return
	}
	respBody, statusCode, err := generator.GenerateImage(body, creds)
	if err != nil {
		logger.Warnf("relay.chatgpt_reverse", "image generation failed", logger.Err(err))
		r.refundAndErrorWithStatus(ctx, token.ID.String(), estTokens, err.Error(), claims, ch, acc, model, start, statusCode, tokenPlanID)
		return
	}
	ctx.SetStatusCode(statusCode)
	ctx.Response.Header.Set("Content-Type", "application/json")
	ctx.SetBody(respBody)
	r.releaseLocalConcurrency(token.ID.String(), claims)
	logger.Debugf("relay.media", "chatgpt reverse image generation completed", logger.F("token_id", token.ID.String()), logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("model", model), logger.F("status", statusCode), logger.F("latency_ms", time.Since(start).Milliseconds()))
	go r.finishUsageWithRoutedModelAndFormats(claims, token.ID, tokenPlanID, ch.ID, acc.ID, model, routedModel, false, clientFormat, upstreamFormat, 0, estimatedImageTokens, start, statusCode, estTokens, r.clientIPForDirectRequest(ctx, claims))
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
	logger.Infof("relay.channel_cache", "channel cache refreshed", logger.F("count", len(channels)))
	return channels
}

func (c *channelCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channels = nil
	c.expiry = time.Time{}
}

func (r *Relayer) InvalidateChannelCache() {
	if r == nil || r.chCache == nil {
		return
	}
	r.chCache.invalidate()
	logger.Infof("relay.channel_cache", "channel cache invalidated")
}

// handleBuffered: standard buffered request with retry.
func (r *Relayer) handleBuffered(ctx *fasthttp.RequestCtx, token db.Token, tokenPlanID uuid.UUID, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, routedModel string, clientFormat, upstreamFormat provider.Format, start time.Time, estTokens int, claims *internalauth.Claims, trace *relayDebugTrace, adminInfo map[string]interface{}, affinityScope string, requestType relayRequestType, channelAttempts int) {
	var respBody []byte
	var statusCode int
	var respHeaders fasthttp.ResponseHeader
	respAccount := acc
	currentCreds := creds
	currentAccount := acc
	refreshedAuthAccounts := make(map[uuid.UUID]bool)
	transientExcluded := make(map[string]bool)
	maxAttempts := r.accountAttemptLimit(ch)
	if maxAttempts < 3 {
		maxAttempts = 3
	}

	for retry := 0; retry < maxAttempts; retry++ {
		upReq := fasthttp.AcquireRequest()
		upResp := fasthttp.AcquireResponse()
		retryReason := "upstream_retry"

		upReq.SetRequestURI(url)
		upReq.Header.SetMethodBytes(ctx.Method())
		upReq.SetBody(body)
		if err := adaptor.SetupRequestHeader(upReq, currentCreds); err != nil {
			trace.Event("setup_headers_failed", logger.Err(err), logger.F("mode", "buffered"), logger.F("retry", retry))
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed", claims, ch, currentAccount, model, start, tokenPlanID)
			return
		}
		applyCodexMetadataHeaders(upReq, upstreamFormat, body)

		err := doUpstreamBuffered(adaptor, bufferedClient, upReq, upResp)
		fasthttp.ReleaseRequest(upReq)

		shouldRetry := false
		if err != nil {
			trace.Event("upstream_request_failed", logger.Err(err), logger.F("mode", "buffered"), logger.F("retry", retry))
			logger.Warnf("relay.upstream", "buffered request failed, switching channel", logger.F("retry", retry), logger.Err(err))
			fasthttp.ReleaseResponse(upResp)
			// Network error is a gateway-level failure: try channel failover
			if channelAttempts < r.channelAttemptLimit() {
				r.prepareChannelFailover(ch, fasthttp.StatusBadGateway, []byte(err.Error()), model)
				nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
					token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
				if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
					trace.Event("buffered_retry_switch_channel",
						logger.F("reason", "upstream_request_error"),
						logger.F("from_channel_id", ch.ID.String()),
						logger.F("channel_id", nextCh.ID.String()),
						logger.F("account_id", nextAcc.ID.String()),
						logger.F("channel_attempts", channelAttempts+1),
					)
					appendRouteFallback(adminInfo, "buffered", ch, currentAccount, nextAcc, fasthttp.StatusBadGateway, "channel_failover", channelAttempts+1)
					r.handleBuffered(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, trace, adminInfo, affinityScope, requestType, channelAttempts+1)
					return
				}
			}
			// No alternative channel: surface error
			trace.Event("buffered_no_alternative_channel", logger.F("reason", "upstream_request_error"), logger.F("channel_attempts", channelAttempts))
			r.refundAndError(ctx, token.ID.String(), estTokens, "upstream request failed: "+err.Error(), claims, ch, currentAccount, model, start, tokenPlanID)
			return
		} else if upResp.StatusCode() == 429 {
			trace.Event("upstream_headers_received", logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("status", upResp.StatusCode()))
			// Trigger quota refresh on 429 so admin can see updated usage
			if r.quotaScheduler != nil && currentAccount != nil && ch != nil {
				r.quotaScheduler.On429(currentAccount.ID, ch.ID)
			}
			respBody429 := copyBody(upResp)
			statusCode = 429
			copyHeaders(upResp, &respHeaders)
			retryDelay := parseRetryDelay(respBody429, upstreamFormat)
			trace.Event("upstream_429",
				logger.F("mode", "buffered"),
				logger.F("retry", retry),
				logger.F("retry_delay_ms", retryDelay.Milliseconds()),
				logger.F("body_bytes", len(respBody429)),
				logger.F("body_preview", compactLogBody(respBody429)),
			)
			if retryDelay >= 0 && retryDelay <= 3*time.Second && retry < maxAttempts-1 {
				// Short delay: wait and retry same account
				logger.Infof("relay.429", "short retry delay, retrying same account", logger.F("delay", retryDelay), logger.F("retry", retry))
				time.Sleep(retryDelay)
				fasthttp.ReleaseResponse(upResp)
				continue
			}
			// Medium/long delay or unknown: switch account
			shouldRetry = true
			retryReason = "quota_exhausted"
			r.prepareAccountFailover(ch, currentAccount, statusCode, respBody429, true)
		} else if isUpstreamQuotaExhausted(upResp.StatusCode(), upResp.Body()) {
			trace.Event("upstream_headers_received", logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("status", upResp.StatusCode()))
			respBody = copyBody(upResp)
			statusCode = upResp.StatusCode()
			copyHeaders(upResp, &respHeaders)
			if r.quotaScheduler != nil && currentAccount != nil && ch != nil {
				r.quotaScheduler.On429(currentAccount.ID, ch.ID)
			}
			trace.Event("upstream_quota_exhausted",
				logger.F("mode", "buffered"),
				logger.F("retry", retry),
				logger.F("status", statusCode),
				logger.F("body_bytes", len(respBody)),
				logger.F("body_preview", compactLogBody(respBody)),
			)
			shouldRetry = true
			retryReason = "quota_exhausted"
			r.prepareAccountFailover(ch, currentAccount, statusCode, respBody, true)
		} else if currentAccount != nil && !refreshedAuthAccounts[currentAccount.ID] && isOAuthAuthFailure(currentAccount, upResp.StatusCode(), upResp.Body()) {
			trace.Event("upstream_headers_received", logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("status", upResp.StatusCode()))
			respBody = copyBody(upResp)
			statusCode = upResp.StatusCode()
			copyHeaders(upResp, &respHeaders)
			if refreshedCreds, ok := r.refreshOAuthCredentialsAfterAuthFailure(ch, currentAccount, statusCode, respBody); ok {
				trace.Event("oauth_refreshed_after_upstream_error",
					logger.F("mode", "buffered"),
					logger.F("retry", retry),
					logger.F("status", statusCode),
					logger.F("body_bytes", len(respBody)),
					logger.F("body_preview", compactLogBody(respBody)),
				)
				refreshedAuthAccounts[currentAccount.ID] = true
				currentCreds = refreshedCreds
				fasthttp.ReleaseResponse(upResp)
				continue
			}
		} else if failoverReason, isQuota, ok := upstreamAccountFailoverReason(upResp.StatusCode(), upResp.Body()); ok {
			trace.Event("upstream_headers_received", logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("status", upResp.StatusCode()))
			respBody = copyBody(upResp)
			statusCode = upResp.StatusCode()
			copyHeaders(upResp, &respHeaders)
			trace.Event("upstream_account_failover",
				logger.F("mode", "buffered"),
				logger.F("retry", retry),
				logger.F("status", statusCode),
				logger.F("reason", failoverReason),
				logger.F("body_bytes", len(respBody)),
				logger.F("body_preview", compactLogBody(respBody)),
			)
			shouldRetry = true
			retryReason = failoverReason
			r.prepareAccountFailover(ch, currentAccount, statusCode, respBody, isQuota)
		} else if upResp.StatusCode() >= 500 || upResp.StatusCode() == fasthttp.StatusRequestTimeout {
			trace.Event("upstream_headers_received", logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("status", upResp.StatusCode()))
			logger.Warnf("relay.upstream", "gateway-level upstream status, switching channel", logger.F("status", upResp.StatusCode()), logger.F("retry", retry))
			respBody = copyBody(upResp)
			statusCode = upResp.StatusCode()
			copyHeaders(upResp, &respHeaders)
			trace.Event("retryable_upstream_status",
				logger.F("mode", "buffered"),
				logger.F("retry", retry),
				logger.F("status", statusCode),
				logger.F("body_bytes", len(respBody)),
				logger.F("body_preview", compactLogBody(respBody)),
			)
			// Gateway-level error (5xx/408): skip entire channel, do NOT retry accounts within same channel
			if channelAttempts < r.channelAttemptLimit() {
				r.prepareChannelFailover(ch, statusCode, respBody, model)
				nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
					token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
				if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
					trace.Event("buffered_retry_switch_channel",
						logger.F("status", statusCode),
						logger.F("reason", "channel_failover"),
						logger.F("from_channel_id", ch.ID.String()),
						logger.F("channel_id", nextCh.ID.String()),
						logger.F("account_id", nextAcc.ID.String()),
						logger.F("channel_attempts", channelAttempts+1),
					)
					fasthttp.ReleaseResponse(upResp)
					appendRouteFallback(adminInfo, "buffered", ch, currentAccount, nextAcc, statusCode, "channel_failover", channelAttempts+1)
					r.handleBuffered(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, trace, adminInfo, affinityScope, requestType, channelAttempts+1)
					return
				}
			}
			// No alternative channel available: surface the upstream error directly
			fasthttp.ReleaseResponse(upResp)
			trace.Event("buffered_no_alternative_channel", logger.F("status", statusCode), logger.F("channel_attempts", channelAttempts))
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, currentAccount, model, false, start, clientFormat, claims, tokenPlanID)
			return
		} else {
			trace.Event("upstream_headers_received", logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("status", upResp.StatusCode()))
		}

		if shouldRetry {
			previousAccount := currentAccount
			fasthttp.ReleaseResponse(upResp)
			nextAccount := r.pickNextExcluding(ch, poolFromChannel(r.pools, ch), transientExcluded)
			if nextAccount == nil {
				// All accounts on this channel exhausted: escalate to channel failover
				trace.Event("retry_account_unavailable", logger.F("mode", "buffered"), logger.F("retry", retry))
				if channelAttempts < r.channelAttemptLimit() {
					nextCh, nextAcc, nextAdaptor, nextCreds, nextErr := r.resolveChannelAndAccountWithAttempts(
						token.ID.String(), model, affinityScope, nil, channelcap.AnalyzeJSON(string(requestType), body))
					if nextErr == nil && nextCh != nil && nextCh.ID.String() != ch.ID.String() {
						trace.Event("buffered_retry_escalate_channel",
							logger.F("reason", retryReason),
							logger.F("from_channel_id", ch.ID.String()),
							logger.F("channel_id", nextCh.ID.String()),
							logger.F("account_id", nextAcc.ID.String()),
							logger.F("channel_attempts", channelAttempts+1),
						)
						appendRouteFallback(adminInfo, "buffered", ch, currentAccount, nextAcc, statusCode, "channel_escalation", channelAttempts+1)
						r.handleBuffered(ctx, token, tokenPlanID, nextCh, nextAcc, nextAdaptor, url, body, nextCreds, model, routedModel, clientFormat, upstreamFormat, start, estTokens, claims, trace, adminInfo, affinityScope, requestType, channelAttempts+1)
						return
					}
				}
				break
			}
			currentAccount = nextAccount
			trace.Event("retry_switch_account",
				logger.F("mode", "buffered"),
				logger.F("retry", retry),
				logger.F("account_id", currentAccount.ID.String()),
			)
			appendRouteFallback(adminInfo, "buffered", ch, previousAccount, currentAccount, statusCode, retryReason, retry+1)
			respAccount = currentAccount
			adaptor.Init(ch, currentAccount)
			currentCreds, err = r.ensureCredentials(ch, currentAccount)
			if err != nil {
				trace.Event("retry_credentials_failed", logger.Err(err), logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("account_id", currentAccount.ID.String()))
				logger.Warnf("relay.credentials", "credential error on retry", logger.F("retry", retry), logger.Err(err))
				currentAccount = r.retryNext(ch, currentAccount, r.cooldownPolicy.ComputeCooldown(ErrAccountSide, 0, currentAccount.ID.String()))
				if currentAccount == nil {
					trace.Event("replacement_account_unavailable", logger.F("mode", "buffered"), logger.F("retry", retry))
					break
				}
				trace.Event("retry_replacement_account",
					logger.F("mode", "buffered"),
					logger.F("retry", retry),
					logger.F("account_id", currentAccount.ID.String()),
				)
				appendRouteFallback(adminInfo, "buffered", ch, respAccount, currentAccount, statusCode, "credential_error", retry+1)
				respAccount = currentAccount
				adaptor.Init(ch, currentAccount)
				currentCreds, err = r.ensureCredentials(ch, currentAccount)
				if err != nil {
					trace.Event("replacement_credentials_failed", logger.Err(err), logger.F("mode", "buffered"), logger.F("retry", retry), logger.F("account_id", currentAccount.ID.String()))
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
		trace.Event("buffered_result_all_retries_exhausted", logger.F("status", fasthttp.StatusServiceUnavailable))
		r.releaseLocalConcurrency(token.ID.String(), claims)
		go r.finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims, token.ID, ch.ID, respAccount.ID, model, routedModel, false, clientFormat, upstreamFormat, start, fasthttp.StatusServiceUnavailable, estTokens, "all retries exhausted", r.clientIPForDirectRequest(ctx, claims), adminInfo, tokenPlanID)
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
			trace.Event("buffered_result_upstream_error",
				logger.F("status", statusCode),
				logger.F("body_bytes", len(respBody)),
				logger.F("body_preview", compactLogBody(respBody)),
			)
			r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, respAccount, model, false, start, clientFormat, claims, tokenPlanID)
			return
		}
	}

	upstreamRespBody := respBody
	trace.StreamChunk("response.upstream.json", upstreamRespBody)

	// Response format normalization/conversion. Same-format buffered responses
	// keep the upstream standard JSON intact to preserve provider-native fields.
	responseConverted := upstreamFormat != clientFormat
	if responseConverted {
		if converted, err := provider.ConvertResponse(upstreamFormat, clientFormat, respBody); err != nil {
			trace.Event("response_conversion_failed", logger.Err(err), logger.F("mode", "buffered"))
			logger.Warnf("relay.convert", "response conversion failed", logger.Err(err))
			r.finishFailedBuffered(ctx, token.ID.String(), estTokens, "response conversion failed", claims, ch, respAccount, model, start, fasthttp.StatusBadGateway, tokenPlanID)
			ctx.Error(`{"error":"response conversion failed"}`, fasthttp.StatusBadGateway)
			return
		} else {
			respBody = converted
		}
	}

	// Parse usage from upstream-format response after conversion succeeds.
	pt, ct, cacheCreationTokens, cacheReadTokens := 0, 0, 0, 0
	if claims != nil && claims.RequestID != "" {
		if usage, err := adaptor.ParseUsageFull(upstreamRespBody); err == nil {
			pt, ct = usage.PromptTokens, usage.CompletionTokens
			cacheCreationTokens = usage.CacheCreationInputTokens
			cacheReadTokens = usage.CacheReadInputTokens
			if cacheReadTokens == 0 {
				cacheReadTokens = usage.PromptCacheHitTokens
			}
		} else if parsedPT, parsedCT, err := adaptor.ParseUsage(upstreamRespBody); err == nil {
			pt, ct = parsedPT, parsedCT
		}
		estimateMissingUsage(&pt, &ct, body, upstreamRespBody, 0)
	} else {
		pt, ct, cacheCreationTokens, cacheReadTokens = r.settleAndRefund(token.ID.String(), tokenPlanID, body, upstreamRespBody, adaptor, estTokens, model)
	}

	ctx.SetStatusCode(statusCode)
	respHeaders.VisitAll(func(key, value []byte) {
		ctx.Response.Header.SetBytesKV(key, value)
	})
	if responseConverted {
		sanitizeConvertedResponseHeaders(&ctx.Response.Header)
	}
	ctx.SetBody(respBody)
	trace.StreamChunk("response.downstream.json", respBody)
	if scope := requestAffinityScope(ctx, body); scope != "" && ch.AffinityTTL > 0 {
		r.affinity.Set(token.ID.String(), model, scope, ch.ID.String(), respAccount.ID.String(), ch.AffinityTTL)
	}

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
	trace.Event("buffered_result_success",
		logger.F("status", statusCode),
		logger.F("prompt_tokens", pt),
		logger.F("completion_tokens", ct),
		logger.F("cache_creation_tokens", cacheCreationTokens),
		logger.F("cache_read_tokens", cacheReadTokens),
		logger.F("response_bytes", len(respBody)),
		logger.F("latency_ms", time.Since(start).Milliseconds()),
	)
	if claims != nil && claims.RequestID != "" {
		// Gateway-authenticated requests did not acquire the Relay-local
		// concurrency limiter; Gateway owns that slot and releases it when this
		// response completes.
		go r.finishUsageWithRoutedModelFormatsCacheAndAdmin(claims, token.ID, tokenPlanID, ch.ID, respAccount.ID, model, routedModel, false, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, estTokens, adminInfo, r.clientIPForDirectRequest(ctx, claims))
	} else {
		go r.writeLogWithRoutedModelFormatsErrorCacheAndAdmin(token.ID, ch.ID, respAccount.ID, model, routedModel, false, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, "", adminInfo, r.clientIPForDirectRequest(ctx, claims))
	}
}

func logGeminiCodeUpstreamError(ch *db.Channel, acc *db.Account, statusCode int, reqBody, respBody []byte) {
	var summary struct {
		Model              string   `json:"model"`
		Project            string   `json:"project"`
		EnabledCreditTypes []string `json:"enabled_credit_types"`
	}
	if err := json.Unmarshal(reqBody, &summary); err != nil {
		logger.Warnf("relay.gemini_code", "upstream error", logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("account_name", acc.Name), logger.F("status", statusCode), logger.Err(err), logger.F("response", compactLogBody(respBody)))
		return
	}
	logger.Warnf("relay.gemini_code", "upstream error", logger.F("channel_id", ch.ID.String()), logger.F("account_id", acc.ID.String()), logger.F("account_name", acc.Name), logger.F("status", statusCode), logger.F("model", summary.Model), logger.F("project", summary.Project), logger.F("enabled_credit_types", summary.EnabledCreditTypes), logger.F("response", compactLogBody(respBody)))
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

func forceStreamAggregationFormat(upstreamFormat provider.Format) provider.Format {
	switch upstreamFormat {
	case provider.FormatOpenAIResponses, provider.FormatCodexResponses:
		return upstreamFormat
	default:
		return provider.FormatOpenAIChatCompletions
	}
}

func forceStreamPreAggregationConverter(upstreamFormat provider.Format) func([]byte) []byte {
	aggregationFormat := forceStreamAggregationFormat(upstreamFormat)
	if aggregationFormat == upstreamFormat {
		return nil
	}
	return newStreamConverterFunc(upstreamFormat, aggregationFormat)
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

func (r *Relayer) resolveChannelAndAccount(tokenID, model string, capabilityReq ...channelcap.Request) (*db.Channel, *db.Account, provider.Adaptor, string, error) {
	return r.resolveChannelAndAccountWithAttempts(tokenID, model, "", nil, capabilityReq...)
}

func (r *Relayer) resolveChannelAndAccountWithAttempts(tokenID, model, affinityScope string, attempts *[]map[string]interface{}, capabilityReq ...channelcap.Request) (*db.Channel, *db.Account, provider.Adaptor, string, error) {
	// Try affinity cache first
	if affinityScope != "" && r.affinity != nil {
		if chID, accID := r.affinity.Get(tokenID, model, affinityScope); chID != "" {
			if r.db != nil {
				var ch db.Channel
				if err := r.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", chID).First(&ch).Error; err == nil {
					modelOK := channelSupportsModel(ch, model)
					capabilityOK := channelSupportsCapability(ch, capabilityReq...)
					attempt := r.routeAttemptInfo(ch, modelOK, capabilityOK)
					attempt["source"] = "affinity"
					if accID != "" {
						attempt["affinity_account_id"] = accID
					}
					if modelOK && capabilityOK {
						if acc, adaptor, creds, err := r.pickAccountForAffinity(ch, accID, model); err == nil {
							attempt["selected"] = true
							attempt["account_id"] = acc.ID.String()
							appendRouteAttempt(attempts, attempt)
							return &ch, acc, adaptor, creds, nil
						} else {
							attempt["skip_reason"] = err.Error()
						}
					} else if !modelOK {
						attempt["skip_reason"] = "unsupported_model"
					} else {
						attempt["skip_reason"] = "unsupported_capability"
					}
					appendRouteAttempt(attempts, attempt)
				}
			} else if ch, ok := r.runtimeChannelByID(chID); ok {
				modelOK := channelSupportsModel(ch, model)
				capabilityOK := channelSupportsCapability(ch, capabilityReq...)
				attempt := r.routeAttemptInfo(ch, modelOK, capabilityOK)
				attempt["source"] = "affinity"
				if accID != "" {
					attempt["affinity_account_id"] = accID
				}
				if modelOK && capabilityOK {
					if acc, adaptor, creds, err := r.pickAccountForAffinity(ch, accID, model); err == nil {
						attempt["selected"] = true
						attempt["account_id"] = acc.ID.String()
						appendRouteAttempt(attempts, attempt)
						return &ch, acc, adaptor, creds, nil
					} else {
						attempt["skip_reason"] = err.Error()
					}
				} else if !modelOK {
					attempt["skip_reason"] = "unsupported_model"
				} else {
					attempt["skip_reason"] = "unsupported_capability"
				}
				appendRouteAttempt(attempts, attempt)
			}
		}
	}

	if r.db == nil {
		r.runtimeMu.RLock()
		channels := make([]db.Channel, 0, len(r.runtimeChannels))
		for _, ch := range r.runtimeChannels {
			if ch.Enabled {
				channels = append(channels, ch)
			}
		}
		r.runtimeMu.RUnlock()
		channels = channelCandidatesForModelAndCapability(channels, model, capabilityReq, rand.Intn)
		for i := range channels {
			attempt := r.routeAttemptInfo(channels[i], true, true)
			attempt["source"] = "runtime"
			if acc, adaptor, creds, err := r.pickAccount(channels[i], model); err == nil {
				attempt["selected"] = true
				attempt["account_id"] = acc.ID.String()
				appendRouteAttempt(attempts, attempt)
				return &channels[i], acc, adaptor, creds, nil
			} else {
				attempt["skip_reason"] = err.Error()
			}
			appendRouteAttempt(attempts, attempt)
		}
		return nil, nil, nil, "", routeSelectionError(attempts)
	}

	// Priority-based selection (with caching)
	allChannels := r.chCache.get()
	if attempts != nil {
		for i := range allChannels {
			modelOK := channelSupportsModel(allChannels[i], model)
			capabilityOK := channelSupportsCapability(allChannels[i], capabilityReq...)
			if modelOK && capabilityOK {
				continue
			}
			attempt := r.routeAttemptInfo(allChannels[i], modelOK, capabilityOK)
			attempt["source"] = "priority_filter"
			if !modelOK {
				attempt["skip_reason"] = "unsupported_model"
			} else {
				attempt["skip_reason"] = "unsupported_capability"
			}
			appendRouteAttempt(attempts, attempt)
		}
	}
	channels := channelCandidatesForModelAndCapability(allChannels, model, capabilityReq, rand.Intn)
	for i := range channels {
		attempt := r.routeAttemptInfo(channels[i], true, true)
		attempt["source"] = "priority"
		if acc, adaptor, creds, err := r.pickAccount(channels[i], model); err == nil {
			attempt["selected"] = true
			attempt["account_id"] = acc.ID.String()
			appendRouteAttempt(attempts, attempt)
			return &channels[i], acc, adaptor, creds, nil
		} else {
			attempt["skip_reason"] = err.Error()
		}
		appendRouteAttempt(attempts, attempt)
	}
	return nil, nil, nil, "", routeSelectionError(attempts)
}

func appendRouteAttempt(attempts *[]map[string]interface{}, attempt map[string]interface{}) {
	if attempts == nil {
		return
	}
	*attempts = append(*attempts, attempt)
}

func (r *Relayer) recordSelectedAffinity(tokenID, model, scope string, ch *db.Channel, acc *db.Account) {
	if r == nil || r.affinity == nil || ch == nil || acc == nil || strings.TrimSpace(scope) == "" || ch.AffinityTTL <= 0 {
		return
	}
	// Use SetIfAbsent: first successful binding wins; don't overwrite a working affinity.
	r.affinity.SetIfAbsent(tokenID, model, scope, ch.ID.String(), acc.ID.String(), ch.AffinityTTL)
}

func routeLogAdminInfo(affinityScope string, attempts []map[string]interface{}) map[string]interface{} {
	info := make(map[string]interface{})
	affinity := map[string]interface{}{
		"hit": false,
	}
	if source, value := splitRouteScope(affinityScope); source != "" {
		affinity["source"] = source
		affinity["key_hint"] = routeKeyHint(value)
	}
	path := make([]map[string]interface{}, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt == nil {
			continue
		}
		item := compactRouteAttempt(attempt)
		if len(item) > 0 {
			path = append(path, item)
		}
		if selected, _ := attempt["selected"].(bool); selected {
			info["selected"] = item
			if source, _ := attempt["source"].(string); source == "affinity" {
				affinity["hit"] = true
			}
			if accountID, _ := attempt["affinity_account_id"].(string); accountID != "" {
				affinity["account_id"] = accountID
			}
		}
	}
	if strings.TrimSpace(affinityScope) != "" {
		info["affinity"] = affinity
	}
	if len(path) > 0 {
		info["route_path"] = path
	}
	return info
}

func compactRouteAttempt(attempt map[string]interface{}) map[string]interface{} {
	keys := []string{"source", "channel_id", "account_id", "affinity_account_id", "name", "type", "api_format", "priority", "weight", "supports_model", "supports_capability", "skip_reason", "selected"}
	out := make(map[string]interface{}, len(keys))
	for _, key := range keys {
		if value, ok := attempt[key]; ok {
			out[key] = value
		}
	}
	return out
}

func splitRouteScope(scope string) (string, string) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "", ""
	}
	idx := strings.Index(scope, ":")
	if idx <= 0 {
		return "legacy", scope
	}
	return scope[:idx], scope[idx+1:]
}

func routeKeyHint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 16 {
		return value
	}
	return value[:8] + "..." + value[len(value)-4:]
}

func appendRouteFallback(info map[string]interface{}, mode string, ch *db.Channel, from, to *db.Account, statusCode int, reason string, attempt int) {
	if info == nil {
		return
	}
	item := map[string]interface{}{
		"mode":        mode,
		"status_code": statusCode,
		"reason":      reason,
		"attempt":     attempt,
	}
	if ch != nil {
		item["channel_id"] = ch.ID.String()
		item["channel_name"] = ch.Name
	}
	if from != nil {
		item["from_account_id"] = from.ID.String()
		item["from_account_name"] = from.Name
	}
	if to != nil {
		item["to_account_id"] = to.ID.String()
		item["to_account_name"] = to.Name
	}
	existing, _ := info["fallback_path"].([]map[string]interface{})
	existing = append(existing, item)
	info["fallback_path"] = existing
}

func (r *Relayer) routeAttemptInfo(ch db.Channel, modelOK, capabilityOK bool) map[string]interface{} {
	item := map[string]interface{}{
		"channel_id":          ch.ID.String(),
		"name":                ch.Name,
		"type":                ch.Type,
		"api_format":          ch.APIFormat,
		"priority":            ch.Priority,
		"weight":              effectiveChannelWeight(ch),
		"supports_model":      modelOK,
		"supports_capability": capabilityOK,
	}
	if r.pools != nil {
		if pool, ok := r.pools.GetPool(ch.ID.String()); ok {
			stats := pool.Stats()
			item["pool_exists"] = true
			item["pool_accounts"] = stats.Accounts
			item["pool_total_weight"] = stats.TotalWeight
			item["pool_closed"] = stats.Closed
		} else {
			item["pool_exists"] = false
		}
	} else {
		item["pool_exists"] = false
	}
	return item
}

var (
	errNoChannel = fmt.Errorf("no available channel for model")
	errNoAccount = fmt.Errorf("no available account for model")
)

func routeSelectionError(attempts *[]map[string]interface{}) error {
	if attempts == nil {
		return errNoChannel
	}
	for _, attempt := range *attempts {
		if attempt == nil {
			continue
		}
		modelOK, _ := attempt["supports_model"].(bool)
		capabilityOK, _ := attempt["supports_capability"].(bool)
		reason, _ := attempt["skip_reason"].(string)
		if modelOK && capabilityOK && strings.Contains(reason, "no available account") {
			return errNoAccount
		}
	}
	return errNoChannel
}

func (r *Relayer) routeFailureDiagnostics(model string, capabilityReq ...channelcap.Request) []map[string]interface{} {
	channels := r.chCache.get()
	out := make([]map[string]interface{}, 0, len(channels))
	for i := range channels {
		ch := channels[i]
		modelOK := channelSupportsModel(ch, model)
		capabilityOK := channelSupportsCapability(ch, capabilityReq...)
		if !modelOK && !capabilityOK {
			continue
		}
		item := map[string]interface{}{
			"channel_id":          ch.ID.String(),
			"name":                ch.Name,
			"type":                ch.Type,
			"api_format":          ch.APIFormat,
			"priority":            ch.Priority,
			"weight":              effectiveChannelWeight(ch),
			"models":              ch.Models,
			"model_aliases":       ch.ModelAliases,
			"supports_model":      modelOK,
			"supports_capability": capabilityOK,
		}
		if r.pools != nil {
			if pool, ok := r.pools.GetPool(ch.ID.String()); ok {
				stats := pool.Stats()
				item["pool_exists"] = true
				item["pool_accounts"] = stats.Accounts
				item["pool_total_weight"] = stats.TotalWeight
				item["pool_closed"] = stats.Closed
			} else {
				item["pool_exists"] = false
			}
		} else {
			item["pool_exists"] = false
		}
		out = append(out, item)
	}
	return out
}

func channelCandidatesForModel(channels []db.Channel, model string, randomInt func(int) int) []db.Channel {
	return channelCandidatesForModelAndCapability(channels, model, nil, randomInt)
}

func channelCandidatesForModelAndCapability(channels []db.Channel, model string, capabilityReq []channelcap.Request, randomInt func(int) int) []db.Channel {
	candidates := make([]db.Channel, 0, len(channels))
	for i := range channels {
		if channelSupportsModel(channels[i], model) && channelSupportsCapability(channels[i], capabilityReq...) {
			candidates = append(candidates, channels[i])
		}
	}
	if len(candidates) < 2 || randomInt == nil {
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].Priority > candidates[j].Priority
		})
		return candidates
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Priority > candidates[j].Priority
	})
	start := 0
	for start < len(candidates) {
		end := start + 1
		for end < len(candidates) && candidates[end].Priority == candidates[start].Priority {
			end++
		}
		if end-start > 1 {
			weightedShuffleChannels(candidates[start:end], randomInt)
		}
		start = end
	}
	return candidates
}

func weightedShuffleChannels(channels []db.Channel, randomInt func(int) int) {
	for i := 0; i < len(channels)-1; i++ {
		total := 0
		for j := i; j < len(channels); j++ {
			total += effectiveChannelWeight(channels[j])
		}
		if total <= 0 {
			continue
		}
		pick := randomInt(total)
		for j := i; j < len(channels); j++ {
			pick -= effectiveChannelWeight(channels[j])
			if pick < 0 {
				channels[i], channels[j] = channels[j], channels[i]
				break
			}
		}
	}
}

func effectiveChannelWeight(ch db.Channel) int {
	if ch.Weight <= 0 {
		return 100
	}
	return ch.Weight
}

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
		adaptor := getAdaptorForChannel(ch)
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
	adaptor := getAdaptorForChannel(&ch)
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

func (r *Relayer) runtimeChannelByID(channelID string) (db.Channel, bool) {
	id, err := uuid.Parse(channelID)
	if err != nil {
		return db.Channel{}, false
	}
	r.runtimeMu.RLock()
	defer r.runtimeMu.RUnlock()
	ch, ok := r.runtimeChannels[id]
	if !ok || !ch.Enabled {
		return db.Channel{}, false
	}
	return ch, true
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

func (r *Relayer) pickAccount(ch db.Channel, model string) (*db.Account, provider.Adaptor, string, error) {
	adaptor := getAdaptorForChannel(&ch)
	if adaptor == nil {
		return nil, nil, "", fmt.Errorf("unsupported channel type")
	}
	pool, ok := r.pools.GetPool(ch.ID.String())
	if !ok {
		return nil, nil, "", fmt.Errorf("channel pool not initialized")
	}
	account, ok := pool.PickForModel(model, nil)
	if !ok {
		return nil, nil, "", fmt.Errorf("no available account")
	}
	creds, err := r.ensureCredentials(&ch, account)
	if err != nil {
		return nil, nil, "", fmt.Errorf("credential error: %w", err)
	}
	return account, adaptor, creds, nil
}

func (r *Relayer) pickAccountForAffinity(ch db.Channel, accountID string, model string) (*db.Account, provider.Adaptor, string, error) {
	if strings.TrimSpace(accountID) == "" {
		return r.pickAccount(ch, model)
	}
	adaptor := getAdaptorForChannel(&ch)
	if adaptor == nil {
		return nil, nil, "", fmt.Errorf("unsupported channel type")
	}
	pool, ok := r.pools.GetPool(ch.ID.String())
	if !ok {
		return nil, nil, "", fmt.Errorf("channel pool not initialized")
	}
	account, ok := pool.PickByIDForModel(accountID, model)
	if !ok {
		return nil, nil, "", fmt.Errorf("affinity account unavailable (quota/cooldown/model mismatch)")
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
	if account == nil || (account.CredType != "oauth_token" && account.CredType != "chatgpt_reverse") || strings.TrimSpace(account.RefreshToken) == "" {
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

// cooldownAndEvict applies cooldown to the account in the pool and DB, then clears affinity.
// duration is computed by CooldownPolicy.ComputeCooldown based on error class and status code.
func (r *Relayer) cooldownAndEvict(ch *db.Channel, acc *db.Account, duration time.Duration) {
	if r == nil || ch == nil || acc == nil || duration <= 0 {
		return
	}
	until := time.Now().Add(duration)
	if r.pools != nil {
		if pool, ok := r.pools.GetPool(ch.ID.String()); ok {
			pool.CooldownUntil(acc.ID.String(), until)
		}
	}
	acc.CooldownUntil = &until
	if !isAPIKeyChannel(ch) {
		if r.db != nil {
			if err := r.db.Model(acc).Update("cooldown_until", until).Error; err != nil {
				logger.Warnf("relay.account", "persist account cooldown failed", logger.F("account_id", acc.ID.String()), logger.Err(err))
			}
		}
	}
	if r.affinity != nil {
		r.affinity.EvictAccount(acc.ID.String())
	}
}

func upstreamAccountFailoverReason(statusCode int, body []byte) (string, bool, bool) {
	if isUpstreamQuotaExhausted(statusCode, body) {
		return "quota_exhausted", true, true
	}
	if reason, ok := terminalAccountDisableReason(statusCode, body); ok {
		return reason, false, true
	}
	return "", false, false
}

// prepareAccountFailover applies appropriate cooldown/disable based on error classification.
//   - ErrAccountTerminal: disable permanently, no auto-recovery
//   - ErrAccountSide (quota/401/402/403): cooldown via policy, evict affinity
//   - ErrServerSide: no account cooldown (not the account's fault)
func (r *Relayer) prepareAccountFailover(ch *db.Channel, acc *db.Account, statusCode int, body []byte, isQuota bool) {
	if acc == nil {
		return
	}
	errClass := ClassifyUpstreamError(statusCode, body)

	// Terminal auth error → disable permanently, skip cooldown
	if errClass == ErrAccountTerminal {
		r.cooldownAccountOnTerminalUpstreamError(ch, acc, statusCode, body)
		return
	}

	// API key channels: evict affinity only (no DB cooldown since stateless)
	if isAPIKeyChannel(ch) {
		if r.affinity != nil {
			r.affinity.EvictAccount(acc.ID.String())
		}
		return
	}

	// Quota errors: scheduler notification + auto-disable + cooldown
	if isQuota || errClass == ErrAccountSide {
		if isQuota && r.quotaScheduler != nil && ch != nil {
			r.quotaScheduler.On429(acc.ID, ch.ID)
		}
		if isQuota {
			r.markAutoDisable(acc, "quota_exhausted")
		}
		dur := r.cooldownPolicy.ComputeCooldown(errClass, statusCode, acc.ID.String())
		r.cooldownAndEvict(ch, acc, dur)
		return
	}

	// Server-side errors (5xx/408): no account cooldown — not the account's fault
	// Only evict affinity so the next request routes to a different channel
	if errClass == ErrServerSide && r.affinity != nil {
		r.affinity.EvictAccount(acc.ID.String())
	}
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
	if r.db != nil {
		r.db.Model(acc).Update("metadata", acc.Metadata)
	}
}

func (r *Relayer) cooldownAccountOnTerminalUpstreamError(ch *db.Channel, acc *db.Account, statusCode int, body []byte) {
	if r == nil || acc == nil || acc.ID == uuid.Nil {
		return
	}
	reason, ok := terminalAccountDisableReason(statusCode, body)
	if !ok {
		return
	}
	// Terminal auth errors → disable permanently, no auto-recovery
	if acc.Metadata == nil {
		acc.Metadata = make(map[string]interface{})
	}
	now := time.Now().UTC().Format(time.RFC3339)
	acc.Metadata["auto_disable_reason"] = reason
	acc.Metadata["auto_disable_time"] = now
	acc.Metadata["last_terminal_error_reason"] = reason
	acc.Metadata["last_terminal_error_at"] = now
	if statusCode > 0 {
		acc.Metadata["last_terminal_error_status_code"] = statusCode
	}
	if ch != nil {
		acc.Metadata["last_terminal_error_channel_id"] = ch.ID.String()
	}
	if r.db != nil {
		err := r.db.Model(&db.Account{}).
			Where("id = ? AND deleted_at IS NULL", acc.ID).
			Update("metadata", acc.Metadata).Error
		if err != nil {
			logger.Warnf("relay.account", "mark terminal account error failed", logger.F("account_id", acc.ID.String()), logger.Err(err))
		}
	}
	// Disable account in pool (weight=0, no cooldown timer — terminal means manual re-enable only)
	if r.pools != nil && ch != nil {
		if pool, ok := r.pools.GetPool(ch.ID.String()); ok {
			pool.Disable(acc.ID.String())
		}
	}
	// Evict all affinity bindings for this account across all channels
	if r.affinity != nil {
		r.affinity.EvictAccount(acc.ID.String())
	}
	logger.Warnf("relay.account", "account disabled after terminal upstream error",
		logger.F("channel_id", channelIDForLog(ch)),
		logger.F("account_id", acc.ID.String()),
		logger.F("status", statusCode),
		logger.F("reason", reason),
		logger.F("response", compactLogBody(body)),
	)
}

func channelIDForLog(ch *db.Channel) string {
	if ch == nil {
		return ""
	}
	return ch.ID.String()
}

func (r *Relayer) clearAutoDisable(acc *db.Account) {
	if acc == nil {
		return
	}
	changed := false
	if acc.Metadata == nil {
		acc.Metadata = make(map[string]interface{})
	}
	if _, ok := acc.Metadata["auto_disable_reason"]; !ok {
		if acc.CooldownUntil == nil {
			return
		}
	} else {
		delete(acc.Metadata, "auto_disable_reason")
		delete(acc.Metadata, "auto_disable_time")
		changed = true
	}
	if acc.CooldownUntil != nil {
		acc.CooldownUntil = nil
		changed = true
	}
	if !changed {
		return
	}
	if r.db != nil {
		r.db.Model(acc).Updates(map[string]interface{}{
			"metadata":       acc.Metadata,
			"cooldown_until": nil,
		})
	}
}

func (r *Relayer) retryNext(ch *db.Channel, failed *db.Account, cooldownDuration time.Duration) *db.Account {
	r.cooldownAndEvict(ch, failed, cooldownDuration)
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

func addExcludedAccount(excluded map[string]bool, acc *db.Account) map[string]bool {
	if acc == nil {
		return excluded
	}
	if excluded == nil {
		excluded = make(map[string]bool)
	}
	excluded[acc.ID.String()] = true
	return excluded
}

func (r *Relayer) pickNextExcluding(ch *db.Channel, pool *AccountPool, excluded map[string]bool) *db.Account {
	if pool == nil {
		return nil
	}
	if len(excluded) == 0 {
		return r.pickNext(ch, pool)
	}
	acc, ok := pool.PickExcluding(excluded)
	if !ok {
		return nil
	}
	return acc
}

func poolFromChannel(pm *PoolManager, ch *db.Channel) *AccountPool {
	if pm == nil || ch == nil {
		return nil
	}
	p, _ := pm.GetPool(ch.ID.String())
	return p
}

func (r *Relayer) accountAttemptLimit(ch *db.Channel) int {
	if r == nil || r.pools == nil || ch == nil {
		return 1
	}
	pool := poolFromChannel(r.pools, ch)
	if pool == nil {
		return 1
	}
	count := pool.AvailableCount()
	if count <= 0 {
		return 1
	}
	// Cap at 3 accounts per channel; beyond that, escalate to channel failover
	if count > 3 {
		count = 3
	}
	return count
}

func (r *Relayer) channelAttemptLimit() int {
	// Allow up to 3 channel failovers for 500 errors
	return 3
}

func (r *Relayer) prepareChannelFailover(ch *db.Channel, statusCode int, body []byte, model string) {
	logger.Warnf("relay.channel_failover", "channel failing over due to upstream error",
		logger.F("channel_id", ch.ID.String()),
		logger.F("channel_type", ch.Type),
		logger.F("status", statusCode),
		logger.F("model", model),
		logger.F("body_preview", compactLogBody(body)),
	)
	// Clear all affinity bindings for this channel (uses reverse index O(k))
	if r.affinity != nil {
		r.affinity.EvictChannel(ch.ID.String())
	}
	// 404 with model keyword → block (channel, model) pair for 5 minutes
	if statusCode == 404 && model != "" && r.channelModelBlock != nil {
		r.channelModelBlock.Block(ch.ID.String(), model)
	}
}

func requestAffinityScope(ctx *fasthttp.RequestCtx, body []byte) string {
	for _, value := range []string{
		requestClaudeAffinityScope(body),
		headerAffinityScope(ctx, "X-Session-ID", "header"),
		headerAffinityScope(ctx, "x-session-affinity", "opencode"),
		headerAffinityScope(ctx, "Session-Id", "codex"),
		headerAffinityScope(ctx, "Session_id", "codex"),
		headerAffinityScope(ctx, "X-Amp-Thread-Id", "amp"),
		headerAffinityScope(ctx, "X-Client-Request-Id", "clientreq"),
		headerAffinityScope(ctx, "Thread-Id", "thread"),
		requestJSONSessionValue(body),
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func headerAffinityScope(ctx *fasthttp.RequestCtx, name string, prefix string) string {
	if ctx == nil {
		return ""
	}
	value := strings.TrimSpace(string(ctx.Request.Header.Peek(name)))
	if value == "" {
		return ""
	}
	return prefix + ":" + value
}

func requestClaudeAffinityScope(body []byte) string {
	var root map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &root); err != nil {
		return ""
	}
	if metadata, ok := root["metadata"].(map[string]interface{}); ok {
		return sessionFromMetadataUserID(metadata["user_id"])
	}
	return sessionFromMetadataUserID(root["user_id"])
}

func requestJSONSessionValue(body []byte) string {
	var root map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &root); err != nil {
		return ""
	}
	for _, key := range []string{
		"prompt_cache_key",
		"session_id",
		"sessionId",
		"conversation_id",
		"conversationId",
		"conversation",
	} {
		if value := stringFromJSONValue(root[key]); value != "" {
			return "body:" + key + ":" + value
		}
	}
	for _, nestedKey := range []string{"metadata", "client_metadata"} {
		if nested, ok := root[nestedKey].(map[string]interface{}); ok {
			for _, key := range []string{"prompt_cache_key", "session_id", "sessionId", "conversation_id", "conversationId"} {
				if value := stringFromJSONValue(nested[key]); value != "" {
					return nestedKey + ":" + key + ":" + value
				}
			}
			if nestedKey == "metadata" {
				if value := stringFromJSONValue(nested["user_id"]); value != "" {
					return "user:" + value
				}
			}
		}
	}
	return ""
}

func sessionFromMetadataUserID(value interface{}) string {
	text := stringFromJSONValue(value)
	if text == "" {
		return ""
	}
	var nested map[string]interface{}
	if err := json.Unmarshal([]byte(text), &nested); err == nil {
		for _, key := range []string{"session_id", "sessionId", "conversation_id", "conversationId", "thread_id", "threadId"} {
			if value := stringFromJSONValue(nested[key]); value != "" {
				return "claude:" + value
			}
		}
	}
	if matches := claudeCodeSessionPattern.FindStringSubmatch(text); len(matches) >= 2 {
		return "claude:" + matches[1]
	}
	return ""
}

func stringFromJSONValue(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func (r *Relayer) refundAndError(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time, tokenPlanIDs ...uuid.UUID) {
	r.refundAndErrorWithStatus(ctx, tokenID, estTokens, msg, claims, ch, acc, model, start, fasthttp.StatusInternalServerError, tokenPlanIDs...)
}

func (r *Relayer) refundAndErrorWithStatus(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time, statusCode int, tokenPlanIDs ...uuid.UUID) {
	r.releaseLocalConcurrency(tokenID, claims)
	go r.finishFailureUsageWithErrorAndClientIP(claims, tokenID, ch.ID, acc.ID, model, false, start, statusCode, estTokens, msg, r.clientIPForDirectRequest(ctx, claims), tokenPlanIDs...)
	ctx.Error(`{"error":"`+httputil.JSONEscape(msg)+`"}`, statusCode)
}

func (r *Relayer) finishFailedBuffered(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string, claims *internalauth.Claims, ch *db.Channel, acc *db.Account, model string, start time.Time, statusCode int, tokenPlanIDs ...uuid.UUID) {
	r.releaseLocalConcurrency(tokenID, claims)
	go r.finishFailureUsageWithErrorAndClientIP(claims, tokenID, ch.ID, acc.ID, model, false, start, statusCode, estTokens, msg, r.clientIPForDirectRequest(ctx, claims), tokenPlanIDs...)
}

// normalizeErrorResponse converts an upstream error body into the client's expected format.
func normalizeErrorResponse(respBody []byte, clientFormat provider.Format, statusCode int) []byte {
	errMsg := errorMessageFromResponse(respBody)
	if statusCode == fasthttp.StatusRequestEntityTooLarge && (errMsg == "upstream error" || errMsg == "openai_error" || errMsg == "api_error") {
		errMsg = "upstream returned HTTP 413: request body too large"
	}
	switch clientFormat {
	case provider.FormatAnthropic, provider.FormatClaudeCode:
		return formatAnthropicError(errMsg)
	case provider.FormatGemini, provider.FormatGeminiCode, provider.FormatGeminiCLI, provider.FormatAntigravity:
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
	r.cooldownAccountOnTerminalUpstreamError(ch, acc, statusCode, respBody)
	ctx.SetStatusCode(statusCode)
	normalizedBody := normalizeErrorResponse(respBody, clientFormat, statusCode)
	ctx.Response.Header.SetContentType("application/json")
	ctx.SetBody(normalizedBody)
	go r.finishFailureUsageWithErrorAndClientIP(claims, tokenID, ch.ID, acc.ID, model, isStream, start, statusCode, estTokens, errorMessageFromResponse(respBody), r.clientIPForDirectRequest(ctx, claims), tokenPlanIDs...)
}

func injectStreamTrue(body []byte) []byte {
	var bodyMap map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &bodyMap); err != nil {
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
	if err := provider.DecodeJSONUseNumber(body, &root); err != nil {
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
	if depth > 32 {
		return value, false
	}
	changed := false
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if s, ok := child.(string); ok && isUndefinedPlaceholder(s) {
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
			if s, ok := child.(string); ok && isUndefinedPlaceholder(s) {
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
	if err := provider.DecodeJSONUseNumber(body, &bodyMap); err != nil {
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

func channelForceStreamForModel(ch *db.Channel, model, upstreamModel string) bool {
	if ch == nil {
		return false
	}
	models := channelForceStreamModels(ch.Settings)
	if len(models) == 0 {
		return false
	}
	candidates := []string{model, upstreamModel}
	for _, candidate := range candidates {
		normalized := strings.ToLower(strings.TrimSpace(candidate))
		if normalized == "" {
			continue
		}
		if _, ok := models[normalized]; ok {
			return true
		}
	}
	return false
}

func channelForceStreamModels(settings string) map[string]struct{} {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(settings), &raw); err != nil {
		return nil
	}
	values, ok := raw["force_stream_models"]
	if !ok {
		return nil
	}
	out := map[string]struct{}{}
	add := func(value string) {
		for _, item := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			item = strings.ToLower(strings.TrimSpace(item))
			if item != "" {
				out[item] = struct{}{}
			}
		}
	}
	switch typed := values.(type) {
	case []interface{}:
		for _, item := range typed {
			if s, ok := item.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, item := range typed {
			add(item)
		}
	case string:
		add(typed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeCodexResponsesRequest(body []byte, stream bool, clientMetadataSeed string) []byte {
	var bodyMap map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &bodyMap); err != nil {
		return body
	}
	if _, ok := bodyMap["instructions"]; !ok {
		bodyMap["instructions"] = ""
	}
	bodyMap["store"] = false
	bodyMap["stream"] = stream
	if _, ok := bodyMap["parallel_tool_calls"]; !ok {
		bodyMap["parallel_tool_calls"] = false
	}
	if _, ok := bodyMap["tool_choice"]; !ok {
		bodyMap["tool_choice"] = "auto"
	}
	normalizeCodexResponsesTextControls(bodyMap)
	if codexResponsesHasReasoning(bodyMap) {
		bodyMap["include"] = appendUniqueStringInclude(bodyMap["include"], "reasoning.encrypted_content")
	}
	normalizeCodexResponsesClientMetadata(bodyMap, clientMetadataSeed)
	sanitizeCodexReasoningEncryptedContent(bodyMap)
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
		"prompt_cache_retention",
		"response_format",
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

func normalizeCodexResponsesClientMetadata(bodyMap map[string]interface{}, seed string) {
	clientMetadata, _ := bodyMap["client_metadata"].(map[string]interface{})
	if clientMetadata == nil {
		clientMetadata = map[string]interface{}{}
	}
	if seed != "" {
		if installationID, _ := clientMetadata["x-codex-installation-id"].(string); strings.TrimSpace(installationID) == "" {
			clientMetadata["x-codex-installation-id"] = uuid.NewSHA1(uuid.NameSpaceOID, []byte("uapi:codex-installation:"+seed)).String()
		}
		if promptCacheKey, _ := bodyMap["prompt_cache_key"].(string); strings.TrimSpace(promptCacheKey) == "" {
			bodyMap["prompt_cache_key"] = uuid.NewSHA1(uuid.NameSpaceOID, []byte("uapi:codex-thread:"+seed)).String()
		}
	}
	promptCacheKey, _ := bodyMap["prompt_cache_key"].(string)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if promptCacheKey != "" {
		windowID := promptCacheKey + ":0"
		clientMetadata["x-codex-window-id"] = windowID
		turnID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("uapi:codex-turn:"+promptCacheKey)).String()
		if seed != "" {
			turnID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("uapi:codex-turn:"+seed+":"+promptCacheKey)).String()
		}
		if existing, _ := clientMetadata["x-codex-turn-metadata"].(string); strings.TrimSpace(existing) != "" {
			turnID = codexTurnIDFromMetadata(existing, turnID)
		}
		turnMetadata, err := json.Marshal(map[string]string{
			"prompt_cache_key": promptCacheKey,
			"turn_id":          turnID,
			"window_id":        windowID,
		})
		if err == nil {
			clientMetadata["x-codex-turn-metadata"] = string(turnMetadata)
		}
	}
	if len(clientMetadata) > 0 {
		bodyMap["client_metadata"] = clientMetadata
	}
}

func codexClientMetadataSeed(ch *db.Channel, acc *db.Account, scope string) string {
	if ch == nil || acc == nil {
		return ""
	}
	parts := []string{ch.ID.String(), acc.ID.String()}
	if strings.TrimSpace(scope) != "" {
		parts = append(parts, strings.TrimSpace(scope))
	}
	return strings.Join(parts, ":")
}

func applyCodexMetadataHeaders(req *fasthttp.Request, upstreamFormat provider.Format, body []byte) {
	if req == nil || upstreamFormat != provider.FormatCodexResponses {
		return
	}
	var bodyMap map[string]interface{}
	if err := provider.DecodeJSONUseNumber(body, &bodyMap); err != nil {
		return
	}
	clientMetadata, _ := bodyMap["client_metadata"].(map[string]interface{})
	if clientMetadata == nil {
		clientMetadata = map[string]interface{}{}
	}
	promptCacheKey, _ := bodyMap["prompt_cache_key"].(string)
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	// Mirror upstream codex_cli_rs core/src/client.rs:654-655 which emits
	// x-codex-window-id as an HTTP header on /responses requests.
	if windowID, _ := clientMetadata["x-codex-window-id"].(string); strings.TrimSpace(windowID) != "" {
		req.Header.Set("x-codex-window-id", strings.TrimSpace(windowID))
	} else if promptCacheKey != "" {
		req.Header.Set("x-codex-window-id", promptCacheKey+":0")
	}
	// Mirror upstream codex_cli_rs core/src/client.rs:1729-1731 — only emit
	// x-codex-turn-metadata when the value is actually present.
	if turnMetadata, _ := clientMetadata["x-codex-turn-metadata"].(string); strings.TrimSpace(turnMetadata) != "" {
		req.Header.Set("x-codex-turn-metadata", strings.TrimSpace(turnMetadata))
	}
	if promptCacheKey != "" {
		// Mirror codex-api/src/requests/headers.rs:5-13: session-id / thread-id
		// use lowercase hyphens. The upstream client does not emit a
		// Conversation_id header, so we drop it.
		req.Header.Set("session-id", promptCacheKey)
		req.Header.Set("thread-id", promptCacheKey)
		// x-client-request-id is the canonical request id header used by the
		// ChatGPT backend infrastructure; keep it populated so retries and
		// support traces correlate with our prompt cache key.
		req.Header.Set("x-client-request-id", promptCacheKey)
	}
}

func codexTurnIDFromMetadata(raw string, fallback string) string {
	var metadata map[string]interface{}
	if err := provider.DecodeJSONUseNumber([]byte(raw), &metadata); err != nil {
		return fallback
	}
	if turnID, _ := metadata["turn_id"].(string); strings.TrimSpace(turnID) != "" {
		return strings.TrimSpace(turnID)
	}
	return fallback
}

func sanitizeCodexReasoningEncryptedContent(bodyMap map[string]interface{}) {
	items, ok := bodyMap["input"].([]interface{})
	if !ok {
		return
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		if itemType, _ := item["type"].(string); itemType != "reasoning" {
			continue
		}
		rawEncrypted, exists := item["encrypted_content"]
		if !exists {
			continue
		}
		encrypted, ok := rawEncrypted.(string)
		if !ok || !isValidCodexReasoningEncryptedContent(encrypted) {
			delete(item, "encrypted_content")
		}
	}
}

func isValidCodexReasoningEncryptedContent(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !strings.HasPrefix(value, "gAAAA") || len(value) > 200000 {
		return false
	}
	for _, ch := range value {
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '=' {
			continue
		}
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) < 73 || decoded[0] != 0x80 {
		return false
	}
	ciphertextLen := len(decoded) - (1 + 8 + 16 + 32)
	return ciphertextLen > 0 && ciphertextLen%16 == 0
}

func codexResponsesHasReasoning(bodyMap map[string]interface{}) bool {
	raw, ok := bodyMap["reasoning"]
	if !ok || raw == nil {
		return false
	}
	if rawMap, ok := raw.(map[string]interface{}); ok {
		return len(rawMap) > 0
	}
	return true
}

func appendUniqueStringInclude(raw interface{}, value string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	switch typed := raw.(type) {
	case []interface{}:
		for _, item := range typed {
			if s, ok := item.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, item := range typed {
			add(item)
		}
	case string:
		add(typed)
	}
	add(value)
	return out
}

func normalizeCodexResponsesTextControls(bodyMap map[string]interface{}) {
	text, _ := bodyMap["text"].(map[string]interface{})
	if text == nil {
		text = map[string]interface{}{}
	}
	if verbosity, ok := codexTextVerbosity(bodyMap); ok {
		text["verbosity"] = verbosity
	}
	if _, hasFormat := text["format"]; !hasFormat {
		if format, ok := codexTextFormatFromResponseFormat(bodyMap["response_format"]); ok {
			text["format"] = format
		}
	}
	if len(text) > 0 {
		bodyMap["text"] = text
	}
}

func codexTextVerbosity(bodyMap map[string]interface{}) (string, bool) {
	for _, key := range []string{"verbosity", "text_verbosity"} {
		if value, ok := bodyMap[key].(string); ok {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "low", "medium", "high":
				delete(bodyMap, key)
				return strings.ToLower(strings.TrimSpace(value)), true
			}
		}
	}
	return "", false
}

func codexTextFormatFromResponseFormat(raw interface{}) (map[string]interface{}, bool) {
	format, ok := raw.(map[string]interface{})
	if !ok {
		return nil, false
	}
	if typ, _ := format["type"].(string); typ != "json_schema" {
		return nil, false
	}
	jsonSchema, ok := format["json_schema"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	schema, ok := jsonSchema["schema"]
	if !ok || schema == nil {
		return nil, false
	}
	name, _ := jsonSchema["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = "codex_output_schema"
	}
	strict, _ := jsonSchema["strict"].(bool)
	return map[string]interface{}{
		"type":   "json_schema",
		"strict": strict,
		"schema": schema,
		"name":   name,
	}, true
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

func isUndefinedPlaceholder(value string) bool {
	return strings.TrimSpace(value) == "[undefined]"
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
	if err := provider.DecodeJSONUseNumber(body, &bodyMap); err != nil {
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

// isAPIKeyChannel returns true if the channel uses API Key authentication
// (not OAuth, not Reverse). These channels should use transient failover
// instead of permanent cooldown on failures.
func isAPIKeyChannel(ch *db.Channel) bool {
	if ch == nil {
		return false
	}
	apiFormat := ch.APIFormat
	// Not OAuth
	if apiFormat == "codex" || apiFormat == "gemini_code" || apiFormat == "claude_code" || apiFormat == "antigravity" {
		return false
	}
	// Not Reverse
	if apiFormat == "chatgpt_reverse" {
		return false
	}
	return true
}

func isUpstreamQuotaExhausted(statusCode int, body []byte) bool {
	if statusCode == fasthttp.StatusTooManyRequests || statusCode == fasthttp.StatusPaymentRequired {
		return true
	}
	fields := collectErrorFields(body)
	status := strings.ToUpper(firstNonEmptyDisableString(
		stringField(fields, "error.status"),
		stringField(fields, "status"),
	))
	code := strings.ToLower(firstNonEmptyDisableString(
		stringField(fields, "error.code"),
		stringField(fields, "code"),
		stringField(fields, "error.type"),
		stringField(fields, "type"),
	))
	message := strings.ToLower(firstNonEmptyDisableString(
		stringField(fields, "error.message"),
		stringField(fields, "message"),
		stringField(fields, "detail"),
	))
	if status == "RESOURCE_EXHAUSTED" || strings.Contains(code, "insufficient_quota") || strings.Contains(code, "quota") || strings.Contains(code, "rate_limit") {
		return true
	}
	for _, marker := range []string{
		"quota exceeded",
		"quota exhausted",
		"usage limit",
		"rate limit",
		"too many requests",
		"capacity exceeded",
		"no capacity",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
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
	pt, ct, _, _ := parseNonStreamUsageFull(respBody)
	return pt, ct
}

func parseNonStreamUsageFull(respBody []byte) (int, int, int, int) {
	var resp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(respBody, &resp) == nil && (resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0) {
		creation, read := nonStreamCacheTokens(respBody)
		return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, creation, read
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		creation, read := nonStreamCacheTokens(respBody)
		return resp.Usage.InputTokens, resp.Usage.OutputTokens, creation, read
	}
	creation, read := nonStreamCacheTokens(respBody)
	return 0, 0, creation, read
}

func requestJSONBool(body []byte, key string) interface{} {
	var root map[string]interface{}
	if json.Unmarshal(body, &root) != nil {
		return nil
	}
	value, ok := root[key].(bool)
	if !ok {
		return nil
	}
	return value
}

func relayDebugRequestHeaders(req *fasthttp.Request) map[string]string {
	if req == nil {
		return nil
	}
	headers := make(map[string]string)
	req.Header.VisitAll(func(k, v []byte) {
		key := string(k)
		lower := strings.ToLower(key)
		switch lower {
		case "authorization", "x-api-key", "api-key", "anthropic-api-key", "x-goog-api-key", "cookie", "set-cookie":
			headers[key] = "[redacted]"
		default:
			headers[key] = string(v)
		}
	})
	return headers
}

func nonStreamCacheTokens(respBody []byte) (int, int) {
	var root map[string]interface{}
	if json.Unmarshal(respBody, &root) != nil {
		return 0, 0
	}
	if usage, _ := root["usage"].(map[string]interface{}); usage != nil {
		return usageCacheTokens(usage)
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

func getAdaptorForChannel(ch *db.Channel) provider.Adaptor {
	if ch != nil && ch.APIFormat == "chatgpt_reverse" {
		return &chatgptreverse.Adaptor{}
	}
	if ch == nil {
		return nil
	}
	return getAdaptor(ch.Type)
}

func channelSupportsCapability(ch db.Channel, reqs ...channelcap.Request) bool {
	if len(reqs) == 0 {
		return true
	}
	for _, req := range reqs {
		if req.Kind == "" {
			continue
		}
		if !channelcap.Supports(ch, req) {
			return false
		}
	}
	return true
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

func isCodexAPIFormat(format string) bool {
	return format == "codex"
}

func permissionForFormat(format provider.Format) string {
	switch format {
	case provider.FormatAnthropic, provider.FormatClaudeCode:
		return "messages"
	case provider.FormatGemini, provider.FormatGeminiCode, provider.FormatGeminiCLI, provider.FormatAntigravity:
		return "gemini"
	case provider.FormatOpenAIResponses, provider.FormatCodexResponses:
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
	r.writeLogWithRoutedModelFormatsErrorAndCache(tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, pt, ct, 0, 0, start, statusCode, errorMessage, clientIPs...)
}

func (r *Relayer) writeLogWithRoutedModelFormatsErrorAndCache(tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, pt, ct, cacheCreationTokens, cacheReadTokens int, start time.Time, statusCode int, errorMessage string, clientIPs ...string) {
	r.writeLogWithRoutedModelFormatsErrorCacheAndAdmin(tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, errorMessage, nil, clientIPs...)
}

func (r *Relayer) writeLogWithRoutedModelFormatsErrorCacheAndAdmin(tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, pt, ct, cacheCreationTokens, cacheReadTokens int, start time.Time, statusCode int, errorMessage string, adminInfo map[string]interface{}, clientIPs ...string) {
	if r.db == nil {
		return
	}
	clientIP := ""
	if len(clientIPs) > 0 {
		clientIP = strings.TrimSpace(clientIPs[0])
	}
	logEntry := db.Log{
		TokenID:             toUUID(tokenID),
		ClientIP:            clientIP,
		ChannelID:           toUUID(channelID),
		AccountID:           toUUID(accountID),
		Model:               model,
		RoutedModel:         normalizedRoutedModel(model, routedModel),
		ClientFormat:        string(clientFormat),
		UpstreamFormat:      string(upstreamFormat),
		IsStream:            isStream,
		PromptTokens:        int64(pt),
		CompletionTokens:    int64(ct),
		CacheCreationTokens: int64(cacheCreationTokens),
		CacheReadTokens:     int64(cacheReadTokens),
		TotalTokens:         int64(pt + ct),
		LatencyMs:           time.Since(start).Milliseconds(),
		StatusCode:          statusCode,
		ErrorMessage:        logger.Redact(errorMessage),
		AdminInfo:           cloneLogAdminInfo(adminInfo),
	}
	if err := r.db.Create(&logEntry).Error; err != nil {
		logger.Warnf("relay.logs", "write request log failed", logger.Err(err))
	}
}

func cloneLogAdminInfo(info map[string]interface{}) map[string]interface{} {
	if len(info) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(info))
	for key, value := range info {
		out[key] = value
	}
	return out
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
	r.finishUsageWithRoutedModelFormatsAndCache(claims, tokenID, tokenPlanID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, pt, ct, 0, 0, start, statusCode, estTokens, fallbackClientIPs...)
}

func (r *Relayer) finishUsageWithRoutedModelFormatsAndCache(claims *internalauth.Claims, tokenID, tokenPlanID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, pt, ct, cacheCreationTokens, cacheReadTokens int, start time.Time, statusCode int, estTokens int, fallbackClientIPs ...string) {
	r.finishUsageWithRoutedModelFormatsCacheAndAdmin(claims, tokenID, tokenPlanID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, estTokens, nil, fallbackClientIPs...)
}

func (r *Relayer) finishUsageWithRoutedModelFormatsCacheAndAdmin(claims *internalauth.Claims, tokenID, tokenPlanID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, pt, ct, cacheCreationTokens, cacheReadTokens int, start time.Time, statusCode int, estTokens int, adminInfo map[string]interface{}, fallbackClientIPs ...string) {
	planID := requestTokenPlanID(claims, tokenPlanID)
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, routedModel, clientFormat, upstreamFormat, isStream, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLogWithRoutedModelFormatsErrorCacheAndAdmin(tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, pt, ct, cacheCreationTokens, cacheReadTokens, start, statusCode, "", adminInfo, firstClientIP(clientIPFromClaims(claims), fallbackClientIPs...))
	if r.billing != nil {
		go func() {
			if err := r.billing.DBTransactionRefundAndSettle(toUUID(tokenID).String(), planID, estTokens, pt, ct, cacheCreationTokens, cacheReadTokens, model); err != nil {
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
	r.finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims, tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, start, statusCode, estTokens, errorMessage, fallbackClientIP, nil, tokenPlanIDs...)
}

func (r *Relayer) finishFailureUsageWithRoutedModelFormatsErrorClientIPAndAdmin(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model, routedModel string, isStream bool, clientFormat, upstreamFormat provider.Format, start time.Time, statusCode int, estTokens int, errorMessage string, fallbackClientIP string, adminInfo map[string]interface{}, tokenPlanIDs ...uuid.UUID) {
	planID := requestTokenPlanID(claims, firstUUID(tokenPlanIDs))
	if claims != nil && claims.RequestID != "" && r.reportUsageEvent(claims, tokenID, channelID, accountID, model, routedModel, clientFormat, upstreamFormat, isStream, 0, 0, 0, 0, start, statusCode, estTokens) == nil {
		return
	}
	r.writeLogWithRoutedModelFormatsErrorCacheAndAdmin(tokenID, channelID, accountID, model, routedModel, isStream, clientFormat, upstreamFormat, 0, 0, 0, 0, start, statusCode, errorMessage, adminInfo, firstClientIP(clientIPFromClaims(claims), fallbackClientIP))
	if r.billing != nil {
		go func() {
			if err := r.billing.DBTransactionRefundPreConsume(toUUID(tokenID).String(), planID, estTokens, model); err != nil {
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

func (r *Relayer) clientIPForDirectRequest(ctx *fasthttp.RequestCtx, claims *internalauth.Claims) string {
	if claims != nil && claims.RequestID != "" {
		return ""
	}
	return httputil.ClientIPForLog(ctx, r.trustedProxies)
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

func (r *Relayer) reportUsageEvent(claims *internalauth.Claims, tokenID, channelID, accountID interface{}, model, routedModel string, clientFormat, upstreamFormat provider.Format, isStream bool, pt, ct, cacheCreationTokens, cacheReadTokens int, start time.Time, statusCode int, estTokens int) error {
	if r.controlURL == "" {
		return fmt.Errorf("control url not configured")
	}
	payload := map[string]interface{}{
		"request_id":            claims.RequestID,
		"token_id":              toUUID(tokenID),
		"token_plan_id":         toUUID(claims.TokenPlanID),
		"channel_id":            toUUID(channelID),
		"account_id":            toUUID(accountID),
		"model":                 model,
		"routed_model":          normalizedRoutedModel(model, routedModel),
		"client_format":         string(clientFormat),
		"upstream_format":       string(upstreamFormat),
		"is_stream":             isStream,
		"prompt_tokens":         pt,
		"completion_tokens":     ct,
		"cache_creation_tokens": cacheCreationTokens,
		"cache_read_tokens":     cacheReadTokens,
		"estimated_tokens":      estTokens,
		"status_code":           statusCode,
		"latency_ms":            time.Since(start).Milliseconds(),
		"client_ip":             claims.ClientIP,
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

func shouldInjectConvertedStreamField(upstreamFormat provider.Format) bool {
	switch upstreamFormat {
	case provider.FormatOpenAIChatCompletions, provider.FormatOpenAIResponses, provider.FormatAnthropic, provider.FormatClaudeCode, provider.FormatCodexResponses:
		return true
	default:
		return false
	}
}

func isLikelyNativeCodexClientRequest(ctx *fasthttp.RequestCtx) bool {
	originator := strings.ToLower(strings.TrimSpace(string(ctx.Request.Header.Peek("originator"))))
	if originator == strings.ToLower(openai.CodexOriginator) {
		return true
	}
	userAgent := strings.ToLower(string(ctx.Request.Header.UserAgent()))
	return strings.Contains(userAgent, "codex")
}

// HTTPDoer is an optional interface adaptors may implement to take over the
// upstream HTTP roundtrip. Used by chatgpt_reverse to apply TLS fingerprint
// impersonation (utls) instead of fasthttp's native TLS stack.
type HTTPDoer interface {
	DoHTTPRequest(req *fasthttp.Request, resp *fasthttp.Response) error
}

// doUpstreamStreaming routes a streaming request through HTTPDoer if the
// adaptor implements it; otherwise falls back to the default fasthttp client.
func doUpstreamStreaming(adaptor provider.Adaptor, req *fasthttp.Request, resp *fasthttp.Response) error {
	if d, ok := adaptor.(HTTPDoer); ok {
		return d.DoHTTPRequest(req, resp)
	}
	return doStreamingRequest(req, resp)
}

// doUpstreamBuffered routes a buffered request through HTTPDoer if the
// adaptor implements it; otherwise falls back to the buffered fasthttp client.
func doUpstreamBuffered(adaptor provider.Adaptor, bufferedClient *fasthttp.Client, req *fasthttp.Request, resp *fasthttp.Response) error {
	if d, ok := adaptor.(HTTPDoer); ok {
		return d.DoHTTPRequest(req, resp)
	}
	return bufferedClient.Do(req, resp)
}

func doStreamingRequest(req *fasthttp.Request, resp *fasthttp.Response) error {
	var lastErr error
	snapshot := fasthttp.AcquireRequest()
	req.CopyTo(snapshot)
	defer fasthttp.ReleaseRequest(snapshot)

	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			req.Reset()
			snapshot.CopyTo(req)
			resp.Reset()
		}
		if err := doStreamingRequestOnce(req, resp); err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "streaming request panic") || isRetryableStreamingRequestError(err) {
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func doStreamingRequestOnce(req *fasthttp.Request, resp *fasthttp.Response) (err error) {
	client := newStreamingClient()
	defer func() {
		if rec := recover(); rec != nil {
			client.CloseIdleConnections()
			err = fmt.Errorf("streaming request panic: %v\n%s", rec, debug.Stack())
		} else if err != nil {
			client.CloseIdleConnections()
		}
	}()
	return client.Do(req, resp)
}

func newStreamingClient() *fasthttp.Client {
	return &fasthttp.Client{
		ReadTimeout:        0,
		WriteTimeout:       0,
		MaxConnDuration:    0,
		StreamResponseBody: true,
	}
}

func isRetryableStreamingRequestError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "dialing to the given tcp address timed out") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "server closed connection before returning the first response byte") ||
		strings.Contains(msg, "no such host")
}

func releaseStreamingResponse(resp *fasthttp.Response) {
	defer func() {
		_ = recover()
	}()
	fasthttp.ReleaseResponse(resp)
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

func (r *Relayer) settleAndRefund(tokenID string, tokenPlanID uuid.UUID, reqBody, respBody []byte, adaptor provider.Adaptor, estTokens int, model string) (int, int, int, int) {
	if r.billing == nil {
		return 0, 0, 0, 0
	}
	pt, ct, cc, cr := 0, 0, 0, 0
	if adaptor != nil && len(respBody) > 0 {
		if usage, err := adaptor.ParseUsageFull(respBody); err == nil {
			pt, ct = usage.PromptTokens, usage.CompletionTokens
			cc, cr = usage.CacheCreationInputTokens, usage.CacheReadInputTokens
			if cr == 0 {
				cr = usage.PromptCacheHitTokens
			}
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
	return pt, ct, cc, cr
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
