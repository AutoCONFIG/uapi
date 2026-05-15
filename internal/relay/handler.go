package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/relay/provider"
	"github.com/AutoCONFIG/cli-relay/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/cli-relay/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/cli-relay/internal/relay/provider/openai"
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

type Relayer struct {
	db        *gorm.DB
	pools     *PoolManager
	billing   *BillingService
	affinity  *AffinityCache
	concLimiter *ConcurrencyLimiter
}

func NewRelayer(database *gorm.DB, pools *PoolManager, billing *BillingService, affinity *AffinityCache, concLimit int) *Relayer {
	return &Relayer{
		db:          database,
		pools:       pools,
		billing:     billing,
		affinity:    affinity,
		concLimiter: NewConcurrencyLimiter(concLimit),
	}
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
	default:
		clientFormat = provider.FormatOpenAIChat // backward compat
	}

	// 1. Auth
	tokenKey := extractBearerToken(ctx)
	if tokenKey == "" {
		ctx.Error(`{"error":"missing authorization"}`, fasthttp.StatusUnauthorized)
		return
	}
	var token db.Token
	if err := r.db.Where("key = ? AND enabled = true AND deleted_at IS NULL", tokenKey).First(&token).Error; err != nil {
		ctx.Error(`{"error":"invalid token"}`, fasthttp.StatusUnauthorized)
		return
	}

	// 2. Concurrency check
	tokenID := token.ID.String()
	if !r.concLimiter.Acquire(tokenID) {
		ctx.Error(`{"error":"concurrent request limit exceeded"}`, 429)
		return
	}
	defer r.concLimiter.Release(tokenID)

	// 3. Billing check
	if r.billing != nil {
		if err := r.billing.CheckLimit(token.ID.String()); err != nil {
			ctx.Error(`{"error":"rate limit exceeded"}`, 429)
			return
		}
		// Check user balance if token is linked to a user
		if token.UserID != "" {
			if err := r.billing.CheckUserBalance(token.UserID); err != nil {
				ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, 402)
				return
			}
		}
	}

	// 3. Parse request
	var req relayRequest
	body := ctx.PostBody()
	if err := json.Unmarshal(body, &req); err != nil {
		ctx.Error(`{"error":"invalid request body"}`, fasthttp.StatusBadRequest)
		return
	}
	if req.Model == "" {
		ctx.Error(`{"error":"model is required"}`, fasthttp.StatusBadRequest)
		return
	}

	// 4. Find channel + account
	targetChannel, account, adaptor, creds, err := r.resolveChannelAndAccount(token.ID.String(), req.Model)
	if err != nil {
		ctx.Error(`{"error":"`+jsonEscape(err.Error())+`"}`, fasthttp.StatusNotFound)
		return
	}

	// 5. Pre-consume billing
	estimatedTokens := req.MaxTokens
	if estimatedTokens == 0 {
		estimatedTokens = 1000
	}
	if r.billing != nil {
		_ = r.billing.PreConsume(token.ID.String(), req.Model, estimatedTokens)
	}

	// 6. Build upstream request
	adaptor.Init(targetChannel, account)
	upstreamURL, err := adaptor.GetRequestURL(path)
	if err != nil {
		if r.billing != nil {
			go r.billing.Refund(token.ID.String(), estimatedTokens)
		}
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
		if targetChannel.APIFormat == "responses" {
			upstreamFormat = provider.FormatOpenAIResp
		} else {
			upstreamFormat = provider.FormatOpenAIChat
		}
	case "anthropic":
		upstreamFormat = provider.FormatAnthropic
	case "gemini":
		upstreamFormat = provider.FormatGemini
	default:
		upstreamFormat = provider.FormatOpenAIChat
	}

	convertedBody, err := provider.ConvertRequest(clientFormat, upstreamFormat, body)
	if err != nil {
		if r.billing != nil {
			go r.billing.Refund(token.ID.String(), estimatedTokens)
		}
		ctx.Error(`{"error":"convert request failed: `+jsonEscape(err.Error())+`"}`, fasthttp.StatusBadRequest)
		return
	}

	// 7. Dispatch
	if req.Stream && !forceStreamActive {
		r.handleStreaming(ctx, token, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, start, estimatedTokens)
	} else if forceStreamActive {
		r.handleForceStream(ctx, token, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, start, estimatedTokens)
	} else {
		r.handleBuffered(ctx, token, targetChannel, account, adaptor, upstreamURL, convertedBody, creds, req.Model, start, estimatedTokens)
	}

	// 8. Record affinity
	if targetChannel.AffinityTTL > 0 {
		r.affinity.Set(token.ID.String(), req.Model, targetChannel.ID.String(), targetChannel.AffinityTTL)
	}
}

// handleStreaming: real-time chunk-by-chunk forwarding using SSEStreamReader.
func (r *Relayer) handleStreaming(ctx *fasthttp.RequestCtx, token db.Token, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, start time.Time, estTokens int) {
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed")
		return
	}

	// streamingClient returns after receiving headers, body streamed via BodyStream
	if err := streamingClient.Do(upReq, upResp); err != nil {
		log.Printf("streaming upstream error: %v", err)
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error")
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		respBody := upResp.Body()
		bodyCopy := make([]byte, len(respBody))
		copy(bodyCopy, respBody)
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, false, start)
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
	var convertLine func([]byte) []byte
	if adaptor.GetChannelType() != "openai" {
		convertLine = adaptor.ConvertStreamLine
	}

	// Producer goroutine: owns upReq/upResp lifecycle, releases when done
	go func() {
		defer fasthttp.ReleaseRequest(upReq)
		defer fasthttp.ReleaseResponse(upResp)

		result := streamAndForward(upResp.BodyStream(), reader, tracker, convertLine)
		if result.err != nil {
			log.Printf("stream forward error: %v", result.err)
		}
		pt, ct := tracker.Result()
		go r.writeLog(token.ID, ch.ID, acc.ID, model, true, pt, ct, start, statusCode)
		if r.billing != nil {
			go r.billing.Refund(token.ID.String(), estTokens)
			if pt > 0 || ct > 0 {
				go r.billing.Settle(token.ID.String(), pt, ct, model)
			}
		}
	}()
}

// handleForceStream: stream to upstream, buffer all, convert to non-stream for downstream.
func (r *Relayer) handleForceStream(ctx *fasthttp.RequestCtx, token db.Token, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, start time.Time, estTokens int) {
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	upReq.SetRequestURI(url)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(body)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed")
		return
	}

	if err := streamingClient.Do(upReq, upResp); err != nil {
		log.Printf("force stream upstream error: %v", err)
		r.refundAndError(ctx, token.ID.String(), estTokens, "upstream error")
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		respBody := upResp.Body()
		bodyCopy := make([]byte, len(respBody))
		copy(bodyCopy, respBody)
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, bodyCopy, ch, acc, model, false, start)
		return
	}

	// Buffer entire stream
	respBody, err := io.ReadAll(upResp.BodyStream())
	if err != nil {
		log.Printf("force stream read error: %v", err)
		r.refundAndError(ctx, token.ID.String(), estTokens, "read upstream error")
		return
	}

	// TODO: Response format conversion (requires reverse converters, upstreamFormat -> clientFormat)
	// SSE -> non-stream JSON
	respBody = StreamToNonStream(respBody)

	ctx.SetStatusCode(statusCode)
	ctx.Response.Header.Set("Content-Type", "application/json")
	ctx.SetBody(respBody)

	pt, ct := parseNonStreamUsage(respBody)
	go r.writeLog(token.ID, ch.ID, acc.ID, model, false, pt, ct, start, statusCode)
	if r.billing != nil {
		go r.billing.Refund(token.ID.String(), estTokens)
		if pt > 0 || ct > 0 {
			go r.billing.Settle(token.ID.String(), pt, ct, model)
		}
	}
}

// handleBuffered: standard buffered request with retry.
func (r *Relayer) handleBuffered(ctx *fasthttp.RequestCtx, token db.Token, ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, url string, body []byte, creds string, model string, start time.Time, estTokens int) {
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
			r.refundAndError(ctx, token.ID.String(), estTokens, "setup headers failed")
			return
		}

		err := fasthttp.Do(upReq, upResp)
		fasthttp.ReleaseRequest(upReq)

		shouldRetry := false
		if err != nil {
			log.Printf("upstream error (retry %d): %v", retry, err)
			shouldRetry = true
		} else if upResp.StatusCode() >= 500 {
			log.Printf("upstream %d (retry %d)", upResp.StatusCode(), retry)
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
			currentCreds, err = EnsureValidCredentials(currentAccount, r.db)
			if err != nil {
				log.Printf("decrypt error on retry %d: %v", retry, err)
				currentAccount = r.retryNext(ch, currentAccount)
				if currentAccount == nil {
					break
				}
				respAccount = currentAccount
				adaptor.Init(ch, currentAccount)
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
		if r.billing != nil {
			r.billing.Refund(token.ID.String(), estTokens)
		}
		ctx.Error(`{"error":"all retries exhausted"}`, fasthttp.StatusServiceUnavailable)
		return
	}

	if statusCode >= 400 {
		r.refundOnError(ctx, token.ID.String(), estTokens, statusCode, respBody, ch, respAccount, model, false, start)
		return
	}

	// TODO: Response format conversion (requires reverse converters, upstreamFormat -> clientFormat)

	ctx.SetStatusCode(statusCode)
	respHeaders.VisitAll(func(key, value []byte) {
		ctx.Response.Header.SetBytesKV(key, value)
	})
	ctx.SetBody(respBody)

	r.settleAndRefund(token.ID.String(), respBody, adaptor, estTokens, model)
	pt, ct := parseNonStreamUsage(respBody)
	go r.writeLog(token.ID, ch.ID, respAccount.ID, model, false, pt, ct, start, statusCode)
}

// --- Helpers ---

func (r *Relayer) resolveChannelAndAccount(tokenID, model string) (*db.Channel, *db.Account, provider.Adaptor, string, error) {
	// Try affinity cache first
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

	// Priority-based selection
	var channels []db.Channel
	if err := r.db.Where("enabled = true AND deleted_at IS NULL").Order("priority DESC").Find(&channels).Error; err != nil {
		return nil, nil, nil, "", err
	}
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
	creds, err := EnsureValidCredentials(account, r.db)
	if err != nil {
		return nil, nil, "", fmt.Errorf("decrypt error")
	}
	return account, adaptor, creds, nil
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

func (r *Relayer) refundAndError(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, msg string) {
	if r.billing != nil {
		r.billing.Refund(tokenID, estTokens)
	}
	ctx.Error(`{"error":"`+jsonEscape(msg)+`"}`, fasthttp.StatusInternalServerError)
}

func (r *Relayer) refundOnError(ctx *fasthttp.RequestCtx, tokenID string, estTokens int, statusCode int, respBody []byte, ch *db.Channel, acc *db.Account, model string, isStream bool, start time.Time) {
	if r.billing != nil {
		r.billing.Refund(tokenID, estTokens)
	}
	ctx.SetStatusCode(statusCode)
	ctx.SetBody(respBody)
	go r.writeLog(tokenID, ch.ID, acc.ID, model, isStream, 0, 0, start, statusCode)
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

func copyBody(resp *fasthttp.Response) []byte {
	b := resp.Body()
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func copyHeaders(resp *fasthttp.Response, dst *fasthttp.ResponseHeader) {
	resp.Header.VisitAll(func(k, v []byte) { dst.SetBytesKV(k, v) })
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
	return ""
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

func (r *Relayer) writeLog(tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time, statusCode int) {
	logEntry := db.Log{
		TokenID:          toUUID(tokenID),
		ChannelID:        toUUID(channelID),
		AccountID:        toUUID(accountID),
		Model:            model,
		IsStream:         isStream,
		PromptTokens:     pt,
		CompletionTokens: ct,
		TotalTokens:      pt + ct,
		LatencyMs:        int(time.Since(start).Milliseconds()),
		StatusCode:       statusCode,
	}
	if err := r.db.Create(&logEntry).Error; err != nil {
		log.Printf("write log error: %v", err)
	}
}

func toUUID(v interface{}) uuid.UUID {
	if id, ok := v.(uuid.UUID); ok {
		return id
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

func (r *Relayer) settleAndRefund(tokenID string, respBody []byte, adaptor provider.Adaptor, estTokens int, model string) {
	if r.billing == nil {
		return
	}
	go r.billing.Refund(tokenID, estTokens)
	if pt, ct, err := adaptor.ParseUsage(respBody); err == nil && (pt > 0 || ct > 0) {
		go r.billing.Settle(tokenID, pt, ct, model)
	}
}
