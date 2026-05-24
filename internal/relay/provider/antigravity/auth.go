package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
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

	FallbackVersion       = "1.21.9"
	NodeAPIClientUA       = "google-api-nodejs-client/10.3.0"
	GoogAPIClientUA       = "gl-node/22.21.1"
	OAuthProviderMetadata = "antigravity"
)

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
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("antigravity exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read antigravity exchange response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("antigravity exchange failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse antigravity exchange response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("antigravity exchange failed: %s", tokenResp.Error)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("antigravity exchange response missing access token")
	}
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
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("antigravity refresh request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read antigravity refresh response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("antigravity refresh failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse antigravity refresh response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("antigravity refresh response missing access token")
	}
	return &tokenResp, nil
}

func fetchUserInfo(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo?alt=json", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", RequestUserAgent())
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("userinfo failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}
	return strings.TrimSpace(info.Email), nil
}

func loadCodeAssist(ctx context.Context, accessToken string) (map[string]interface{}, error) {
	body, _ := json.Marshal(map[string]interface{}{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, APIEndpoint+"/"+APIVersion+":loadCodeAssist", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", RequestUserAgent())
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("loadCodeAssist failed: status %d: %s", resp.StatusCode, compactBody(respBody))
	}
	var out map[string]interface{}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func onboardUser(ctx context.Context, accessToken, tierID string) (string, error) {
	if tierID == "" {
		tierID = "free-tier"
	}
	ua := LoadCodeAssistUserAgent()
	body, _ := json.Marshal(map[string]interface{}{
		"tier_id": tierID,
		"metadata": map[string]string{
			"ide_type":    "ANTIGRAVITY",
			"ide_version": FallbackVersion,
			"ide_name":    "antigravity",
		},
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
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", err
		}
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return "", readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("onboardUser failed: status %d: %s", resp.StatusCode, compactBody(respBody))
		}
		var out map[string]interface{}
		if err := json.Unmarshal(respBody, &out); err != nil {
			return "", err
		}
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

func NativeOAuthUserAgent() string { return "vscode/1.X.X (Antigravity/" + FallbackVersion + ")" }

func RequestUserAgent() string { return antigravityBaseUA() }

func LoadCodeAssistUserAgent() string { return antigravityBaseUA() + " " + NodeAPIClientUA }

func antigravityBaseUA() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	if goos == "darwin" {
		goos = "darwin"
	}
	if goarch == "amd64" {
		goarch = "x64"
	}
	return "antigravity/" + FallbackVersion + " " + goos + "/" + goarch
}

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
