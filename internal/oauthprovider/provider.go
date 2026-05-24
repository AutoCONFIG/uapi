package oauthprovider

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

type ExchangeRequest struct {
	Code         string
	RedirectURI  string
	CodeVerifier string
	ClientID     string
	ClientSecret string
	TokenURL     string
	State        string
}

type ExchangeResult struct {
	Credential   string
	RefreshToken string
	Expiry       *time.Time
	Metadata     map[string]interface{}
}

type Provider interface {
	Key() string
	DefaultClientID() string
	DefaultClientSecret() string
	DefaultTokenURL() string
	DefaultRedirectURI() string
	ManualCallback() bool
	ChannelAllowed(db.Channel) bool
	TokenURLAllowed(string) bool
	PKCE() (verifier, challenge string, err error)
	BuildAuthURL(clientID, redirectURI, challenge, state string) string
	Exchange(ExchangeRequest) (*ExchangeResult, error)
	SyncMetadata(accessToken string, metadata map[string]interface{}) (map[string]interface{}, error)
}

var registry = map[string]Provider{}

func Register(provider Provider) {
	if provider == nil || strings.TrimSpace(provider.Key()) == "" {
		panic("oauthprovider: invalid provider")
	}
	registry[strings.ToLower(provider.Key())] = provider
}

func Get(key string) (Provider, bool) {
	provider, ok := registry[strings.ToLower(strings.TrimSpace(key))]
	return provider, ok
}

func SupportedKeys() []string {
	keys := make([]string, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	return keys
}

type providerImpl struct {
	key                 string
	defaultClientID     string
	defaultClientSecret string
	defaultTokenURL     string
	defaultRedirectURI  string
	manualCallback      bool
	channelAllowed      func(db.Channel) bool
	pkce                func() (string, string, error)
	buildAuthURL        func(clientID, redirectURI, challenge, state string) string
	exchange            func(ExchangeRequest) (*ExchangeResult, error)
	syncMetadata        func(accessToken string, metadata map[string]interface{}) (map[string]interface{}, error)
}

func (p providerImpl) Key() string                 { return p.key }
func (p providerImpl) DefaultClientID() string     { return p.defaultClientID }
func (p providerImpl) DefaultClientSecret() string { return p.defaultClientSecret }
func (p providerImpl) DefaultTokenURL() string     { return p.defaultTokenURL }
func (p providerImpl) DefaultRedirectURI() string  { return p.defaultRedirectURI }
func (p providerImpl) ManualCallback() bool        { return p.manualCallback }
func (p providerImpl) ChannelAllowed(ch db.Channel) bool {
	return p.channelAllowed != nil && p.channelAllowed(ch)
}
func (p providerImpl) TokenURLAllowed(raw string) bool { return tokenURLIs(raw, p.defaultTokenURL) }
func (p providerImpl) PKCE() (string, string, error) {
	if p.pkce == nil {
		return "", "", nil
	}
	return p.pkce()
}
func (p providerImpl) BuildAuthURL(clientID, redirectURI, challenge, state string) string {
	return p.buildAuthURL(clientID, redirectURI, challenge, state)
}
func (p providerImpl) Exchange(req ExchangeRequest) (*ExchangeResult, error) { return p.exchange(req) }
func (p providerImpl) SyncMetadata(accessToken string, metadata map[string]interface{}) (map[string]interface{}, error) {
	if p.syncMetadata == nil {
		return metadata, nil
	}
	return p.syncMetadata(accessToken, metadata)
}

func init() {
	Register(providerImpl{
		key: "openai", defaultClientID: openai.DefaultClientID, defaultTokenURL: openai.DefaultTokenURL,
		defaultRedirectURI: openai.DefaultRedirectURI, manualCallback: true,
		channelAllowed: func(ch db.Channel) bool { return strings.EqualFold(ch.Type, "openai") && ch.APIFormat == "codex" },
		pkce: func() (string, string, error) {
			v, err := openai.GenerateCodeVerifier()
			if err != nil {
				return "", "", err
			}
			return v, openai.GenerateCodeChallenge(v), nil
		},
		buildAuthURL: openai.BuildAuthURL,
		exchange: func(req ExchangeRequest) (*ExchangeResult, error) {
			tokens, err := openai.ExchangeCode(req.TokenURL, req.Code, req.RedirectURI, req.CodeVerifier, req.ClientID)
			if err != nil {
				return nil, err
			}
			credential := tokens.AccessToken
			var metadata map[string]interface{}
			if tokens.IDToken != "" {
				if parsed, err := openai.ParseIDTokenMetadata(tokens.IDToken); err == nil {
					metadata = parsed
				}
			}
			if usage, err := openai.FetchCodexUsage(tokens.AccessToken, metadataString(metadata, "chatgpt_account_id"), metadataBool(metadata, "chatgpt_account_is_fedramp")); err == nil {
				if metadata == nil {
					metadata = map[string]interface{}{}
				}
				metadata["codex_usage"] = usage
			}
			if tokens.IDToken != "" {
				if exchanged, err := openai.ExchangeForAPIKey(req.TokenURL, tokens.IDToken, req.ClientID); err == nil && exchanged != "" {
					credential = exchanged
				}
			}
			exp := time.Now().Add(8 * 24 * time.Hour)
			if tokens.ExpiresIn > 0 {
				exp = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
			}
			return &ExchangeResult{Credential: credential, RefreshToken: tokens.RefreshToken, Expiry: &exp, Metadata: metadata}, nil
		},
	})

	Register(providerImpl{
		key: "gemini", defaultClientID: gemini.DefaultClientID, defaultClientSecret: gemini.DefaultClientSecret,
		defaultTokenURL: gemini.DefaultTokenURL, defaultRedirectURI: gemini.DefaultRedirectURI, manualCallback: true,
		channelAllowed: func(ch db.Channel) bool { return strings.EqualFold(ch.Type, "gemini") && ch.APIFormat == "gemini_code" },
		pkce: func() (string, string, error) {
			v, err := gemini.GenerateCodeVerifier()
			if err != nil {
				return "", "", err
			}
			return v, gemini.GenerateCodeChallenge(v), nil
		},
		buildAuthURL: gemini.BuildAuthURL,
		exchange: func(req ExchangeRequest) (*ExchangeResult, error) {
			tokens, err := gemini.ExchangeCode(req.TokenURL, req.Code, req.RedirectURI, req.CodeVerifier, req.ClientID, req.ClientSecret)
			if err != nil {
				return nil, err
			}
			exp := time.Now().Add(time.Hour)
			if tokens.ExpiresIn > 0 {
				exp = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
			}
			metadata, _ := gemini.FetchCodeAssistMetadata(tokens.AccessToken, "")
			return &ExchangeResult{Credential: tokens.AccessToken, RefreshToken: tokens.RefreshToken, Expiry: &exp, Metadata: metadata}, nil
		},
		syncMetadata: func(accessToken string, metadata map[string]interface{}) (map[string]interface{}, error) {
			return gemini.FetchCodeAssistMetadata(accessToken, geminiProjectID(metadata))
		},
	})

	Register(providerImpl{
		key: "anthropic", defaultClientID: anthropic.DefaultClientID, defaultTokenURL: anthropic.DefaultTokenURL,
		defaultRedirectURI: anthropic.DefaultRedirectURI, manualCallback: true,
		channelAllowed: func(ch db.Channel) bool {
			return strings.EqualFold(ch.Type, "anthropic") && ch.APIFormat == "claude_code"
		},
		pkce: func() (string, string, error) {
			v, err := anthropic.GenerateCodeVerifier()
			if err != nil {
				return "", "", err
			}
			return v, anthropic.GenerateCodeChallenge(v), nil
		},
		buildAuthURL: anthropic.BuildAuthURL,
		exchange: func(req ExchangeRequest) (*ExchangeResult, error) {
			tokens, err := anthropic.ExchangeCode(req.TokenURL, req.Code, req.RedirectURI, req.CodeVerifier, req.ClientID, req.State)
			if err != nil {
				return nil, err
			}
			exp := time.Now().Add(time.Hour)
			if tokens.ExpiresIn > 0 {
				exp = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
			}
			metadata, _ := anthropic.FetchAccountMetadata(tokens.AccessToken, strings.Fields(tokens.Scope))
			return &ExchangeResult{Credential: tokens.AccessToken, RefreshToken: tokens.RefreshToken, Expiry: &exp, Metadata: metadata}, nil
		},
		syncMetadata: func(accessToken string, _ map[string]interface{}) (map[string]interface{}, error) {
			return anthropic.FetchAccountMetadata(accessToken, strings.Fields(anthropic.DefaultScope))
		},
	})

	Register(providerImpl{
		key: "antigravity", defaultClientID: antigravity.DefaultClientID, defaultClientSecret: antigravity.DefaultClientSecret,
		defaultTokenURL: antigravity.DefaultTokenURL, defaultRedirectURI: antigravity.DefaultRedirectURI, manualCallback: true,
		channelAllowed: func(ch db.Channel) bool {
			return strings.EqualFold(ch.Type, "antigravity") && ch.APIFormat == "antigravity"
		},
		buildAuthURL: func(clientID, redirectURI, _ string, state string) string {
			return antigravity.BuildAuthURL(clientID, redirectURI, state)
		},
		exchange: func(req ExchangeRequest) (*ExchangeResult, error) {
			tokens, err := antigravity.ExchangeCode(req.TokenURL, req.Code, req.RedirectURI, req.ClientID, req.ClientSecret)
			if err != nil {
				return nil, err
			}
			exp := time.Now().Add(time.Hour)
			if tokens.ExpiresIn > 0 {
				exp = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
			}
			metadata, _ := antigravity.FetchAccountMetadata(tokens.AccessToken)
			if metadata == nil {
				metadata = map[string]interface{}{}
			}
			metadata["oauth_provider"] = "antigravity"
			return &ExchangeResult{Credential: tokens.AccessToken, RefreshToken: tokens.RefreshToken, Expiry: &exp, Metadata: metadata}, nil
		},
		syncMetadata: func(accessToken string, _ map[string]interface{}) (map[string]interface{}, error) {
			return antigravity.FetchAccountMetadata(accessToken)
		},
	})
}

func tokenURLIs(rawURL, expectedURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	expected, err := url.Parse(expectedURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, expected.Scheme) && strings.EqualFold(parsed.Hostname(), expected.Hostname()) && parsed.Port() == "" && parsed.EscapedPath() == expected.EscapedPath() && parsed.RawQuery == "" && parsed.Fragment == ""
}

func metadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return value
}

func metadataBool(metadata map[string]interface{}, key string) bool {
	if metadata == nil {
		return false
	}
	value, _ := metadata[key].(bool)
	return value
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

func Require(key string) (Provider, error) {
	provider, ok := Get(key)
	if !ok {
		return nil, fmt.Errorf("unsupported oauth provider: %s", key)
	}
	return provider, nil
}
