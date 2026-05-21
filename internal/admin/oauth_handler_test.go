package admin

import (
	"fmt"
	"testing"
	"time"
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
