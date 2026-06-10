package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

const (
	DefaultAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	DefaultTokenURL     = "https://oauth2.googleapis.com/token"
	DefaultClientID     = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	DefaultClientSecret = "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"
	DefaultRedirectURI  = "http://localhost:51121/oauth-callback"
	DefaultScope        = "openid https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs"

	APIEndpoint      = "https://cloudcode-pa.googleapis.com"
	DailyAPIEndpoint = "https://daily-cloudcode-pa.googleapis.com"
	APIVersion       = "v1internal"

	AntigravityReleasesURL = "https://antigravity-auto-updater-974169037036.us-central1.run.app/releases"
	FallbackVersion        = "2.0.1"
	VersionCacheTTL        = 6 * time.Hour
	VersionFetchTimeout    = 10 * time.Second
	NodeAPIClientUA        = "google-api-nodejs-client/10.3.0"
	GoogAPIClientUA        = "gl-node/22.21.1"
	OAuthProviderMetadata  = "antigravity"
)

type antigravityRelease struct {
	Version     string `json:"version"`
	ExecutionID string `json:"execution_id"`
}

var (
	cachedAntigravityVersion = FallbackVersion
	antigravityVersionMu     sync.RWMutex
	antigravityVersionExpiry time.Time
	antigravityUpdaterOnce   sync.Once
	fetchAntigravityVersion  = fetchAntigravityLatestVersion
)

func StartVersionUpdater(ctx context.Context) {
	antigravityUpdaterOnce.Do(func() {
		go runVersionUpdater(ctx)
	})
}

func runVersionUpdater(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	refreshAntigravityVersion(ctx)
	ticker := time.NewTicker(VersionCacheTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshAntigravityVersion(ctx)
		}
	}
}

func refreshAntigravityVersion(ctx context.Context) {
	version, err := fetchAntigravityVersion(ctx)
	antigravityVersionMu.Lock()
	defer antigravityVersionMu.Unlock()
	now := time.Now()
	if err == nil {
		cachedAntigravityVersion = version
		antigravityVersionExpiry = now.Add(VersionCacheTTL)
		return
	}
	if cachedAntigravityVersion == "" || now.After(antigravityVersionExpiry) {
		cachedAntigravityVersion = FallbackVersion
		antigravityVersionExpiry = now.Add(VersionCacheTTL)
	}
}

func LatestVersion() string {
	antigravityVersionMu.RLock()
	if cachedAntigravityVersion != "" && time.Now().Before(antigravityVersionExpiry) {
		version := cachedAntigravityVersion
		antigravityVersionMu.RUnlock()
		return version
	}
	antigravityVersionMu.RUnlock()
	return FallbackVersion
}

func fetchAntigravityLatestVersion(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, VersionFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, AntigravityReleasesURL, nil)
	if err != nil {
		return "", fmt.Errorf("build antigravity releases request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch antigravity releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("antigravity releases API returned status %d", resp.StatusCode)
	}
	var releases []antigravityRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decode antigravity releases response: %w", err)
	}
	if len(releases) == 0 {
		return "", errors.New("antigravity releases API returned empty list")
	}
	version := strings.TrimSpace(releases[0].Version)
	if version == "" {
		return "", errors.New("antigravity releases API returned empty version")
	}
	return version, nil
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func BuildAuthURL(clientID, redirectURI, state string) string {
	if strings.TrimSpace(clientID) == "" {
		clientID = DefaultClientID
	}
	if strings.TrimSpace(redirectURI) == "" {
		redirectURI = DefaultRedirectURI
	}
	params := [][2]string{
		{"response_type", "code"},
		{"client_id", clientID},
		{"redirect_uri", redirectURI},
		{"access_type", "offline"},
		{"prompt", "consent"},
		{"include_granted_scopes", "true"},
		{"scope", DefaultScope},
		{"state", state},
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param[0]+"="+strings.ReplaceAll(url.QueryEscape(param[1]), "+", "%20"))
	}
	return DefaultAuthURL + "?" + strings.Join(parts, "&")
}

func ExchangeCode(tokenURL, code, redirectURI, clientID, clientSecret string) (*TokenResponse, error) {
	if strings.TrimSpace(tokenURL) == "" {
		tokenURL = DefaultTokenURL
	}
	if strings.TrimSpace(clientID) == "" {
		clientID = DefaultClientID
	}
	if strings.TrimSpace(clientSecret) == "" {
		clientSecret = DefaultClientSecret
	}
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("antigravity exchange request build failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", NativeOAuthUserAgent())
	debugInfo := oauthdebug.NewHTTPDebug(req, []byte(data.Encode()))
	resp, err := httpClient.Do(req)
	if err != nil {
		oauthdebug.Write("antigravity", "exchange_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("antigravity exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("antigravity", "exchange_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("read antigravity exchange response: %w", err)
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("antigravity exchange failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("antigravity", "exchange_code", nil, debugInfo, nil, err)
		return nil, err
	}
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		oauthdebug.Write("antigravity", "exchange_code", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("parse antigravity exchange response: %w", err)
	}
	if tokenResp.Error != "" {
		err := fmt.Errorf("antigravity exchange failed: %s", tokenResp.Error)
		oauthdebug.Write("antigravity", "exchange_code", nil, debugInfo, nil, err)
		return nil, err
	}
	if tokenResp.AccessToken == "" {
		err := fmt.Errorf("antigravity exchange response missing access token")
		oauthdebug.Write("antigravity", "exchange_code", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("antigravity", "exchange_code", nil, debugInfo, tokenResp, nil)
	return &tokenResp, nil
}

func FetchAccountMetadata(accessToken string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	metadata := map[string]interface{}{
		"oauth_provider": OAuthProviderMetadata,
		"setup_status":   "ready",
	}
	if email, err := fetchUserInfo(ctx, accessToken); err == nil && email != "" {
		metadata["email"] = email
	}
	loadRes, err := loadCodeAssist(ctx, accessToken)
	if err != nil {
		metadata["setup_status"] = "metadata_failed"
		metadata["setup_error"] = err.Error()
		return metadata, nil
	}
	metadata["load_code_assist"] = loadRes
	if project := extractProject(loadRes); project != "" {
		metadata["project_id"] = project
		return metadata, nil
	}
	project, err := onboardUser(ctx, accessToken, defaultTierID(loadRes))
	if err != nil {
		metadata["setup_status"] = "onboard_failed"
		metadata["setup_error"] = err.Error()
		return metadata, nil
	}
	metadata["project_id"] = project
	return metadata, nil
}

func RefreshToken(tokenURL, refreshToken, clientID, clientSecret string) (*TokenResponse, error) {
	if strings.TrimSpace(tokenURL) == "" {
		tokenURL = DefaultTokenURL
	}
	if strings.TrimSpace(clientID) == "" {
		clientID = DefaultClientID
	}
	if strings.TrimSpace(clientSecret) == "" {
		clientSecret = DefaultClientSecret
	}
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("antigravity refresh request build failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	debugInfo := oauthdebug.NewHTTPDebug(req, []byte(data.Encode()))
	resp, err := httpClient.Do(req)
	if err != nil {
		oauthdebug.Write("antigravity", "refresh_token", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("antigravity refresh request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("antigravity", "refresh_token", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("read antigravity refresh response: %w", err)
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("antigravity refresh failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("antigravity", "refresh_token", nil, debugInfo, nil, err)
		return nil, err
	}
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		oauthdebug.Write("antigravity", "refresh_token", nil, debugInfo, nil, err)
		return nil, fmt.Errorf("parse antigravity refresh response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		err := fmt.Errorf("antigravity refresh response missing access token")
		oauthdebug.Write("antigravity", "refresh_token", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("antigravity", "refresh_token", nil, debugInfo, tokenResp, nil)
	return &tokenResp, nil
}

func fetchUserInfo(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo?alt=json", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", RequestUserAgent())
	debugInfo := oauthdebug.NewHTTPDebug(req, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		oauthdebug.Write("antigravity", "fetch_userinfo", nil, debugInfo, nil, err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("antigravity", "fetch_userinfo", nil, debugInfo, nil, err)
		return "", err
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("userinfo failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("antigravity", "fetch_userinfo", nil, debugInfo, nil, err)
		return "", err
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		oauthdebug.Write("antigravity", "fetch_userinfo", nil, debugInfo, nil, err)
		return "", err
	}
	oauthdebug.Write("antigravity", "fetch_userinfo", nil, debugInfo, info, nil)
	return strings.TrimSpace(info.Email), nil
}

func loadCodeAssist(ctx context.Context, accessToken string) (map[string]interface{}, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"metadata": cloudCodeMetadata(""),
		"mode":     "FULL_ELIGIBILITY_CHECK",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, DailyAPIEndpoint+"/"+APIVersion+":loadCodeAssist", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", LoadCodeAssistUserAgent())
	req.Header.Set("X-Goog-Api-Client", GoogAPIClientUA)
	debugInfo := oauthdebug.NewHTTPDebug(req, body)
	resp, err := httpClient.Do(req)
	if err != nil {
		oauthdebug.Write("antigravity", "load_code_assist", nil, debugInfo, nil, err)
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("antigravity", "load_code_assist", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, respBody)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("loadCodeAssist failed: status %d: %s", resp.StatusCode, compactBody(respBody))
		oauthdebug.Write("antigravity", "load_code_assist", nil, debugInfo, nil, err)
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(respBody, &out); err != nil {
		oauthdebug.Write("antigravity", "load_code_assist", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("antigravity", "load_code_assist", nil, debugInfo, out, nil)
	return out, nil
}

func onboardUser(ctx context.Context, accessToken, tierID string) (string, error) {
	if tierID == "" {
		tierID = "free-tier"
	}
	ua := LoadCodeAssistUserAgent()
	body, _ := json.Marshal(map[string]interface{}{
		"tierId":   tierID,
		"metadata": cloudCodeMetadata(""),
	})
	for attempt := 0; attempt < 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, DailyAPIEndpoint+"/"+APIVersion+":onboardUser", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", ua)
		req.Header.Set("X-Goog-Api-Client", GoogAPIClientUA)
		debugInfo := oauthdebug.NewHTTPDebug(req, body)
		resp, err := httpClient.Do(req)
		if err != nil {
			oauthdebug.Write("antigravity", "onboard_user", map[string]interface{}{"attempt": attempt + 1}, debugInfo, nil, err)
			return "", err
		}
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
			oauthdebug.Write("antigravity", "onboard_user", map[string]interface{}{"attempt": attempt + 1}, debugInfo, nil, readErr)
			return "", readErr
		}
		oauthdebug.FinishHTTPDebug(debugInfo, resp, respBody)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := fmt.Errorf("onboardUser failed: status %d: %s", resp.StatusCode, compactBody(respBody))
			oauthdebug.Write("antigravity", "onboard_user", map[string]interface{}{"attempt": attempt + 1}, debugInfo, nil, err)
			return "", err
		}
		var out map[string]interface{}
		if err := json.Unmarshal(respBody, &out); err != nil {
			oauthdebug.Write("antigravity", "onboard_user", map[string]interface{}{"attempt": attempt + 1}, debugInfo, nil, err)
			return "", err
		}
		oauthdebug.Write("antigravity", "onboard_user", map[string]interface{}{"attempt": attempt + 1}, debugInfo, out, nil)
		if done, _ := out["done"].(bool); done {
			if response, ok := out["response"].(map[string]interface{}); ok {
				if project := extractProject(response); project != "" {
					return project, nil
				}
			}
			return "", fmt.Errorf("onboardUser response missing project_id")
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("onboardUser did not complete")
}

func extractProject(data map[string]interface{}) string {
	for _, key := range []string{"cloudaicompanionProject", "projectId", "project"} {
		switch value := data[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		case map[string]interface{}:
			if id, _ := value["id"].(string); strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	return ""
}

func cloudCodeMetadata(projectID string) map[string]string {
	metadata := map[string]string{
		"ideName":       "antigravity",
		"ideType":       "ANTIGRAVITY",
		"ideVersion":    LatestVersion(),
		"pluginVersion": "unknown",
		"platform":      cloudCodePlatform(),
		"updateChannel": "stable",
		"pluginType":    "GEMINI",
	}
	if strings.TrimSpace(projectID) != "" {
		metadata["duetProject"] = strings.TrimSpace(projectID)
	}
	return metadata
}

func cloudCodePlatform() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/amd64":
		return "DARWIN_AMD64"
	case "darwin/arm64":
		return "DARWIN_ARM64"
	case "linux/amd64":
		return "LINUX_AMD64"
	case "linux/arm64":
		return "LINUX_ARM64"
	case "windows/amd64":
		return "WINDOWS_AMD64"
	default:
		return "PLATFORM_UNSPECIFIED"
	}
}

func defaultTierID(loadRes map[string]interface{}) string {
	if tiers, ok := loadRes["allowedTiers"].([]interface{}); ok {
		for _, raw := range tiers {
			row, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if isDefault, _ := row["isDefault"].(bool); !isDefault {
				continue
			}
			if id, _ := row["id"].(string); strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	if tier, ok := loadRes["currentTier"].(map[string]interface{}); ok {
		if id, _ := tier["id"].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return "free-tier"
}

func NativeOAuthUserAgent() string { return "vscode/1.X.X (Antigravity/" + LatestVersion() + ")" }

func AntigravityUserAgent() string { return "antigravity/" + LatestVersion() + " darwin/arm64" }

func RequestUserAgent() string { return AntigravityUserAgent() }

func LoadCodeAssistUserAgent() string { return AntigravityUserAgent() + " " + NodeAPIClientUA }

func compactBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	for _, key := range []string{"access_token", "refresh_token", "id_token", "client_secret", "authorization", "api_key"} {
		lower := strings.ToLower(text)
		if idx := strings.Index(lower, key); idx >= 0 {
			end := idx + len(key)
			if end < len(text) {
				text = text[:end] + "=[redacted]"
			}
		}
	}
	if text == "" {
		return "empty response"
	}
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}
