package relay

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

func TestIsOAuthAuthFailureOnlyRefreshesOAuthAuthErrors(t *testing.T) {
	oauthAccount := &db.Account{CredType: "oauth_token", RefreshToken: "encrypted-refresh-token"}

	if !isOAuthAuthFailure(oauthAccount, fasthttp.StatusUnauthorized, []byte(`{"error":"invalid_token"}`)) {
		t.Fatalf("401 from oauth account should trigger refresh")
	}
	if !isOAuthAuthFailure(oauthAccount, fasthttp.StatusForbidden, []byte(`{"error":{"message":"Access token expired"}}`)) {
		t.Fatalf("explicit token expiry should trigger refresh")
	}
	if isOAuthAuthFailure(oauthAccount, fasthttp.StatusTooManyRequests, []byte(`{"error":"quota exceeded"}`)) {
		t.Fatalf("quota errors must not trigger oauth refresh")
	}
	if isOAuthAuthFailure(&db.Account{CredType: "api_key"}, fasthttp.StatusUnauthorized, []byte(`{"error":"invalid_token"}`)) {
		t.Fatalf("api key accounts must not trigger oauth refresh")
	}
	if isOAuthAuthFailure(&db.Account{CredType: "oauth_token"}, fasthttp.StatusUnauthorized, []byte(`{"error":"invalid_token"}`)) {
		t.Fatalf("oauth accounts without refresh token cannot refresh")
	}
}
