package admin

import (
	"time"

	"github.com/google/uuid"
)

// --- Channel DTOs ---

// CreateChannelRequest is the request DTO for creating a channel.
type CreateChannelRequest struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Endpoint    string `json:"endpoint"`
	Models      string `json:"models"`
	Priority    int    `json:"priority"`
	APIFormat   string `json:"api_format"`
	ForceStream bool   `json:"force_stream"`
	AffinityTTL int    `json:"affinity_ttl"`
}

// UpdateChannelRequest is the request DTO for updating a channel.
type UpdateChannelRequest struct {
	Name        *string `json:"name,omitempty"`
	Type        *string `json:"type,omitempty"`
	Endpoint    *string `json:"endpoint,omitempty"`
	Models      *string `json:"models,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	APIFormat   *string `json:"api_format,omitempty"`
	ForceStream *bool   `json:"force_stream,omitempty"`
	AffinityTTL *int    `json:"affinity_ttl,omitempty"`
}

// StartOAuthRequest asks the backend to create a provider authorization URL.
type StartOAuthRequest struct {
	ChannelID    uuid.UUID `json:"channel_id"`
	Provider     string    `json:"provider"`
	AccountName  string    `json:"account_name"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	TokenURL     string    `json:"token_url"`
}

// OAuthAuthURLResponse is returned after creating an OAuth onboarding session.
type OAuthAuthURLResponse struct {
	AuthURL     string    `json:"auth_url"`
	State       string    `json:"state"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// OAuthStatusResponse describes a pending, completed, failed, or bound OAuth session.
type OAuthStatusResponse struct {
	State          string     `json:"state"`
	Provider       string     `json:"provider"`
	ChannelID      uuid.UUID  `json:"channel_id"`
	Status         string     `json:"status"`
	ReadyToBind    bool       `json:"ready_to_bind"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	BoundAccountID *uuid.UUID `json:"bound_account_id,omitempty"`
}

// BindOAuthAccountRequest creates a channel account from a completed OAuth session.
type BindOAuthAccountRequest struct {
	State       string `json:"state"`
	AccountName string `json:"account_name"`
	Weight      int    `json:"weight"`
	Enabled     *bool  `json:"enabled"`
}

// --- Account DTOs ---

// CreateAccountRequest is the request DTO for creating an account.
type CreateAccountRequest struct {
	ChannelID   uuid.UUID `json:"channel_id"`
	Name        string    `json:"name"`
	Credentials string    `json:"credentials"`
	Weight      int       `json:"weight"`
	Enabled     bool      `json:"enabled"`
}

// UpdateAccountRequest is the request DTO for updating an account.
type UpdateAccountRequest struct {
	ChannelID     uuid.UUID  `json:"channel_id"`
	Name          string     `json:"name"`
	Credentials   string     `json:"credentials"`
	Weight        *int       `json:"weight"`
	Enabled       *bool      `json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until"`
}

// --- Token DTOs ---

// CreateTokenRequest is the request DTO for creating a token.
type CreateTokenRequest struct {
	Name        string     `json:"name"`
	Key         string     `json:"key"`
	Enabled     bool       `json:"enabled"`
	IPWhitelist string     `json:"ip_whitelist"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Models      string     `json:"models"`
	Permissions string     `json:"permissions"`
	Unlimited   bool       `json:"unlimited"`
}

// UpdateTokenRequest is the request DTO for updating a token.
type UpdateTokenRequest struct {
	Name        *string    `json:"name,omitempty"`
	Key         *string    `json:"key,omitempty"`
	IPWhitelist *string    `json:"ip_whitelist,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Models      *string    `json:"models,omitempty"`
	Permissions *string    `json:"permissions,omitempty"`
	Unlimited   *bool      `json:"unlimited,omitempty"`
}

// --- Plan DTOs ---

// CreatePlanRequest is the request DTO for creating a plan.
type CreatePlanRequest struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Limits          string `json:"limits"`
	ModelRatios     string `json:"model_ratios"`
	CompletionRatio string `json:"completion_ratio"`
	TokenQuota      int64  `json:"token_quota"`
	Enabled         bool   `json:"enabled"`
}

// UpdatePlanRequest is the request DTO for updating a plan.
type UpdatePlanRequest struct {
	Name            *string `json:"name,omitempty"`
	Type            *string `json:"type,omitempty"`
	Limits          *string `json:"limits,omitempty"`
	ModelRatios     *string `json:"model_ratios,omitempty"`
	CompletionRatio *string `json:"completion_ratio,omitempty"`
	TokenQuota      *int64  `json:"token_quota,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

// --- User DTOs ---

// UpdateUserRequest is the request DTO for updating a user.
type UpdateUserRequest struct {
	Status      *string `json:"status,omitempty"`
	Balance     *int64  `json:"balance,omitempty"`
	NewPassword *string `json:"new_password,omitempty"`
}

// --- Dashboard DTO ---

// DashboardDTO is the response DTO for the dashboard.
type DashboardDTO struct {
	TotalRequests  int64 `json:"total_requests"`
	TotalTokens    int64 `json:"total_tokens"`
	ActiveChannels int64 `json:"active_channels"`
	ActiveAccounts int64 `json:"active_accounts"`
}

// --- Paginated Response ---

// PaginatedResponse is a generic paginated list response.
type PaginatedResponse struct {
	Total int64       `json:"total"`
	Page  int         `json:"page"`
	Limit int         `json:"limit"`
	Items interface{} `json:"items"`
}
