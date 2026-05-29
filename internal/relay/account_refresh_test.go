package relay

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/google/uuid"
)

func TestShouldRefreshOAuthCredentialsUsesStableJitterWindow(t *testing.T) {
	account := &db.Account{Base: db.Base{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001")}, CredType: "oauth_token"}
	expirySoon := time.Now().Add(oauthRefreshSkew(account) - time.Second)
	account.TokenExpiry = &expirySoon

	if !shouldRefreshOAuthCredentialsForChannel(account, &db.Channel{APIFormat: "gemini_code"}) {
		t.Fatalf("account inside stable refresh window should refresh")
	}

	expiryLater := time.Now().Add(oauthRefreshSkew(account) + time.Minute)
	account.TokenExpiry = &expiryLater
	if shouldRefreshOAuthCredentialsForChannel(account, &db.Channel{APIFormat: "gemini_code"}) {
		t.Fatalf("account outside stable refresh window should not refresh")
	}
}

func TestShouldRefreshOAuthCredentialsCoversAllOAuthFormats(t *testing.T) {
	formats := []string{"codex", "gemini_code", "claude_code", "antigravity"}
	for _, format := range formats {
		account := &db.Account{Base: db.Base{ID: uuid.New()}, CredType: "oauth_token", RefreshToken: "encrypted-refresh-token"}
		if !shouldRefreshOAuthCredentialsForChannel(account, &db.Channel{APIFormat: format}) {
			t.Fatalf("%s oauth account without expiry should refresh on use", format)
		}

		expired := time.Now().Add(-time.Second)
		account.TokenExpiry = &expired
		if !shouldRefreshOAuthCredentialsForChannel(account, &db.Channel{APIFormat: format}) {
			t.Fatalf("%s expired oauth account should refresh on use", format)
		}
	}
}

func TestOAuthProviderAndTokenURLPreferChannelAPIFormat(t *testing.T) {
	account := &db.Account{TokenURL: gemini.DefaultTokenURL}
	ch := &db.Channel{APIFormat: "antigravity"}

	if got := oauthProviderKeyForChannel(account, ch); got != "antigravity" {
		t.Fatalf("provider = %q, want antigravity", got)
	}
	if got := oauthTokenURLForChannel(&db.Account{}, ch); got != antigravity.DefaultTokenURL {
		t.Fatalf("token url = %q, want %q", got, antigravity.DefaultTokenURL)
	}
}
