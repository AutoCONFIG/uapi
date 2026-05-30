package relay

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	"github.com/google/uuid"
)

func TestShouldRefreshOAuthCredentialsUsesRequestRefreshWindow(t *testing.T) {
	account := &db.Account{Base: db.Base{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001")}, CredType: "oauth_token"}

	for _, format := range []string{"codex", "gemini_code", "claude_code", "antigravity"} {
		t.Run(format, func(t *testing.T) {
			expiryLater := time.Now().Add(requestAccessTokenRefreshWindow + time.Minute)
			account.TokenExpiry = &expiryLater
			if shouldRefreshOAuthCredentialsForChannel(account, &db.Channel{APIFormat: format}) {
				t.Fatalf("%s account with unexpired token should not refresh on request", format)
			}

			expirySoon := time.Now().Add(requestAccessTokenRefreshWindow - time.Second)
			account.TokenExpiry = &expirySoon
			if !shouldRefreshOAuthCredentialsForChannel(account, &db.Channel{APIFormat: format}) {
				t.Fatalf("%s account inside request refresh window should refresh on request", format)
			}

			expired := time.Now().Add(-time.Second)
			account.TokenExpiry = &expired
			if !shouldRefreshOAuthCredentialsForChannel(account, &db.Channel{APIFormat: format}) {
				t.Fatalf("%s expired oauth account should refresh on request", format)
			}
		})
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

func TestOAuthClientDefaultsCoverAllOAuthFormats(t *testing.T) {
	tests := []struct {
		providerKey string
		wantID      string
		wantSecret  string
	}{
		{providerKey: "openai", wantID: openai.DefaultClientID},
		{providerKey: "anthropic", wantID: anthropic.DefaultClientID},
		{providerKey: "gemini", wantID: gemini.DefaultClientID, wantSecret: gemini.DefaultClientSecret},
		{providerKey: "antigravity", wantID: antigravity.DefaultClientID, wantSecret: antigravity.DefaultClientSecret},
	}
	for _, tt := range tests {
		t.Run(tt.providerKey, func(t *testing.T) {
			if got := oauthClientIDForProvider("", tt.providerKey); got != tt.wantID {
				t.Fatalf("client id = %q, want %q", got, tt.wantID)
			}
			secret, err := oauthClientSecretForProvider(&db.Account{}, tt.providerKey)
			if err != nil {
				t.Fatalf("client secret: %v", err)
			}
			if secret != tt.wantSecret {
				t.Fatalf("client secret = %q, want %q", secret, tt.wantSecret)
			}
		})
	}
}

func TestOAuthRefreshHookReceivesAccountID(t *testing.T) {
	accountID := uuid.New()
	var got uuid.UUID
	r := &Relayer{}
	r.SetOAuthRefreshHook(func(id uuid.UUID) {
		got = id
	})

	r.notifyOAuthAccountRefreshed(accountID)

	if got != accountID {
		t.Fatalf("hook account id = %s, want %s", got, accountID)
	}
}
