package internalauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
)

const (
	HeaderGatewayID       = "X-UAPI-Gateway-ID"
	HeaderTimestamp       = "X-UAPI-Timestamp"
	HeaderSignature       = "X-UAPI-Signature"
	HeaderTokenID         = "X-UAPI-Token-ID"
	HeaderTokenPlanID     = "X-UAPI-Token-Plan-ID"
	HeaderUserID          = "X-UAPI-User-ID"
	HeaderModel           = "X-UAPI-Model"
	HeaderEstimatedTokens = "X-UAPI-Estimated-Tokens"
	HeaderPrecharged      = "X-UAPI-Precharged"
	HeaderClientIP        = "X-UAPI-Client-IP"
	HeaderRequestID       = "X-UAPI-Request-ID"
	HeaderChannelID       = "X-UAPI-Channel-ID"
	HeaderAccountID       = "X-UAPI-Account-ID"
)

const MaxClockSkew = 5 * time.Minute

type Claims struct {
	GatewayID       string
	TokenID         string
	TokenPlanID     string
	UserID          string
	Model           string
	EstimatedTokens int
	Precharged      bool
	ClientIP        string
	RequestID       string
	ChannelID       string
	AccountID       string
}

func SignRequest(req *fasthttp.Request, secret string, claims Claims, now time.Time) error {
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("empty internal auth secret")
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set(HeaderGatewayID, claims.GatewayID)
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderTokenID, claims.TokenID)
	req.Header.Set(HeaderTokenPlanID, claims.TokenPlanID)
	req.Header.Set(HeaderUserID, claims.UserID)
	req.Header.Set(HeaderModel, claims.Model)
	req.Header.Set(HeaderEstimatedTokens, strconv.Itoa(claims.EstimatedTokens))
	if claims.Precharged {
		req.Header.Set(HeaderPrecharged, "1")
	} else {
		req.Header.Set(HeaderPrecharged, "0")
	}
	if claims.ClientIP != "" {
		req.Header.Set(HeaderClientIP, claims.ClientIP)
	}
	req.Header.Set(HeaderRequestID, claims.RequestID)
	req.Header.Set(HeaderChannelID, claims.ChannelID)
	req.Header.Set(HeaderAccountID, claims.AccountID)
	sig := signature(string(req.Header.Method()), requestPath(req), timestamp, req.Body(), claims, secret)
	req.Header.Set(HeaderSignature, sig)
	return nil
}

func VerifyRequest(ctx *fasthttp.RequestCtx, secret string, now time.Time) (Claims, bool) {
	var claims Claims
	if strings.TrimSpace(secret) == "" {
		return claims, false
	}
	sig := strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderSignature)))
	timestamp := strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderTimestamp)))
	if sig == "" || timestamp == "" {
		return claims, false
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return claims, false
	}
	t := time.Unix(ts, 0)
	if now.Sub(t) > MaxClockSkew || t.Sub(now) > MaxClockSkew {
		return claims, false
	}
	claims = Claims{
		GatewayID:   strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderGatewayID))),
		TokenID:     strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderTokenID))),
		TokenPlanID: strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderTokenPlanID))),
		UserID:      strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderUserID))),
		Model:       strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderModel))),
		ClientIP:    strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderClientIP))),
		RequestID:   strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderRequestID))),
		ChannelID:   strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderChannelID))),
		AccountID:   strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderAccountID))),
	}
	est, _ := strconv.Atoi(strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderEstimatedTokens))))
	claims.EstimatedTokens = est
	claims.Precharged = strings.TrimSpace(string(ctx.Request.Header.Peek(HeaderPrecharged))) == "1"
	expected := signature(string(ctx.Method()), string(ctx.RequestURI()), timestamp, ctx.PostBody(), claims, secret)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return Claims{}, false
	}
	return claims, claims.TokenID != "" && claims.Model != ""
}

func StripHeaders(req *fasthttp.Request) {
	for _, h := range []string{HeaderGatewayID, HeaderTimestamp, HeaderSignature, HeaderTokenID, HeaderTokenPlanID, HeaderUserID, HeaderModel, HeaderEstimatedTokens, HeaderPrecharged, HeaderClientIP, HeaderRequestID, HeaderChannelID, HeaderAccountID} {
		req.Header.Del(h)
	}
}

func signature(method, path, timestamp string, body []byte, claims Claims, secret string) string {
	bodyHash := sha256.Sum256(body)
	payload := strings.Join([]string{
		method,
		path,
		timestamp,
		hex.EncodeToString(bodyHash[:]),
		claims.GatewayID,
		claims.TokenID,
		claims.TokenPlanID,
		claims.UserID,
		claims.Model,
		strconv.Itoa(claims.EstimatedTokens),
		boolString(claims.Precharged),
		claims.ClientIP,
		claims.RequestID,
		claims.ChannelID,
		claims.AccountID,
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func requestPath(req *fasthttp.Request) string {
	uri := req.URI()
	if uri == nil {
		return string(req.RequestURI())
	}
	return string(uri.RequestURI())
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
