package anthropic

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
	"github.com/google/uuid"
)

var oauthHTTPClient = &http.Client{Timeout: 30 * time.Second}

var ClaudeCodeSessionID = uuid.NewString()

const (
	DefaultAuthURL       = "https://claude.com/cai/oauth/authorize"
	DefaultTokenURL      = "https://platform.claude.com/v1/oauth/token"
	DefaultAPIBaseURL    = "https://api.anthropic.com"
	DefaultClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	DefaultRedirectURI   = "https://platform.claude.com/oauth/code/callback"
	DefaultScope         = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	ClaudeAIRefreshScope = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	OAuthBetaHeader      = "oauth-2025-04-20"
	ClaudeCodeVersion    = "2.1.156"
	ClaudeCLIUserAgent   = "claude-cli/" + ClaudeCodeVersion + " (external, cli)"
	ClaudeCodeUserAgent  = "claude-code/" + ClaudeCodeVersion
	RolesURL             = DefaultAPIBaseURL + "/api/oauth/claude_cli/roles"
	ProfileURL           = DefaultAPIBaseURL + "/api/oauth/profile"
	FirstTokenDateURL    = DefaultAPIBaseURL + "/api/organization/claude_code_first_token_date"
	UsageURL             = DefaultAPIBaseURL + "/api/oauth/usage"
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Account      struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Organization struct {
		UUID string `json:"uuid"`
	} `json:"organization"`
}

func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func GenerateCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func BuildAuthURL(clientID, redirectURI, codeChallenge, state string) string {
	params := [][2]string{
		{"code", "true"},
		{"client_id", clientID},
		{"response_type", "code"},
		{"redirect_uri", redirectURI},
		{"scope", DefaultScope},
		{"code_challenge", codeChallenge},
		{"code_challenge_method", "S256"},
		{"state", state},
	}
	return DefaultAuthURL + "?" + encodeQuery(params)
}

func encodeQuery(params [][2]string) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param[0]+"="+url.QueryEscape(param[1]))
	}
	return strings.Join(parts, "&")
}

func ExchangeCode(tokenURL, code, redirectURI, codeVerifier, clientID, state string) (*TokenResponse, error) {
	return ExchangeCodeWithDebugMetadata(tokenURL, code, redirectURI, codeVerifier, clientID, state, nil)
}

func ExchangeCodeWithDebugMetadata(tokenURL, code, redirectURI, codeVerifier, clientID, state string, metadata map[string]interface{}) (*TokenResponse, error) {
	payload := map[string]interface{}{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     clientID,
		"code_verifier": codeVerifier,
		"state":         state,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	debugInfo := oauthdebug.NewHTTPDebug(req, body)
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		oauthdebug.Write("claude_code", "exchange_code", metadata, debugInfo, nil, err)
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("claude_code", "exchange_code", metadata, debugInfo, nil, err)
		return nil, fmt.Errorf("read token response: %w", err)
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, respBody)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("token exchange failed: status %d: %s", resp.StatusCode, compactBody(respBody))
		oauthdebug.Write("claude_code", "exchange_code", metadata, debugInfo, nil, err)
		return nil, err
	}
	var result TokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		oauthdebug.Write("claude_code", "exchange_code", metadata, debugInfo, nil, err)
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if result.Error != "" {
		err := fmt.Errorf("token exchange failed: %s", result.Error)
		oauthdebug.Write("claude_code", "exchange_code", metadata, debugInfo, nil, err)
		return nil, err
	}
	if result.AccessToken == "" {
		err := fmt.Errorf("token exchange response missing access_token")
		oauthdebug.Write("claude_code", "exchange_code", metadata, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("claude_code", "exchange_code", metadata, debugInfo, result, nil)
	return &result, nil
}

type AccountMetadata struct {
	Profile               *OAuthProfile `json:"profile,omitempty"`
	Roles                 *UserRoles    `json:"roles,omitempty"`
	FirstTokenDate        interface{}   `json:"first_token_date,omitempty"`
	Usage                 interface{}   `json:"usage,omitempty"`
	SubscriptionType      string        `json:"subscription_type,omitempty"`
	RateLimitTier         string        `json:"rate_limit_tier,omitempty"`
	BillingType           string        `json:"billing_type,omitempty"`
	AccountUUID           string        `json:"account_uuid,omitempty"`
	EmailAddress          string        `json:"email_address,omitempty"`
	OrganizationUUID      string        `json:"organization_uuid,omitempty"`
	OrganizationName      string        `json:"organization_name,omitempty"`
	DisplayName           string        `json:"display_name,omitempty"`
	AccountCreatedAt      string        `json:"account_created_at,omitempty"`
	SubscriptionCreatedAt string        `json:"subscription_created_at,omitempty"`
	HasExtraUsageEnabled  *bool         `json:"has_extra_usage_enabled,omitempty"`
	Scopes                []string      `json:"scopes,omitempty"`
	LastSyncedAt          string        `json:"last_synced_at,omitempty"`
}

type OAuthProfile struct {
	Account struct {
		UUID        string `json:"uuid"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		CreatedAt   string `json:"created_at"`
	} `json:"account"`
	Organization struct {
		UUID                  string `json:"uuid"`
		Name                  string `json:"name"`
		OrganizationType      string `json:"organization_type"`
		RateLimitTier         string `json:"rate_limit_tier"`
		BillingType           string `json:"billing_type"`
		SubscriptionCreatedAt string `json:"subscription_created_at"`
		HasExtraUsageEnabled  *bool  `json:"has_extra_usage_enabled"`
	} `json:"organization"`
}

type UserRoles struct {
	OrganizationRole string `json:"organization_role"`
	WorkspaceRole    string `json:"workspace_role"`
	OrganizationName string `json:"organization_name"`
}

func FetchAccountMetadata(accessToken string, scopes []string) (map[string]interface{}, error) {
	meta := AccountMetadata{
		Scopes:       scopes,
		LastSyncedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if profile, err := fetchOAuthProfile(accessToken); err == nil && profile != nil {
		meta.Profile = profile
		meta.AccountUUID = profile.Account.UUID
		meta.EmailAddress = profile.Account.Email
		meta.OrganizationUUID = profile.Organization.UUID
		meta.OrganizationName = profile.Organization.Name
		meta.DisplayName = profile.Account.DisplayName
		meta.AccountCreatedAt = profile.Account.CreatedAt
		meta.SubscriptionCreatedAt = profile.Organization.SubscriptionCreatedAt
		meta.RateLimitTier = profile.Organization.RateLimitTier
		meta.BillingType = profile.Organization.BillingType
		meta.HasExtraUsageEnabled = profile.Organization.HasExtraUsageEnabled
		meta.SubscriptionType = subscriptionType(profile.Organization.OrganizationType)
	}
	if roles, err := fetchUserRoles(accessToken); err == nil && roles != nil {
		meta.Roles = roles
		if meta.OrganizationName == "" {
			meta.OrganizationName = roles.OrganizationName
		}
	}
	if firstTokenDate, err := fetchFirstTokenDate(accessToken); err == nil {
		meta.FirstTokenDate = firstTokenDate
	}
	if usage, err := fetchUsage(accessToken); err == nil {
		meta.Usage = usage
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

func fetchUsage(accessToken string) (interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, UsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ClaudeCodeUserAgent)
	debugInfo := oauthdebug.NewHTTPDebug(req, nil)
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		oauthdebug.Write("claude_code", "fetch_usage", nil, debugInfo, nil, err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("claude_code", "fetch_usage", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("usage fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("claude_code", "fetch_usage", nil, debugInfo, nil, err)
		return nil, err
	}
	var usage map[string]interface{}
	if err := json.Unmarshal(body, &usage); err != nil {
		oauthdebug.Write("claude_code", "fetch_usage", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("claude_code", "fetch_usage", nil, debugInfo, usage, nil)
	return usage, nil
}

func fetchOAuthProfile(accessToken string) (*OAuthProfile, error) {
	req, err := http.NewRequest(http.MethodGet, ProfileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	debugInfo := oauthdebug.NewHTTPDebug(req, nil)
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		oauthdebug.Write("claude_code", "fetch_profile", nil, debugInfo, nil, err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("claude_code", "fetch_profile", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("profile fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("claude_code", "fetch_profile", nil, debugInfo, nil, err)
		return nil, err
	}
	var profile OAuthProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		oauthdebug.Write("claude_code", "fetch_profile", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("claude_code", "fetch_profile", nil, debugInfo, profile, nil)
	return &profile, nil
}

func fetchUserRoles(accessToken string) (*UserRoles, error) {
	req, err := http.NewRequest(http.MethodGet, RolesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	debugInfo := oauthdebug.NewHTTPDebug(req, nil)
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		oauthdebug.Write("claude_code", "fetch_roles", nil, debugInfo, nil, err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("claude_code", "fetch_roles", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("roles fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("claude_code", "fetch_roles", nil, debugInfo, nil, err)
		return nil, err
	}
	var roles UserRoles
	if err := json.Unmarshal(body, &roles); err != nil {
		oauthdebug.Write("claude_code", "fetch_roles", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("claude_code", "fetch_roles", nil, debugInfo, roles, nil)
	return &roles, nil
}

func fetchFirstTokenDate(accessToken string) (interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, FirstTokenDateURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", OAuthBetaHeader)
	req.Header.Set("User-Agent", ClaudeCodeUserAgent)
	debugInfo := oauthdebug.NewHTTPDebug(req, nil)
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		oauthdebug.Write("claude_code", "fetch_first_token_date", nil, debugInfo, nil, err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write("claude_code", "fetch_first_token_date", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("first token date fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
		oauthdebug.Write("claude_code", "fetch_first_token_date", nil, debugInfo, nil, err)
		return nil, err
	}
	var result struct {
		FirstTokenDate interface{} `json:"first_token_date"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		oauthdebug.Write("claude_code", "fetch_first_token_date", nil, debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write("claude_code", "fetch_first_token_date", nil, debugInfo, result, nil)
	return result.FirstTokenDate, nil
}

func subscriptionType(orgType string) string {
	switch orgType {
	case "claude_max":
		return "max"
	case "claude_pro":
		return "pro"
	case "claude_enterprise":
		return "enterprise"
	case "claude_team":
		return "team"
	default:
		return ""
	}
}

func compactBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	s = redactOAuthBody(strings.Join(strings.Fields(s), " "))
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
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
