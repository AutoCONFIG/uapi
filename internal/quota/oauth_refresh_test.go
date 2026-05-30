package quota

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

func TestOAuthRefreshClientDefaultsCoverAllOAuthFormats(t *testing.T) {
	tests := []struct {
		format     string
		wantID     string
		wantSecret string
	}{
		{format: "codex", wantID: openai.DefaultClientID},
		{format: "claude_code", wantID: anthropic.DefaultClientID},
		{format: "gemini_code", wantID: gemini.DefaultClientID, wantSecret: gemini.DefaultClientSecret},
		{format: "antigravity", wantID: antigravity.DefaultClientID, wantSecret: antigravity.DefaultClientSecret},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			if got := oauthClientIDForFormat("", tt.format); got != tt.wantID {
				t.Fatalf("client id = %q, want %q", got, tt.wantID)
			}
			if got := oauthClientSecretForFormat(&db.Account{}, tt.format); got != tt.wantSecret {
				t.Fatalf("client secret = %q, want %q", got, tt.wantSecret)
			}
		})
	}
}
