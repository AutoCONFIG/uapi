package gemini

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// httpClient is shared across OAuth operations with a reasonable timeout.
var httpClient = &http.Client{Timeout: 30 * time.Second}

const (
	DefaultAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	DefaultTokenURL     = "https://oauth2.googleapis.com/token"
	DefaultClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	DefaultClientSecret = "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"
	DefaultRedirectURI  = "http://127.0.0.1:1456/oauth2callback"
	DefaultScope        = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
	CodeAssistEndpoint  = "https://cloudcode-pa.googleapis.com"
	CodeAssistVersion   = "v1internal"
	GeminiCLIVersion    = "0.44.0-nightly.20260512.g022e8baef"

	UserTierFree     = "free-tier"
	UserTierLegacy   = "legacy-tier"
	UserTierStandard = "standard-tier"
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// GenerateCodeVerifier creates a PKCE code verifier
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
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

// BuildAuthURL constructs the authorization URL. Gemini CLI browser auth does
// not use PKCE; PKCE is only included when a challenge is provided.
func BuildAuthURL(clientID, redirectURI, codeChallenge, state string) string {
	params := [][2]string{
		{"response_type", "code"},
		{"client_id", clientID},
		{"redirect_uri", redirectURI},
		{"access_type", "offline"},
		{"scope", DefaultScope},
		{"state", state},
	}
	if codeChallenge != "" {
		params = append(params, [2]string{"code_challenge", codeChallenge})
		params = append(params, [2]string{"code_challenge_method", "S256"})
	}
	return DefaultAuthURL + "?" + encodeGoogleQuery(params)
}

func encodeGoogleQuery(params [][2]string) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param[0]+"="+strings.ReplaceAll(url.QueryEscape(param[1]), "+", "%20"))
	}
	return strings.Join(parts, "&")
}

// ExchangeCode exchanges authorization code for tokens
func ExchangeCode(tokenURL, code, redirectURI, codeVerifier, clientID, clientSecret string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"client_id":    {clientID},
	}
	if codeVerifier != "" {
		data.Set("code_verifier", codeVerifier)
	}
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}
	resp, err := httpClient.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("exchange failed: status %d: %s", resp.StatusCode, compactBody(body))
	}
	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("exchange failed: %s", tokenResp.Error)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}
	return &tokenResp, nil
}

func setCodeAssistHeaders(req *http.Request, accessToken, model string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if model == "" {
		model = "gemini-2.5-pro"
	}
	req.Header.Set("User-Agent", GeminiCLIUserAgent(model))
}

func GeminiCLIUserAgent(model string) string {
	if model == "" {
		model = "gemini-2.5-pro"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return "GeminiCLI/" + GeminiCLIVersion + "/" + model + " (" + runtime.GOOS + "; " + arch + "; cli)"
}

func compactBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	text = redactOAuthBody(text)
	if text == "" {
		return "empty response"
	}
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
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

func FetchCodeAssistMetadata(accessToken, projectID string) (map[string]interface{}, error) {
	return SetupCodeAssistAccount(accessToken, projectID)
}

func SetupCodeAssistAccount(accessToken, projectID string) (map[string]interface{}, error) {
	loadRes, err := loadCodeAssist(accessToken, projectID, "")
	if err != nil {
		return nil, err
	}
	meta := codeAssistMetadata(loadRes, projectID)
	if validation := codeAssistValidation(loadRes); validation != nil {
		meta["setup_status"] = "validation_required"
		meta["validation_required"] = true
		meta["validation"] = validation
		return meta, nil
	}

	if _, ok := loadRes["currentTier"].(map[string]interface{}); !ok {
		tier := defaultOnboardTier(loadRes)
		if tierID := jsonString(tier, "id"); tierID != "" {
			onboardProjectID := projectID
			if tierID == UserTierFree {
				onboardProjectID = ""
			}
			operation, err := onboardUser(accessToken, tierID, onboardProjectID)
			if err != nil {
				meta["setup_status"] = "onboard_failed"
				meta["setup_error"] = err.Error()
				return meta, nil
			}
			operation, err = waitCodeAssistOperation(accessToken, operation)
			if err != nil {
				meta["setup_status"] = "onboard_pending"
				meta["operation"] = operation
				meta["setup_error"] = err.Error()
				return meta, nil
			}
			if response, ok := operation["response"].(map[string]interface{}); ok {
				if project := jsonString(response, "cloudaicompanionProject"); project != "" {
					projectID = project
				}
			}
			loadRes, err = loadCodeAssist(accessToken, projectID, "")
			if err == nil {
				meta = codeAssistMetadata(loadRes, projectID)
			}
		}
	}

	if _, ok := meta["setup_status"]; !ok {
		meta["setup_status"] = "ready"
		meta["validation_required"] = false
	}
	return meta, nil
}

func loadCodeAssist(accessToken, projectID, mode string) (map[string]interface{}, error) {
	reqBody := map[string]interface{}{
		"cloudaicompanionProject": stringOrNil(projectID),
		"metadata":                codeAssistClientMetadata(projectID),
	}
	if mode != "" {
		reqBody["mode"] = mode
	}
	return codeAssistPost(accessToken, "loadCodeAssist", reqBody)
}

func onboardUser(accessToken, tierID, projectID string) (map[string]interface{}, error) {
	reqBody := map[string]interface{}{
		"tierId":                  tierID,
		"cloudaicompanionProject": stringOrNil(projectID),
		"metadata":                codeAssistClientMetadata(projectID),
	}
	return codeAssistPost(accessToken, "onboardUser", reqBody)
}

func waitCodeAssistOperation(accessToken string, operation map[string]interface{}) (map[string]interface{}, error) {
	name, _ := operation["name"].(string)
	if name == "" || boolValue(operation["done"]) {
		return operation, nil
	}
	for i := 0; i < 24; i++ {
		time.Sleep(5 * time.Second)
		current, err := getCodeAssistOperation(accessToken, name)
		if err != nil {
			return operation, err
		}
		operation = current
		if boolValue(operation["done"]) {
			return operation, nil
		}
	}
	return operation, fmt.Errorf("onboard operation %s did not complete", name)
}

func getCodeAssistOperation(accessToken, name string) (map[string]interface{}, error) {
	endpoint := strings.TrimRight(CodeAssistEndpoint, "/") + "/" + strings.TrimLeft(name, "/")
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	setCodeAssistHeaders(req, accessToken, "")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get operation failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read operation response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get operation failed: status %d: %s", resp.StatusCode, compactBody(respBody))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse operation response: %w", err)
	}
	return result, nil
}

func codeAssistPost(accessToken, method string, reqBody map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequest(http.MethodPost, CodeAssistEndpoint+"/"+CodeAssistVersion+":"+method, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	setCodeAssistHeaders(req, accessToken, "")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", method, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s failed: status %d: %s", method, resp.StatusCode, compactBody(respBody))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse %s response: %w", method, err)
	}
	return result, nil
}

func codeAssistMetadata(loadRes map[string]interface{}, projectID string) map[string]interface{} {
	meta := map[string]interface{}{
		"load_code_assist": loadRes,
		"last_synced_at":   time.Now().UTC().Format(time.RFC3339),
	}
	if project := jsonString(loadRes, "cloudaicompanionProject"); project != "" {
		meta["project_id"] = project
	} else if projectID != "" {
		meta["project_id"] = projectID
	}
	if tier, ok := loadRes["currentTier"].(map[string]interface{}); ok {
		meta["user_tier"] = tier
	}
	if paidTier, ok := loadRes["paidTier"].(map[string]interface{}); ok {
		meta["paid_tier"] = paidTier
	}
	return meta
}

func codeAssistClientMetadata(projectID string) map[string]interface{} {
	return map[string]interface{}{
		"ideType":     "IDE_UNSPECIFIED",
		"platform":    "PLATFORM_UNSPECIFIED",
		"pluginType":  "GEMINI",
		"duetProject": stringOrNil(projectID),
	}
}

func codeAssistValidation(loadRes map[string]interface{}) map[string]interface{} {
	tiers, ok := loadRes["ineligibleTiers"].([]interface{})
	if !ok {
		return nil
	}
	for _, item := range tiers {
		tier, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if jsonString(tier, "reasonCode") == "VALIDATION_REQUIRED" && jsonString(tier, "validationUrl") != "" {
			return map[string]interface{}{
				"reason_code":     jsonString(tier, "reasonCode"),
				"reason_message":  jsonString(tier, "reasonMessage"),
				"validation_url":  jsonString(tier, "validationUrl"),
				"tier_id":         jsonString(tier, "tierId"),
				"tier_name":       jsonString(tier, "tierName"),
				"link_text":       jsonString(tier, "validationUrlLinkText"),
				"learn_more_url":  jsonString(tier, "validationLearnMoreUrl"),
				"learn_more_text": jsonString(tier, "validationLearnMoreLinkText"),
			}
		}
	}
	return nil
}

func defaultOnboardTier(loadRes map[string]interface{}) map[string]interface{} {
	if tiers, ok := loadRes["allowedTiers"].([]interface{}); ok {
		for _, item := range tiers {
			if tier, ok := item.(map[string]interface{}); ok && boolValue(tier["isDefault"]) {
				return tier
			}
		}
	}
	return map[string]interface{}{"id": UserTierLegacy}
}

func boolValue(value interface{}) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func stringOrNil(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func jsonString(data map[string]interface{}, key string) string {
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	if object, ok := value.(map[string]interface{}); ok {
		if id, ok := object["id"].(string); ok {
			return id
		}
	}
	return ""
}
