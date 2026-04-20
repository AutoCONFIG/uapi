package manager

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/config"
	"github.com/AutoCONFIG/cli-relay/internal/provider"
)

// TokenStorer is the minimal interface the manager needs for token persistence.
type TokenStorer interface {
	Load(ctx context.Context, providerName string) (*provider.TokenSet, error)
	Save(ctx context.Context, providerName string, tokens *provider.TokenSet) error
	Delete(ctx context.Context, providerName string) error
}

// ProviderStatus represents the current health of a provider's authentication.
type ProviderStatus string

const (
	StatusUnknown       ProviderStatus = "unknown"
	StatusActive        ProviderStatus = "active"
	StatusStale         ProviderStatus = "stale"
	StatusExpired       ProviderStatus = "expired"
	StatusRefreshFailed ProviderStatus = "refresh_failed"
	StatusNoAuth        ProviderStatus = "no_auth"
)

// ProviderInfo is the manager's view of a single provider's state.
type ProviderInfo struct {
	Name        string         `json:"name"`
	Status      ProviderStatus `json:"status"`
	AuthMethod  string         `json:"auth_method,omitempty"`
	LastRefresh *time.Time     `json:"last_refresh,omitempty"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// TokenManager orchestrates token lifecycle across all configured providers.
type TokenManager struct {
	providers map[string]provider.Provider
	store     TokenStorer
	configs   map[string]config.ProviderConfig
	logger    *slog.Logger

	mu           sync.RWMutex
	cachedTokens map[string]*provider.TokenSet
	statuses     map[string]ProviderInfo
	permFailures map[string]error // permanent refresh failures, cached

	onTokenChange []func(providerName string, tokens *provider.TokenSet)
}

// NewTokenManager creates a manager with the given providers and store.
func NewTokenManager(
	providers []provider.Provider,
	store TokenStorer,
	configs map[string]config.ProviderConfig,
	logger *slog.Logger,
) *TokenManager {
	m := &TokenManager{
		providers:    make(map[string]provider.Provider),
		store:        store,
		configs:      configs,
		logger:       logger,
		cachedTokens: make(map[string]*provider.TokenSet),
		statuses:     make(map[string]ProviderInfo),
		permFailures: make(map[string]error),
	}

	for _, p := range providers {
		m.providers[p.Name()] = p
		m.statuses[p.Name()] = ProviderInfo{
			Name:   p.Name(),
			Status: StatusUnknown,
		}
	}

	return m
}

// GetToken returns valid tokens for a provider, refreshing if necessary.
func (m *TokenManager) GetToken(ctx context.Context, providerName string) (*provider.TokenSet, error) {
	p, ok := m.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	// Check for permanent failure
	if err, failed := m.permFailures[providerName]; failed {
		return nil, fmt.Errorf("provider %s has permanent auth failure: %w", providerName, err)
	}

	// Try cache first
	m.mu.RLock()
	cached, hasCached := m.cachedTokens[providerName]
	m.mu.RUnlock()

	if hasCached && !cached.NeedsRefresh(m.proactiveRefreshAge(providerName)) {
		return cached, nil
	}

	// Load from store
	tokens, err := m.store.Load(ctx, providerName)
	if err != nil {
		return nil, fmt.Errorf("load tokens for %s: %w", providerName, err)
	}

	if tokens == nil || tokens.IsEmpty() {
		m.updateStatus(providerName, StatusNoAuth, "no tokens stored")
		return nil, fmt.Errorf("no auth for provider %s; run login first", providerName)
	}

	// Check if refresh is needed
	if tokens.NeedsRefresh(m.proactiveRefreshAge(providerName)) {
		newTokens, err := p.Refresh(ctx, tokens)
		if err != nil {
			if isPermanent(err) {
				m.permFailures[providerName] = err
				m.updateStatus(providerName, StatusRefreshFailed, err.Error())
			}
			// Return stale tokens if still valid
			if !tokens.IsExpired() {
				m.logger.Warn("refresh failed, returning stale token",
					"provider", providerName, "error", err)
				return tokens, nil
			}
			return nil, fmt.Errorf("refresh failed for %s: %w", providerName, err)
		}

		if err := m.store.Save(ctx, providerName, newTokens); err != nil {
			m.logger.Error("failed to save refreshed tokens", "provider", providerName, "error", err)
		}

		tokens = newTokens
		m.notifyTokenChange(providerName, tokens)
	}

	m.mu.Lock()
	m.cachedTokens[providerName] = tokens
	m.mu.Unlock()

	m.updateStatus(providerName, StatusActive, "")
	return tokens, nil
}

// Login initiates an interactive login for a provider.
func (m *TokenManager) Login(ctx context.Context, providerName string, method provider.AuthMethod) (*provider.TokenSet, error) {
	p, ok := m.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	tokens, err := p.Login(ctx, method)
	if err != nil {
		return nil, fmt.Errorf("login for %s: %w", providerName, err)
	}

	if err := m.store.Save(ctx, providerName, tokens); err != nil {
		return nil, fmt.Errorf("save tokens for %s: %w", providerName, err)
	}

	m.mu.Lock()
	m.cachedTokens[providerName] = tokens
	delete(m.permFailures, providerName)
	m.mu.Unlock()

	m.updateStatus(providerName, StatusActive, "")
	m.notifyTokenChange(providerName, tokens)

	return tokens, nil
}

// Logout revokes and deletes tokens for a provider.
func (m *TokenManager) Logout(ctx context.Context, providerName string) error {
	p, ok := m.providers[providerName]
	if !ok {
		return fmt.Errorf("unknown provider: %s", providerName)
	}

	tokens, _ := m.store.Load(ctx, providerName)
	if tokens != nil {
		if err := p.Revoke(ctx, tokens); err != nil {
			m.logger.Warn("revoke failed (non-fatal)", "provider", providerName, "error", err)
		}
	}

	if err := m.store.Delete(ctx, providerName); err != nil {
		return fmt.Errorf("delete tokens for %s: %w", providerName, err)
	}

	m.mu.Lock()
	delete(m.cachedTokens, providerName)
	delete(m.permFailures, providerName)
	m.mu.Unlock()

	m.updateStatus(providerName, StatusNoAuth, "")
	return nil
}

// Status returns the current status of all providers.
func (m *TokenManager) Status(_ context.Context) []ProviderInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ProviderInfo, 0, len(m.providers))
	for _, p := range m.providers {
		info, ok := m.statuses[p.Name()]
		if !ok {
			info = ProviderInfo{Name: p.Name(), Status: StatusUnknown}
		}
		result = append(result, info)
	}
	return result
}

// RefreshForce forces a refresh attempt for a provider.
func (m *TokenManager) RefreshForce(ctx context.Context, providerName string) (*provider.TokenSet, error) {
	p, ok := m.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	tokens, err := m.store.Load(ctx, providerName)
	if err != nil {
		return nil, fmt.Errorf("load tokens for %s: %w", providerName, err)
	}
	if tokens == nil || tokens.IsEmpty() {
		return nil, fmt.Errorf("no tokens to refresh for %s", providerName)
	}

	newTokens, err := p.Refresh(ctx, tokens)
	if err != nil {
		if isPermanent(err) {
			m.permFailures[providerName] = err
			m.updateStatus(providerName, StatusRefreshFailed, err.Error())
		}
		return nil, err
	}

	if err := m.store.Save(ctx, providerName, newTokens); err != nil {
		m.logger.Error("failed to save refreshed tokens", "provider", providerName, "error", err)
	}

	m.mu.Lock()
	m.cachedTokens[providerName] = newTokens
	delete(m.permFailures, providerName)
	m.mu.Unlock()

	m.updateStatus(providerName, StatusActive, "")
	m.notifyTokenChange(providerName, newTokens)

	return newTokens, nil
}

// Recover attempts to recover from a 401 error for a provider.
func (m *TokenManager) Recover(ctx context.Context, providerName string) error {
	// Step 1: Reload from store (another process may have refreshed)
	tokens, err := m.store.Load(ctx, providerName)
	if err != nil {
		return fmt.Errorf("reload tokens: %w", err)
	}
	if tokens != nil && !tokens.IsExpired() {
		m.mu.Lock()
		m.cachedTokens[providerName] = tokens
		delete(m.permFailures, providerName)
		m.mu.Unlock()
		m.updateStatus(providerName, StatusActive, "")
		return nil
	}

	// Step 2: Force refresh
	_, err = m.RefreshForce(ctx, providerName)
	if err != nil {
		m.updateStatus(providerName, StatusRefreshFailed, err.Error())
		return fmt.Errorf("recovery failed for %s: %w", providerName, err)
	}

	return nil
}

// OnTokenChange registers a callback invoked whenever tokens are refreshed.
func (m *TokenManager) OnTokenChange(fn func(providerName string, tokens *provider.TokenSet)) {
	m.onTokenChange = append(m.onTokenChange, fn)
}

func (m *TokenManager) notifyTokenChange(providerName string, tokens *provider.TokenSet) {
	for _, fn := range m.onTokenChange {
		fn(providerName, tokens)
	}
}

func (m *TokenManager) updateStatus(providerName string, status ProviderStatus, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	info := ProviderInfo{
		Name:   providerName,
		Status: status,
		Error:  errMsg,
	}

	if tokens, ok := m.cachedTokens[providerName]; ok {
		info.LastRefresh = tokens.LastRefresh
		info.ExpiresAt = tokens.ExpiresAt
	}

	m.statuses[providerName] = info
}

func (m *TokenManager) proactiveRefreshAge(providerName string) time.Duration {
	if cfg, ok := m.configs[providerName]; ok && cfg.ProactiveRefreshAge > 0 {
		return cfg.ProactiveRefreshAge
	}
	return 192 * time.Hour // default: 8 days
}

func isPermanent(err error) bool {
	// Check for permanent refresh errors from provider implementations
	type permanentError interface {
		Permanent() bool
	}
	if pe, ok := err.(permanentError); ok {
		return pe.Permanent()
	}
	return false
}
