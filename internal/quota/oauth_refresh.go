package quota

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
	"github.com/AutoCONFIG/uapi/internal/oauthdebug"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

var oauthRefreshClient = &http.Client{Timeout: 30 * time.Second}

type oauthRefreshResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (s *Scheduler) refreshOAuthAccessToken(acc *db.Account, ch db.Channel) (string, error) {
	if acc.RefreshToken == "" {
		return "", fmt.Errorf("oauth account %s has no refresh token", acc.ID)
	}
	refreshToken, err := crypto.Decrypt(acc.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("decrypt refresh token: %w", err)
	}
	if strings.TrimSpace(refreshToken) == "" {
		return "", fmt.Errorf("oauth account %s has empty refresh token", acc.ID)
	}

	result, err := refreshTokenRequest(acc, ch, refreshToken)
	if err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("refresh response missing access token")
	}

	quotaToken := result.AccessToken
	if ch.APIFormat == "codex" && result.IDToken != "" {
		if metadata, err := openai.ParseIDTokenMetadata(result.IDToken); err == nil {
			mergeMetadata(acc, metadata)
		}
	}

	updates := map[string]interface{}{}
	if encrypted, err := crypto.Encrypt(result.AccessToken); err == nil {
		acc.Credentials = encrypted
		updates["credentials"] = encrypted
	} else {
		logger.Warnf("quota.token", "failed to encrypt refreshed credential", logger.F("account_id", acc.ID.String()), logger.Err(err))
	}
	if result.RefreshToken != "" {
		if encrypted, err := crypto.Encrypt(result.RefreshToken); err == nil {
			acc.RefreshToken = encrypted
			updates["refresh_token"] = encrypted
		} else {
			logger.Warnf("quota.token", "failed to encrypt refreshed refresh token", logger.F("account_id", acc.ID.String()), logger.Err(err))
		}
	}
	expiry := time.Now().Add(8 * 24 * time.Hour)
	if result.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	}
	acc.TokenExpiry = &expiry
	updates["token_expiry"] = expiry

	if acc.Metadata == nil {
		acc.Metadata = map[string]interface{}{}
	}
	acc.Metadata["oauth_last_refresh_at"] = time.Now().UTC().Format(time.RFC3339)
	s.syncOAuthMetadata(acc, ch, quotaToken, result.Scope)
	if ch.APIFormat == "codex" {
		normalizeCodexMetadata(acc.Metadata)
	}
	updates["metadata"] = acc.Metadata

	if err := s.db.Model(&db.Account{}).Where("id = ?", acc.ID).Updates(updates).Error; err != nil {
		return quotaToken, fmt.Errorf("persist refreshed oauth token: %w", err)
	}
	return quotaToken, nil
}

func refreshTokenRequest(acc *db.Account, ch db.Channel, refreshToken string) (*oauthRefreshResult, error) {
	tokenURL := strings.TrimSpace(acc.TokenURL)
	if tokenURL == "" {
		switch ch.APIFormat {
		case "codex":
			tokenURL = openai.DefaultTokenURL
		case "claude_code":
			tokenURL = anthropic.DefaultTokenURL
		case "antigravity":
			tokenURL = antigravity.DefaultTokenURL
		default:
			tokenURL = gemini.DefaultTokenURL
		}
	}
	clientID := oauthClientIDForFormat(acc.ClientID, ch.APIFormat)
	clientSecret := oauthClientSecretForFormat(acc, ch.APIFormat)

	if ch.APIFormat == "antigravity" {
		tokens, err := antigravity.RefreshToken(tokenURL, refreshToken, clientID, clientSecret)
		if err != nil {
			return nil, err
		}
		return &oauthRefreshResult{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, ExpiresIn: tokens.ExpiresIn}, nil
	}

	var resp *http.Response
	var err error
	var req *http.Request
	var requestBody []byte
	if ch.APIFormat == "claude_code" {
		payload := map[string]interface{}{
			"grant_type":    "refresh_token",
			"refresh_token": refreshToken,
			"client_id":     clientID,
			"scope":         anthropic.ClaudeAIRefreshScope,
		}
		requestBody, _ = json.Marshal(payload)
		var reqErr error
		req, reqErr = http.NewRequest(http.MethodPost, tokenURL, bytes.NewReader(requestBody))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err = oauthRefreshClient.Do(req)
	} else if ch.APIFormat == "codex" {
		refreshReq, reqErr := openai.NewRefreshTokenRequest(tokenURL, refreshToken, clientID, clientSecret)
		if reqErr != nil {
			return nil, reqErr
		}
		req = refreshReq
		if req.GetBody != nil {
			if bodyReader, bodyErr := req.GetBody(); bodyErr == nil {
				requestBody, _ = io.ReadAll(bodyReader)
				_ = bodyReader.Close()
			}
		}
		if len(requestBody) == 0 {
			payload := map[string]string{"client_id": clientID, "grant_type": "refresh_token", "refresh_token": refreshToken}
			if clientSecret != "" {
				payload["client_secret"] = clientSecret
			}
			requestBody, _ = json.Marshal(payload)
		}
		resp, err = oauthRefreshClient.Do(req)
	} else {
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {refreshToken},
			"client_id":     {clientID},
		}
		if clientSecret != "" {
			form.Set("client_secret", clientSecret)
		}
		requestBody = []byte(form.Encode())
		var reqErr error
		req, reqErr = http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(string(requestBody)))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err = oauthRefreshClient.Do(req)
	}
	debugInfo := oauthdebug.NewHTTPDebug(req, requestBody)
	if err != nil {
		oauthdebug.Write(ch.APIFormat, "refresh_token", oauthRefreshDebugMetadata(acc, ch), debugInfo, nil, err)
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		oauthdebug.FinishHTTPDebug(debugInfo, resp, nil)
		oauthdebug.Write(ch.APIFormat, "refresh_token", oauthRefreshDebugMetadata(acc, ch), debugInfo, nil, err)
		return nil, fmt.Errorf("read refresh response: %w", err)
	}
	oauthdebug.FinishHTTPDebug(debugInfo, resp, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("refresh failed: status %d: %s", resp.StatusCode, compactOAuthBody(body))
		oauthdebug.Write(ch.APIFormat, "refresh_token", oauthRefreshDebugMetadata(acc, ch), debugInfo, nil, err)
		return nil, err
	}
	var result oauthRefreshResult
	if err := json.Unmarshal(body, &result); err != nil {
		oauthdebug.Write(ch.APIFormat, "refresh_token", oauthRefreshDebugMetadata(acc, ch), debugInfo, nil, err)
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}
	if result.Error != "" {
		err := fmt.Errorf("refresh failed: %s", result.Error)
		oauthdebug.Write(ch.APIFormat, "refresh_token", oauthRefreshDebugMetadata(acc, ch), debugInfo, nil, err)
		return nil, err
	}
	oauthdebug.Write(ch.APIFormat, "refresh_token", oauthRefreshDebugMetadata(acc, ch), debugInfo, result, nil)
	return &result, nil
}

func (s *Scheduler) syncOAuthMetadata(acc *db.Account, ch db.Channel, accessToken, scope string) {
	switch ch.APIFormat {
	case "gemini_code":
		if metadata, err := gemini.FetchCodeAssistMetadata(accessToken, geminiProjectID(acc.Metadata)); err == nil {
			mergeMetadata(acc, metadata)
		}
	case "claude_code":
		scopes := strings.Fields(scope)
		if len(scopes) == 0 {
			scopes = strings.Fields(anthropic.ClaudeAIRefreshScope)
		}
		if metadata, err := anthropic.FetchAccountMetadata(accessToken, scopes); err == nil {
			mergeMetadata(acc, metadata)
		}
	case "antigravity":
		if metadata, err := antigravity.FetchAccountMetadata(accessToken); err == nil {
			mergeMetadata(acc, metadata)
			acc.Metadata["oauth_provider"] = "antigravity"
		}
	}
}

func oauthRefreshDebugMetadata(acc *db.Account, ch db.Channel) map[string]interface{} {
	metadata := map[string]interface{}{
		"channel_id":  ch.ID.String(),
		"channel":     ch.Name,
		"api_format":  ch.APIFormat,
		"account_id":  acc.ID.String(),
		"account":     acc.Name,
		"token_url":   acc.TokenURL,
		"client_id":   acc.ClientID,
		"cred_type":   acc.CredType,
		"has_refresh": acc.RefreshToken != "",
	}
	if acc.TokenExpiry != nil {
		metadata["token_expiry"] = acc.TokenExpiry.Format(time.RFC3339)
	}
	return metadata
}

func decryptedClientSecret(acc *db.Account) string {
	if acc.ClientSecret == "" {
		return ""
	}
	secret, err := crypto.Decrypt(acc.ClientSecret)
	if err != nil {
		return ""
	}
	return secret
}

func oauthClientIDForFormat(clientID, apiFormat string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID != "" {
		return clientID
	}
	switch apiFormat {
	case "codex":
		return openai.DefaultClientID
	case "claude_code":
		return anthropic.DefaultClientID
	case "gemini_code":
		return gemini.DefaultClientID
	case "antigravity":
		return antigravity.DefaultClientID
	default:
		return ""
	}
}

func oauthClientSecretForFormat(acc *db.Account, apiFormat string) string {
	if secret := decryptedClientSecret(acc); secret != "" {
		return secret
	}
	switch apiFormat {
	case "gemini_code":
		return gemini.DefaultClientSecret
	case "antigravity":
		return antigravity.DefaultClientSecret
	default:
		return ""
	}
}

func mergeMetadata(acc *db.Account, metadata map[string]interface{}) {
	if metadata == nil {
		return
	}
	if acc.Metadata == nil {
		acc.Metadata = map[string]interface{}{}
	}
	for key, value := range metadata {
		acc.Metadata[key] = value
	}
}

func normalizeCodexMetadata(metadata map[string]interface{}) {
	if metadata == nil {
		return
	}
	metadata["auth_mode"] = "chatgpt"
	accountID := ""
	if id, ok := metadata["account_id"].(string); ok && strings.TrimSpace(id) != "" {
		accountID = strings.TrimSpace(id)
	}
	if id, ok := metadata["chatgpt_account_id"].(string); ok && strings.TrimSpace(id) != "" {
		accountID = strings.TrimSpace(id)
	}
	if accountID != "" {
		metadata["account_id"] = accountID
		metadata["chatgpt_account_id"] = accountID
	}
}

func compactOAuthBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	for _, key := range []string{"access_token", "refresh_token", "id_token", "client_secret", "authorization", "api_key"} {
		lower := strings.ToLower(text)
		for {
			idx := strings.Index(lower, strings.ToLower(key))
			if idx < 0 {
				break
			}
			sep := strings.IndexAny(text[idx+len(key):], ":=")
			if sep < 0 {
				break
			}
			start := idx + len(key) + sep + 1
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
			text = text[:start] + "[redacted]" + text[end:]
			lower = strings.ToLower(text)
		}
	}
	if len(text) > 300 {
		return text[:300] + "..."
	}
	if text == "" {
		return "empty response"
	}
	return text
}
