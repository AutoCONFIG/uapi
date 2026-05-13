package relay

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/crypto"
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/AutoCONFIG/cli-relay/internal/relay/anthropic"
	"github.com/AutoCONFIG/cli-relay/internal/relay/gemini"
	"github.com/AutoCONFIG/cli-relay/internal/relay/openai"
	"github.com/AutoCONFIG/cli-relay/internal/relay/types"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type Relayer struct {
	db      *gorm.DB
	pools   *PoolManager
	billing *BillingService
}

func NewRelayer(database *gorm.DB, pools *PoolManager, billing *BillingService) *Relayer {
	return &Relayer{db: database, pools: pools, billing: billing}
}

type relayRequest struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

func (r *Relayer) HandleRelay(ctx *fasthttp.RequestCtx) {
	start := time.Now()
	path := string(ctx.Path())

	// 1. Extract and validate Bearer token
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

	// 2. Check billing limits
	if r.billing != nil {
		if err := r.billing.CheckLimit(token.ID.String()); err != nil {
			ctx.Error(`{"error":"rate limit exceeded"}`, 429)
			return
		}
	}

	// 3. Parse model from request body
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

	// 4. Find channel that supports this model
	var channels []db.Channel
	if err := r.db.Where("enabled = true AND deleted_at IS NULL").Order("priority DESC").Find(&channels).Error; err != nil {
		ctx.Error(`{"error":"internal error"}`, fasthttp.StatusInternalServerError)
		return
	}
	var targetChannel *db.Channel
	for i := range channels {
		if modelInList(req.Model, channels[i].Models) {
			targetChannel = &channels[i]
			break
		}
	}
	if targetChannel == nil {
		ctx.Error(`{"error":"no available channel for model"}`, fasthttp.StatusNotFound)
		return
	}

	// 5. Get adaptor for channel type
	adaptor := getAdaptor(targetChannel.Type)
	if adaptor == nil {
		ctx.Error(`{"error":"unsupported channel type"}`, fasthttp.StatusInternalServerError)
		return
	}

	// 6. Pick account from pool
	pool, ok := r.pools.GetPool(targetChannel.ID.String())
	if !ok {
		ctx.Error(`{"error":"channel pool not initialized"}`, fasthttp.StatusServiceUnavailable)
		return
	}

	account, ok := pool.Pick()
	if !ok {
		ctx.Error(`{"error":"no available account"}`, fasthttp.StatusServiceUnavailable)
		return
	}

	// 7. Pre-consume billing
	estimatedTokens := req.MaxTokens
	if estimatedTokens == 0 {
		estimatedTokens = 1000 // rough estimate
	}
	if r.billing != nil {
		_ = r.billing.PreConsume(token.ID.String(), req.Model, estimatedTokens)
	}

	// 8. Decrypt credentials
	creds, err := crypto.Decrypt(account.Credentials)
	if err != nil {
		log.Printf("decrypt credentials error: %v", err)
		ctx.Error(`{"error":"internal error"}`, fasthttp.StatusInternalServerError)
		return
	}

	// 9. Init adaptor and build upstream request
	adaptor.Init(targetChannel, account)
	upstreamURL, err := adaptor.GetRequestURL(path)
	if err != nil {
		ctx.Error(`{"error":"build url failed"}`, fasthttp.StatusInternalServerError)
		return
	}
	convertedBody, err := adaptor.ConvertRequest(body)
	if err != nil {
		ctx.Error(`{"error":"convert request failed"}`, fasthttp.StatusBadRequest)
		return
	}

	// 10. Send request with retry on failure
	var respBody []byte
	var statusCode int
	var respHeaders fasthttp.ResponseHeader
	var respAccount *db.Account
	maxRetries := 3

	for retry := 0; retry < maxRetries; retry++ {
		upReq := fasthttp.AcquireRequest()
		upResp := fasthttp.AcquireResponse()

		upReq.SetRequestURI(upstreamURL)
		upReq.Header.SetMethodBytes(ctx.Method())
		upReq.SetBody(convertedBody)
		if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			ctx.Error(`{"error":"setup headers failed"}`, fasthttp.StatusInternalServerError)
			return
		}

		err := fasthttp.Do(upReq, upResp)
		fasthttp.ReleaseRequest(upReq)

		if err != nil {
			log.Printf("upstream request error (retry %d): %v", retry, err)
			fasthttp.ReleaseResponse(upResp)
			pool.Cooldown(account.ID.String(), 5*time.Minute)
			account, ok = pool.Pick()
			if !ok {
				break
			}
			respAccount = account
			adaptor.Init(targetChannel, account)
			creds, _ = crypto.Decrypt(account.Credentials)
			continue
		}

		if upResp.StatusCode() >= 500 {
			log.Printf("upstream %d error (retry %d)", upResp.StatusCode(), retry)
			pool.Cooldown(account.ID.String(), 5*time.Minute)
			// Copy data before releasing
			respBody = make([]byte, len(upResp.Body()))
			copy(respBody, upResp.Body())
			statusCode = upResp.StatusCode()
			upResp.Header.VisitAll(func(k, v []byte) { respHeaders.SetBytesKV(k, v) })
			fasthttp.ReleaseResponse(upResp)
			account, ok = pool.Pick()
			if !ok {
				break
			}
			respAccount = account
			adaptor.Init(targetChannel, account)
			creds, _ = crypto.Decrypt(account.Credentials)
			continue
		}

		// Success
		respBody = make([]byte, len(upResp.Body()))
		copy(respBody, upResp.Body())
		statusCode = upResp.StatusCode()
		upResp.Header.VisitAll(func(k, v []byte) { respHeaders.SetBytesKV(k, v) })
		fasthttp.ReleaseResponse(upResp)
		respAccount = account
		break
	}

	if respBody == nil || respAccount == nil {
		// Refund on failure
		if r.billing != nil {
			r.billing.Refund(token.ID.String(), estimatedTokens)
		}
		ctx.Error(`{"error":"all retries exhausted"}`, fasthttp.StatusServiceUnavailable)
		return
	}

	// 11. Forward response
	ctx.SetStatusCode(statusCode)
	respHeaders.VisitAll(func(key, value []byte) {
		ctx.Response.Header.SetBytesKV(key, value)
	})

	if req.Stream {
		ctx.Response.Header.Set("Content-Type", "text/event-stream")
		ctx.Response.Header.Set("Cache-Control", "no-cache")
		ctx.Response.Header.Set("Connection", "keep-alive")
		ctx.Response.Header.Set("X-Accel-Buffering", "no")
		// Parse usage from the buffered body before streaming to client
		lastData := extractLastSSEData(respBody)
		if pt, ct, err := adaptor.ParseStreamUsage(lastData); err == nil && pt > 0 {
			go r.writeLog(token.ID, targetChannel.ID, respAccount.ID, req.Model, true, pt, ct, start)
			if r.billing != nil {
				go r.billing.Settle(token.ID.String(), pt, ct, req.Model)
			}
		}
		// Stream buffered body to client
		ctx.SetBody(respBody)
	} else {
		ctx.SetBody(respBody)
		if pt, ct, err := adaptor.ParseUsage(respBody); err == nil {
			go r.writeLog(token.ID, targetChannel.ID, respAccount.ID, req.Model, false, pt, ct, start)
			if r.billing != nil {
				go r.billing.Settle(token.ID.String(), pt, ct, req.Model)
			}
		}
	}
}

// extractLastSSEData finds the last "data: " line (that isn't [DONE]) from buffered SSE body.
func extractLastSSEData(body []byte) []byte {
	var lastData []byte
	lines := bytes.Split(body, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			if !bytes.Equal(data, []byte("[DONE]")) {
				lastData = data
			}
		}
	}
	return lastData
}

func extractBearerToken(ctx *fasthttp.RequestCtx) string {
	auth := string(ctx.Request.Header.Peek("Authorization"))
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}

func getAdaptor(channelType string) types.Adaptor {
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

func (r *Relayer) writeLog(tokenID, channelID, accountID interface{}, model string, isStream bool, pt, ct int, start time.Time) {
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
		StatusCode:       200,
	}
	if err := r.db.Create(&logEntry).Error; err != nil {
		log.Printf("write log error: %v", err)
	}
}

func toUUID(v interface{}) [16]byte {
	if id, ok := v.([16]byte); ok {
		return id
	}
	return [16]byte{}
}
