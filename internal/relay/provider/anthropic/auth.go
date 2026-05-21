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
)

var oauthHTTPClient = &http.Client{Timeout: 30 * time.Second}

const (
	DefaultAuthURL       = "https://claude.com/cai/oauth/authorize"
	DefaultTokenURL      = "https://platform.claude.com/v1/oauth/token"
	DefaultAPIBaseURL    = "https://api.anthropic.com"
	DefaultClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	DefaultRedirectURI   = "https://platform.claude.com/oauth/code/callback"
	DefaultScope         = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	ClaudeAIRefreshScope = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	OAuthBetaHeader      = "oauth-2025-04-20"
	ClaudeCodeUserAgent  = "claude-cli/uapi (external, cli)"
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
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {DefaultScope},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return DefaultAuthURL + "?" + params.Encode()
}

func ExchangeCode(tokenURL, code, redirectURI, codeVerifier, clientID, state string) (*TokenResponse, error) {
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
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token exchange failed: status %d: %s", resp.StatusCode, compactBody(respBody))
	}
	var result TokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("token exchange failed: %s", result.Error)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("token exchange response missing access_token")
	}
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
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("usage fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var usage map[string]interface{}
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, err
	}
	return usage, nil
}

func fetchOAuthProfile(accessToken string) (*OAuthProfile, error) {
	req, err := http.NewRequest(http.MethodGet, ProfileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("profile fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var profile OAuthProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func fetchUserRoles(accessToken string) (*UserRoles, error) {
	req, err := http.NewRequest(http.MethodGet, RolesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("roles fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var roles UserRoles
	if err := json.Unmarshal(body, &roles); err != nil {
		return nil, err
	}
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
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("first token date fetch failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var result struct {
		FirstTokenDate interface{} `json:"first_token_date"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
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
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
