package openai

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
)

// httpClient is shared across OAuth operations with a reasonable timeout.
var httpClient = &http.Client{Timeout: 30 * time.Second}

const (
	DefaultAuthURL     = "https://auth.openai.com/oauth/authorize"
	DefaultTokenURL    = "https://auth.openai.com/oauth/token"
	DefaultClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultScope       = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	DefaultRedirectURI = "http://localhost:1455/auth/callback"
	CodexOriginator    = "codex_cli_rs"

	DeviceUserCodeURL = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	DeviceTokenURL    = "https://auth.openai.com/api/accounts/deviceauth/token"
	DeviceAuthURL     = "https://auth.openai.com/codex/device"
	DeviceRedirectURI = "https://auth.openai.com/deviceauth/callback"
	CodexUsageURL     = "https://chatgpt.com/backend-api/wham/usage"
	CodexAPIBaseURL   = "https://chatgpt.com/backend-api/codex"
	// CodexClientVersion mirrors the codex_cli_rs workspace cargo version that
	// the official client embeds via env!("CARGO_PKG_VERSION") in
	// model-provider-info/src/lib.rs:336 and emits as the `version` HTTP
	// header on every Codex request. The upstream Cargo.toml ships the
	// placeholder "0.0.0" in git and release-please rewrites it at build
	// time; we therefore track the latest published rust-v* tag instead of
	// the literal manifest value so OpenAI's backend does not see a
	// development-only fingerprint. Bump this when upstream cuts a new
	// stable release (see `git tag -l rust-v*` in codex-rs/).
	CodexClientVersion = "0.138.0"
)

var CodexUserAgent = buildCodexUserAgent()

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

type IDTokenMetadata struct {
	Email                   string `json:"email,omitempty"`
	ChatGPTPlanType         string `json:"chatgpt_plan_type,omitempty"`
	ChatGPTUserID           string `json:"chatgpt_user_id,omitempty"`
	ChatGPTAccountID        string `json:"chatgpt_account_id,omitempty"`
	ChatGPTAccountIsFedRAMP bool   `json:"chatgpt_account_is_fedramp,omitempty"`
	RawIDToken              string `json:"raw_id_token,omitempty"`
	LastSyncedAt            string `json:"last_synced_at,omitempty"`
}

type DeviceUserCodeResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     int    `json:"interval,string"`
}

type DeviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

type CodexUsageDebug struct {
	Request  CodexUsageDebugRequest  `json:"request"`
	Response CodexUsageDebugResponse `json:"response"`
}

type CodexUsageDebugRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type CodexUsageDebugResponse struct {
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBytes  int                 `json:"body_bytes,omitempty"`
	Body       string              `json:"body,omitempty"`
}

// GenerateCodeVerifier creates a PKCE code verifier
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateCodeChallenge creates a PKCE S256 code challenge from verifier
func GenerateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func buildCodexUserAgent() string {
	osName := map[string]string{
		"darwin":  "Mac OS",
		"linux":   "Linux",
		"windows": "Windows",
	}[runtime.GOOS]
	if osName == "" {
		osName = runtime.GOOS
	}
	arch := map[string]string{
		"amd64": "x86_64",
		"arm64": "arm64",
		"386":   "x86",
	}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	// Match upstream codex_cli_rs (login/src/auth/default_client.rs build_user_agent):
	// "<originator>/<version> (<os name> <os version>; <arch>) <terminal>".
	// Upstream uses os_info::get() which on Linux reads /etc/os-release or
	// /proc/sys/kernel/osrelease; we follow the same pattern so the emitted
	// UA carries a real kernel/OS version string rather than the literal
	// "unknown" that strongly fingerprints us as a non-official client.
	// Terminal is "unknown" when TERM_PROGRAM is unset, which matches a
	// headless server environment exactly — keep that literal.
	osVersion := detectOSVersion()
	if osVersion == "" {
		osVersion = "unknown"
	}
	return fmt.Sprintf("%s/%s (%s %s; %s) unknown", CodexOriginator, CodexClientVersion, osName, osVersion, arch)
}

// detectOSVersion returns a best-effort OS version token mirroring how
// upstream codex_cli_rs derives the version segment of its User-Agent. We
// only target Linux because UAPI ships as a server-side relay; on any other
// platform the caller substitutes a stable "unknown" placeholder.
func detectOSVersion() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			return v
		}
	}
	return ""
}

// BuildAuthURL constructs the authorization URL with PKCE
func BuildAuthURL(clientID, redirectURI, codeChallenge, state string) string {
	params := [][2]string{
		{"response_type", "code"},
		{"client_id", clientID},
		{"redirect_uri", redirectURI},
		{"scope", DefaultScope},
		{"code_challenge", codeChallenge},
		{"code_challenge_method", "S256"},
		{"id_token_add_organizations", "true"},
		{"codex_cli_simplified_flow", "true"},
		{"state", state},
		{"originator", CodexOriginator},
	}
	return DefaultAuthURL + "?" + encodeCodexQuery(params)
}

func encodeCodexQuery(params [][2]string) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param[0]+"="+strings.ReplaceAll(url.QueryEscape(param[1]), "+", "%20"))
	}
	return strings.Join(parts, "&")
}

// StartDeviceAuth creates an OpenAI device-code authorization session.
func StartDeviceAuth(clientID string) (*DeviceUserCodeResponse, error) {
	requestBody := []byte(fmt.Sprintf(`{"client_id":%q}`, clientID))
	req, err := http.NewRequest(http.MethodPost, DeviceUserCodeURL, strings.NewReader(string(requestBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	debugInfo := oauthdebug.NewHTTPDebug(req, requestBody)
	resp, err := httpClient.Do(req)
	if err != nil {
		oauthdebug.Write("codex", "device_user_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("device auth request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("codex", "device_user_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("read response: %w", err)
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, respBody)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("device auth failed: status %d: %s", resp.StatusCode, compactBody(respBody))
		oauthdebug.Write("codex", "device_user_code", nil, debugInfo, nil, err)
		return nil, err
	}
	var result DeviceUserCodeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		oauthdebug.Write("codex", "device_user_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.DeviceAuthID == "" || result.UserCode == "" {
		err := fmt.Errorf("device auth response missing code")
		oauthdebug.Write("codex", "device_user_code", nil, debugInfo, nil, err)
		return nil, err
	}
	if result.Interval <= 0 {
		result.Interval = 5
	}
	oauthdebug.Write("codex", "device_user_code", nil, debugInfo, result, nil)
	return &result, nil
}

// PollDeviceToken checks whether the user has completed OpenAI device authorization.
func PollDeviceToken(deviceAuthID, userCode string) (*DeviceTokenResponse, bool, error) {
	requestBody := []byte(fmt.Sprintf(`{"device_auth_id":%q,"user_code":%q}`, deviceAuthID, userCode))
	req, err := http.NewRequest(http.MethodPost, DeviceTokenURL, strings.NewReader(string(requestBody)))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	debugInfo := oauthdebug.NewHTTPDebug(req, requestBody)
	resp, err := httpClient.Do(req)
	if err != nil {
		oauthdebug.Write("codex", "device_token", nil, debugInfo, nil, err)
		return nil, false, fmt.Errorf("device token request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("codex", "device_token", nil, debugInfo, nil, err)
		return nil, false, fmt.Errorf("read response: %w", err)
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, respBody)
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		oauthdebug.Write("codex", "device_token", nil, debugInfo, map[string]interface{}{"complete": false}, nil)
		return nil, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("device token failed: status %d: %s", resp.StatusCode, compactBody(respBody))
		oauthdebug.Write("codex", "device_token", nil, debugInfo, nil, err)
		return nil, false, err
	}
	var result DeviceTokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		oauthdebug.Write("codex", "device_token", nil, debugInfo, nil, err)
		return nil, false, fmt.Errorf("parse response: %w", err)
	}
	if result.AuthorizationCode == "" || result.CodeVerifier == "" {
		err := fmt.Errorf("device token response missing authorization code")
		oauthdebug.Write("codex", "device_token", nil, debugInfo, nil, err)
		return nil, false, err
	}
	oauthdebug.Write("codex", "device_token", nil, debugInfo, result, nil)
	return &result, true, nil
}

// ExchangeCode exchanges authorization code for tokens
func ExchangeCode(tokenURL, code, redirectURI, codeVerifier, clientID string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
		"client_id":     {clientID},
	}
	requestBody := []byte(data.Encode())
	req, reqErr := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(string(requestBody)))
	if reqErr != nil {
		return nil, reqErr
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	debugInfo := oauthdebug.NewHTTPDebug(req, requestBody)
	resp, err := httpClient.Do(req)
	if err != nil {
		oauthdebug.Write("codex", "exchange_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("codex", "exchange_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("read response: %w", err)
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("exchange failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("codex", "exchange_code", nil, debugInfo, nil, err)
		return nil, err
	}
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		oauthdebug.Write("codex", "exchange_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if tokenResp.Error != "" {
		err := fmt.Errorf("exchange failed: %s", tokenResp.Error)
		oauthdebug.Write("codex", "exchange_code", nil, debugInfo, nil, err)
		return nil, err
	}
	if tokenResp.AccessToken == "" {
		err := fmt.Errorf("no access token in response: %s", compactBody(body))
		oauthdebug.Write("codex", "exchange_code", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("codex", "exchange_code", nil, debugInfo, tokenResp, nil)
	return &tokenResp, nil
}

func NewRefreshTokenRequest(tokenURL, refreshToken, clientID, clientSecret string) (*http.Request, error) {
	// Match upstream codex_cli_rs (login/src/auth/manager.rs:900-915): send refresh
	// request as JSON with Content-Type: application/json. The OpenAI token server
	// accepts form-urlencoded too, but the official client uses JSON, so mirror it.
	payload := map[string]string{
		"client_id":     clientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	if clientSecret != "" {
		payload["client_secret"] = clientSecret
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("originator", CodexOriginator)
	req.Header.Set("User-Agent", CodexUserAgent)
	return req, nil
}

func FetchCodexUsage(accessToken, accountID string, fedramp bool) (map[string]interface{}, error) {
	usage, _, err := FetchCodexUsageWithDebug(accessToken, accountID, fedramp)
	return usage, err
}

func FetchCodexUsageWithDebug(accessToken, accountID string, fedramp bool) (map[string]interface{}, *CodexUsageDebug, error) {
	req, err := http.NewRequest(http.MethodGet, CodexUsageURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("originator", CodexOriginator)
	req.Header.Set("User-Agent", CodexUserAgent)
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	if fedramp {
		req.Header.Set("X-OpenAI-Fedramp", "true")
	}
	debugInfo := &CodexUsageDebug{
		Request: CodexUsageDebugRequest{
			Method:  req.Method,
			URL:     req.URL.String(),
			Headers: redactedRequestHeaders(req.Header),
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, debugInfo, fmt.Errorf("codex usage request failed: %w", err)
	}
	defer resp.Body.Close()
	debugInfo.Response.StatusCode = resp.StatusCode
	debugInfo.Response.Headers = redactedResponseHeaders(resp.Header)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, debugInfo, fmt.Errorf("read codex usage response: %w", err)
	}
	debugInfo.Response.BodyBytes = len(body)
	debugInfo.Response.Body = compactBodyWithLimit(body, 10000)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, debugInfo, fmt.Errorf("codex usage failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var usage map[string]interface{}
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, debugInfo, fmt.Errorf("parse codex usage response: %w", err)
	}
	// Debug: log codex usage response
	logger.Debugf("relay.codex_usage", "codex usage response",
		logger.F("status", resp.StatusCode),
		logger.F("body_length", len(body)),
		logger.F("body_preview", string(body[:min(500, len(body))])),
	)
	return usage, debugInfo, nil
}

func compactBody(body []byte) string {
	return compactBodyWithLimit(body, 300)
}

func compactBodyWithLimit(body []byte, limit int) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	text = redactOAuthBody(text)
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	if text == "" {
		return "empty response"
	}
	return text
}

func redactedRequestHeaders(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if strings.EqualFold(key, "Authorization") {
			out[key] = "[redacted]"
			continue
		}
		out[key] = logger.Redact(strings.Join(values, ", "))
	}
	return out
}

func redactedResponseHeaders(headers http.Header) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		copied := make([]string, len(values))
		for i, value := range values {
			copied[i] = logger.Redact(value)
		}
		out[key] = copied
	}
	return out
}

func redactOAuthBody(text string) string {
	for _, key := range []string{"access_token", "refresh_token", "id_token", "client_secret", "authorization", "api_key"} {
		for {
			lower := strings.ToLower(text)
			idx := strings.Index(lower, strings.ToLower(key))
			if idx < 0 {
				break
			}
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
				break
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
				break
			}
			if strings.HasPrefix(text[start:], "[redacted]") {
				break
			}
			text = text[:start] + "[redacted]" + text[end:]
		}
	}
	return text
}

func postForm(tokenURL string, data url.Values) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return httpClient.Do(req)
}

func ParseIDTokenMetadata(idToken string) (map[string]interface{}, error) {
	claims, err := decodeJWTPayload(idToken)
	if err != nil {
		return nil, err
	}
	meta := IDTokenMetadata{
		RawIDToken:   idToken,
		LastSyncedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if email, ok := claims["email"].(string); ok {
		meta.Email = email
	}
	if profile, ok := claims["https://api.openai.com/profile"].(map[string]interface{}); ok && meta.Email == "" {
		if email, ok := profile["email"].(string); ok {
			meta.Email = email
		}
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]interface{}); ok {
		if plan, ok := auth["chatgpt_plan_type"].(string); ok {
			meta.ChatGPTPlanType = plan
		}
		if userID, ok := auth["chatgpt_user_id"].(string); ok {
			meta.ChatGPTUserID = userID
		} else if userID, ok := auth["user_id"].(string); ok {
			meta.ChatGPTUserID = userID
		}
		if accountID, ok := auth["chatgpt_account_id"].(string); ok {
			meta.ChatGPTAccountID = accountID
		}
		if fedramp, ok := auth["chatgpt_account_is_fedramp"].(bool); ok {
			meta.ChatGPTAccountIsFedRAMP = fedramp
		}
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeJWTPayload(jwt string) (map[string]interface{}, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil, fmt.Errorf("invalid id token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}
