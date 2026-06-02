package admin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
)

func TestParseOAuthJSONGeminiCLIToken(t *testing.T) {
	expiry := time.Now().Add(45 * time.Minute).Truncate(time.Millisecond)
	raw := fmt.Sprintf(`{
		"access_token": "ya29.test",
		"refresh_token": "1//refresh",
		"scope": "https://www.googleapis.com/auth/cloud-platform openid https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/userinfo.email",
		"token_type": "Bearer",
		"id_token": "id.test",
		"expiry_date": %d
	}`, expiry.UnixMilli())

	got, err := parseOAuthJSON(raw)
	if err != nil {
		t.Fatalf("parseOAuthJSON returned error: %v", err)
	}
	if got.AccessToken != "ya29.test" {
		t.Fatalf("AccessToken = %q", got.AccessToken)
	}
	if got.RefreshToken != "1//refresh" {
		t.Fatalf("RefreshToken = %q", got.RefreshToken)
	}
	if got.Scope == "" {
		t.Fatal("Scope was not parsed")
	}
	if got.TokenType != "Bearer" {
		t.Fatalf("TokenType = %q", got.TokenType)
	}
	if got.IDToken != "id.test" {
		t.Fatalf("IDToken = %q", got.IDToken)
	}
	if got.Expiry == nil {
		t.Fatal("Expiry was nil")
	}
	if !got.Expiry.Equal(expiry) {
		t.Fatalf("Expiry = %s, want %s", got.Expiry, expiry)
	}
}

func TestImportedMetadataKeepsOAuthImportContext(t *testing.T) {
	expiry := time.Now().Add(45 * time.Minute).Truncate(time.Millisecond)
	meta := importedMetadata(&oauthJSONImport{
		Scope:     "scope-a scope-b",
		TokenType: "Bearer",
		IDToken:   "id.test",
		Expiry:    &expiry,
	})

	if meta["oauth_scope"] != "scope-a scope-b" {
		t.Fatalf("oauth_scope = %#v", meta["oauth_scope"])
	}
	if meta["oauth_token_type"] != "Bearer" {
		t.Fatalf("oauth_token_type = %#v", meta["oauth_token_type"])
	}
	if meta["oauth_has_id_token"] != true {
		t.Fatalf("oauth_has_id_token = %#v", meta["oauth_has_id_token"])
	}
	if meta["oauth_expiry"] != expiry.UTC().Format(time.RFC3339) {
		t.Fatalf("oauth_expiry = %#v", meta["oauth_expiry"])
	}
	if meta["oauth_imported_at"] == "" {
		t.Fatal("oauth_imported_at was not set")
	}
}

func TestParseOAuthJSONCodexAuthJSON(t *testing.T) {
	idToken := fakeCodexIDToken(t, "acct-official")
	raw := fmt.Sprintf(`{
		"auth_mode": "chatgpt",
		"OPENAI_API_KEY": null,
		"tokens": {
			"id_token": %q,
			"access_token": "access-token",
			"refresh_token": "refresh-token",
			"account_id": "acct-official"
		}
	}`, idToken)

	got, err := parseOAuthJSON(raw)
	if err != nil {
		t.Fatalf("parseOAuthJSON returned error: %v", err)
	}
	if !got.HasTokens {
		t.Fatal("HasTokens was false")
	}
	if got.AuthMode != "chatgpt" {
		t.Fatalf("AuthMode = %q", got.AuthMode)
	}
	if got.IDToken != idToken {
		t.Fatalf("IDToken = %q", got.IDToken)
	}
	if got.AccessToken != "access-token" {
		t.Fatalf("AccessToken = %q", got.AccessToken)
	}
	if got.RefreshToken != "refresh-token" {
		t.Fatalf("RefreshToken = %q", got.RefreshToken)
	}
	if got.AccountID != "acct-official" {
		t.Fatalf("AccountID = %q", got.AccountID)
	}
}

func TestImportedMetadataCodexParsesIDTokenAndOfficialAccountID(t *testing.T) {
	idToken := fakeCodexIDToken(t, "acct-from-token")
	meta := importedMetadata(&oauthJSONImport{
		IDToken:   idToken,
		AccountID: "acct-official",
	})

	if meta["raw_id_token"] != idToken {
		t.Fatalf("raw_id_token = %#v", meta["raw_id_token"])
	}
	if meta["account_id"] != "acct-official" {
		t.Fatalf("account_id = %#v", meta["account_id"])
	}
	if meta["chatgpt_account_id"] != "acct-official" {
		t.Fatalf("chatgpt_account_id = %#v", meta["chatgpt_account_id"])
	}
	if meta["auth_mode"] != "chatgpt" {
		t.Fatalf("auth_mode = %#v", meta["auth_mode"])
	}
}

func TestExportCodexAuthJSONUsesOfficialShape(t *testing.T) {
	idToken := fakeCodexIDToken(t, "acct-export")
	out, err := exportCodexAuthJSON(&db.Account{Metadata: map[string]interface{}{
		"raw_id_token":       idToken,
		"chatgpt_account_id": "acct-export",
	}}, "access-token", "refresh-token")
	if err != nil {
		t.Fatalf("exportCodexAuthJSON returned error: %v", err)
	}
	if out["auth_mode"] != "chatgpt" {
		t.Fatalf("auth_mode = %#v", out["auth_mode"])
	}
	tokens, ok := out["tokens"].(map[string]interface{})
	if !ok {
		t.Fatalf("tokens = %#v", out["tokens"])
	}
	if tokens["id_token"] != idToken {
		t.Fatalf("tokens.id_token = %#v", tokens["id_token"])
	}
	if tokens["access_token"] != "access-token" {
		t.Fatalf("tokens.access_token = %#v", tokens["access_token"])
	}
	if tokens["refresh_token"] != "refresh-token" {
		t.Fatalf("tokens.refresh_token = %#v", tokens["refresh_token"])
	}
	if tokens["account_id"] != "acct-export" {
		t.Fatalf("tokens.account_id = %#v", tokens["account_id"])
	}
	if _, ok := out["access_token"]; ok {
		t.Fatal("top-level access_token should not be exported for Codex")
	}
}

func fakeCodexIDToken(t *testing.T, accountID string) string {
	t.Helper()
	header := map[string]interface{}{"alg": "none", "typ": "JWT"}
	payload := map[string]interface{}{
		"email": "user@example.com",
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_account_id": accountID,
		},
	}
	encode := func(value interface{}) string {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal jwt part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(body)
	}
	return encode(header) + "." + encode(payload) + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
}
