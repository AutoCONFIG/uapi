package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/oauthprovider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// oauthHTTPClient is a shared client for OAuth token operations.
var oauthHTTPClient = &http.Client{Timeout: 15 * time.Second}

// refreshGroup deduplicates concurrent OAuth refresh calls for the same account.
var refreshGroup singleflight.Group

const (
	defaultOAuthRefreshSkew = 2 * time.Minute
	claudeOAuthRefreshSkew  = 5 * time.Minute
	geminiOAuthRefreshSkew  = 5 * time.Minute
	codexRefreshInterval    = 8 * 24 * time.Hour
)

// EnsureValidCredentials checks if account credentials are valid, refreshes OAuth tokens if needed.
// Returns the decrypted credential string ready for API use.
func EnsureValidCredentials(account *db.Account, database *gorm.DB) (string, error) {
	if account.CredType == "api_key" || account.CredType == "" {
		return crypto.Decrypt(account.Credentials)
	}

	if shouldRefreshOAuthCredentials(account) {
		accountID := account.ID.String()
		v, err, _ := refreshGroup.Do(accountID, func() (interface{}, error) {
			return refreshOAuthToken(account, database)
		})
		if err != nil {
			return "", err
		}
		return v.(string), nil
	}

	if oauthProviderKey(account) == "gemini" && isGoogleOAuthTokenURL(account.TokenURL) && shouldSyncGeminiCodeSetup(account.Metadata) {
		credential, err := crypto.Decrypt(account.Credentials)
		if err != nil {
			return "", err
		}
		if metadata, err := gemini.FetchCodeAssistMetadata(credential, geminiProjectID(account.Metadata)); err == nil {
			account.Metadata = metadata
			if database != nil {
				if updateErr := database.Model(&db.Account{}).Where("id = ?", account.ID).Update("metadata", metadata).Error; updateErr != nil {
					logger.Warnf("relay.oauth", "persist gemini code setup metadata failed", logger.F("account_id", account.ID.String()), logger.Err(updateErr))
				}
			}
		} else {
			logger.Warnf("relay.oauth", "sync gemini code setup metadata failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		}
	}
	if err := ensureOAuthAccountReady(account); err != nil {
		return "", err
	}
	return crypto.Decrypt(account.Credentials)
}

// RefreshOAuthCredentials forces an OAuth refresh and metadata sync for scheduler-driven maintenance.
func RefreshOAuthCredentials(account *db.Account, database *gorm.DB) (string, error) {
	if account.CredType != "oauth_token" {
		return "", fmt.Errorf("account %s is not an oauth account", account.ID)
	}
	accountID := account.ID.String()
	v, err, _ := refreshGroup.Do(accountID, func() (interface{}, error) {
		return refreshOAuthToken(account, database)
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func IsIdleRefreshProvider(tokenURL string) bool {
	return isAnthropicOAuthTokenURL(tokenURL) || isGoogleOAuthTokenURL(tokenURL)
}

func shouldRefreshOAuthCredentials(account *db.Account) bool {
	now := time.Now()
	switch {
	case isOpenAIOAuthTokenURL(account.TokenURL):
		if account.TokenExpiry != nil && !account.TokenExpiry.After(now) {
			return true
		}
		if lastRefresh, ok := oauthLastRefresh(account.Metadata); ok {
			return lastRefresh.Before(now.Add(-codexRefreshInterval))
		}
		return account.TokenExpiry == nil
	case isAnthropicOAuthTokenURL(account.TokenURL):
		return account.TokenExpiry != nil && now.Add(claudeOAuthRefreshSkew).After(*account.TokenExpiry)
	case isGoogleOAuthTokenURL(account.TokenURL):
		return account.TokenExpiry != nil && now.Add(geminiOAuthRefreshSkew).After(*account.TokenExpiry)
	default:
		return account.TokenExpiry != nil && now.Add(defaultOAuthRefreshSkew).After(*account.TokenExpiry)
	}
}

func oauthLastRefresh(metadata map[string]interface{}) (time.Time, bool) {
	if metadata == nil {
		return time.Time{}, false
	}
	for _, key := range []string{"oauth_last_refresh_at", "last_synced_at"} {
		if value, ok := metadata[key].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

func refreshOAuthToken(account *db.Account, database *gorm.DB) (string, error) {
	refreshToken, err := crypto.Decrypt(account.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}
	if strings.TrimSpace(refreshToken) == "" {
		return "", fmt.Errorf("oauth account %s has no refresh token", account.ID)
	}

	if oauthProviderKey(account) == "antigravity" {
		return refreshAntigravityOAuthToken(account, database, refreshToken)
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {account.ClientID},
	}
	if account.ClientSecret != "" {
		clientSecret, err := crypto.Decrypt(account.ClientSecret)
		if err != nil {
			return "", fmt.Errorf("decrypt client secret: %w", err)
		}
		data.Set("client_secret", clientSecret)
	}

	var resp *http.Response
	if isAnthropicOAuthTokenURL(account.TokenURL) {
		payload := map[string]interface{}{
			"grant_type":    "refresh_token",
			"refresh_token": refreshToken,
			"client_id":     account.ClientID,
			"scope":         anthropic.ClaudeAIRefreshScope,
		}
		body, _ := json.Marshal(payload)
		req, reqErr := http.NewRequest(http.MethodPost, account.TokenURL, bytes.NewReader(body))
		if reqErr != nil {
			return "", fmt.Errorf("refresh request build failed: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err = oauthHTTPClient.Do(req)
	} else if isOpenAIOAuthTokenURL(account.TokenURL) {
		clientSecret := ""
		if account.ClientSecret != "" {
			var secretErr error
			clientSecret, secretErr = crypto.Decrypt(account.ClientSecret)
			if secretErr != nil {
				return "", fmt.Errorf("decrypt client secret: %w", secretErr)
			}
		}
		req, reqErr := openai.NewRefreshTokenRequest(account.TokenURL, refreshToken, account.ClientID, clientSecret)
		if reqErr != nil {
			return "", fmt.Errorf("refresh request build failed: %w", reqErr)
		}
		resp, err = oauthHTTPClient.Do(req)
	} else {
		resp, err = oauthHTTPClient.PostForm(account.TokenURL, data)
	}
	if err != nil {
		return "", fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read refresh response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("refresh failed: status %d: %s", resp.StatusCode, compactOAuthBody(respBody))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode refresh response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("refresh failed: %s", result.Error)
	}
	if result.AccessToken == "" && result.IDToken == "" {
		return "", fmt.Errorf("refresh response missing access token")
	}

	if result.IDToken != "" && strings.Contains(account.TokenURL, "auth.openai.com") {
		if metadata, err := openai.ParseIDTokenMetadata(result.IDToken); err == nil {
			if account.Metadata == nil {
				account.Metadata = map[string]interface{}{}
			}
			for key, value := range metadata {
				account.Metadata[key] = value
			}
		}
		if usage, err := openai.FetchCodexUsage(result.AccessToken, openAIAccountID(account.Metadata), openAIFedRAMP(account.Metadata)); err == nil {
			if account.Metadata == nil {
				account.Metadata = map[string]interface{}{}
			}
			account.Metadata["codex_usage"] = usage
		}
	}
	if account.Metadata == nil {
		account.Metadata = map[string]interface{}{}
	}
	account.Metadata["oauth_last_refresh_at"] = time.Now().UTC().Format(time.RFC3339)

	newExpiry := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	if result.ExpiresIn <= 0 {
		newExpiry = time.Now().Add(8 * 24 * time.Hour)
	}
	newCreds, encErr := crypto.Encrypt(result.AccessToken)
	if encErr != nil {
		return "", fmt.Errorf("encrypt refreshed credentials: %w", encErr)
	}
	updates := map[string]interface{}{
		"credentials":  newCreds,
		"token_expiry": newExpiry,
	}
	account.Credentials = newCreds
	account.TokenExpiry = &newExpiry
	if result.RefreshToken != "" {
		newRefresh, encErr := crypto.Encrypt(result.RefreshToken)
		if encErr != nil {
			logger.Warnf("relay.oauth", "encrypt refreshed refresh token failed", logger.F("account_id", account.ID.String()), logger.Err(encErr))
		} else {
			updates["refresh_token"] = newRefresh
			account.RefreshToken = newRefresh
		}
	}
	if isAnthropicOAuthTokenURL(account.TokenURL) {
		scopes := strings.Fields(result.Scope)
		if len(scopes) == 0 {
			scopes = strings.Fields(anthropic.ClaudeAIRefreshScope)
		}
		if metadata, err := anthropic.FetchAccountMetadata(result.AccessToken, scopes); err == nil {
			updates["metadata"] = metadata
			account.Metadata = metadata
		} else {
			logger.Warnf("relay.oauth", "sync anthropic oauth metadata failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		}
	} else if isOpenAIOAuthTokenURL(account.TokenURL) && account.Metadata != nil {
		updates["metadata"] = account.Metadata
	} else if oauthProviderKey(account) == "gemini" && isGoogleOAuthTokenURL(account.TokenURL) {
		projectID := geminiProjectID(account.Metadata)
		if metadata, err := gemini.FetchCodeAssistMetadata(result.AccessToken, projectID); err == nil {
			updates["metadata"] = metadata
			account.Metadata = metadata
		} else {
			logger.Warnf("relay.oauth", "sync gemini code metadata failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		}
	}
	if database != nil {
		if err := database.Model(&db.Account{}).Where("id = ?", account.ID).Updates(updates).Error; err != nil {
			logger.Warnf("relay.oauth", "persist refreshed credentials failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		}
	}

	return result.AccessToken, nil
}

func refreshAntigravityOAuthToken(account *db.Account, database *gorm.DB, refreshToken string) (string, error) {
	clientSecret := ""
	if account.ClientSecret != "" {
		decrypted, err := crypto.Decrypt(account.ClientSecret)
		if err != nil {
			return "", fmt.Errorf("decrypt client secret: %w", err)
		}
		clientSecret = decrypted
	}
	result, err := antigravity.RefreshToken(account.TokenURL, refreshToken, account.ClientID, clientSecret)
	if err != nil {
		return "", err
	}
	credential := result.AccessToken
	newExpiry := time.Now().Add(time.Hour)
	if result.ExpiresIn > 0 {
		newExpiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	}
	newCreds, encErr := crypto.Encrypt(credential)
	if encErr != nil {
		return "", fmt.Errorf("encrypt refreshed credentials: %w", encErr)
	}
	if account.Metadata == nil {
		account.Metadata = map[string]interface{}{}
	}
	account.Metadata["oauth_provider"] = "antigravity"
	account.Metadata["oauth_last_refresh_at"] = time.Now().UTC().Format(time.RFC3339)
	updates := map[string]interface{}{
		"credentials":  newCreds,
		"token_expiry": newExpiry,
		"metadata":     account.Metadata,
	}
	account.Credentials = newCreds
	account.TokenExpiry = &newExpiry
	if result.RefreshToken != "" {
		newRefresh, encErr := crypto.Encrypt(result.RefreshToken)
		if encErr != nil {
			logger.Warnf("relay.oauth", "encrypt refreshed antigravity refresh token failed", logger.F("account_id", account.ID.String()), logger.Err(encErr))
		} else {
			updates["refresh_token"] = newRefresh
			account.RefreshToken = newRefresh
		}
	}
	if oauthProv, ok := oauthprovider.Get("antigravity"); ok {
		if metadata, err := oauthProv.SyncMetadata(credential, account.Metadata); err == nil {
			if metadata == nil {
				metadata = map[string]interface{}{}
			}
			metadata["oauth_last_refresh_at"] = account.Metadata["oauth_last_refresh_at"]
			metadata["oauth_provider"] = "antigravity"
			updates["metadata"] = metadata
			account.Metadata = metadata
		} else {
			logger.Warnf("relay.oauth", "sync antigravity oauth metadata failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		}
	}
	if database != nil {
		if err := database.Model(&db.Account{}).Where("id = ?", account.ID).Updates(updates).Error; err != nil {
			logger.Warnf("relay.oauth", "persist refreshed antigravity credentials failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		}
	}
	return credential, nil
}

func oauthProviderKey(account *db.Account) string {
	if account != nil && account.Metadata != nil {
		if value, ok := account.Metadata["oauth_provider"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	if account != nil {
		if isOpenAIOAuthTokenURL(account.TokenURL) {
			return "openai"
		}
		if isAnthropicOAuthTokenURL(account.TokenURL) {
			return "anthropic"
		}
		if isGoogleOAuthTokenURL(account.TokenURL) {
			return "gemini"
		}
	}
	return ""
}

func shouldSyncGeminiCodeSetup(metadata map[string]interface{}) bool {
	if metadata == nil {
		return true
	}
	status, _ := metadata["setup_status"].(string)
	if status == "validation_required" || status == "onboard_pending" || status == "onboard_failed" {
		return true
	}
	if status == "ready" {
		return geminiProjectID(metadata) == ""
	}
	_, hasLoadCodeAssist := metadata["load_code_assist"]
	return !hasLoadCodeAssist
}

func ensureOAuthAccountReady(account *db.Account) error {
	if account == nil || account.CredType != "oauth_token" || oauthProviderKey(account) != "gemini" || !isGoogleOAuthTokenURL(account.TokenURL) || account.Metadata == nil {
		return nil
	}
	if status, _ := account.Metadata["setup_status"].(string); status == "validation_required" {
		if validation, ok := account.Metadata["validation"].(map[string]interface{}); ok {
			if link, _ := validation["validation_url"].(string); link != "" {
				return fmt.Errorf("gemini code account validation required: %s", link)
			}
		}
		return fmt.Errorf("gemini code account validation required")
	}
	return nil
}

func geminiProjectID(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	if project, ok := metadata["project_id"].(string); ok {
		return project
	}
	if loadRes, ok := metadata["load_code_assist"].(map[string]interface{}); ok {
		if project, ok := loadRes["cloudaicompanionProject"].(string); ok {
			return project
		}
		if project, ok := loadRes["cloudaicompanionProject"].(map[string]interface{}); ok {
			if id, ok := project["id"].(string); ok {
				return id
			}
		}
	}
	return ""
}

func openAIAccountID(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	if accountID, ok := metadata["chatgpt_account_id"].(string); ok {
		return accountID
	}
	return ""
}

func openAIFedRAMP(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	if fedramp, ok := metadata["chatgpt_account_is_fedramp"].(bool); ok {
		return fedramp
	}
	return false
}

func isAnthropicOAuthTokenURL(tokenURL string) bool {
	return oauthTokenURLIs(tokenURL, anthropic.DefaultTokenURL)
}

func isOpenAIOAuthTokenURL(tokenURL string) bool {
	return oauthTokenURLIs(tokenURL, openai.DefaultTokenURL)
}

func isGoogleOAuthTokenURL(tokenURL string) bool {
	return oauthTokenURLIs(tokenURL, gemini.DefaultTokenURL)
}

func oauthTokenURLIs(rawURL, expectedURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	expected, err := url.Parse(expectedURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, expected.Scheme) &&
		strings.EqualFold(parsed.Hostname(), expected.Hostname()) &&
		parsed.Port() == "" &&
		parsed.EscapedPath() == expected.EscapedPath() &&
		parsed.RawQuery == "" &&
		parsed.Fragment == ""
}

func compactOAuthBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	text = redactOAuthText(text)
	if text == "" {
		return "empty response"
	}
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

func redactOAuthText(text string) string {
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
