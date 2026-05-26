package admin

import (
	"time"

	"github.com/google/uuid"
)

// --- Channel DTOs ---

// CreateChannelRequest is the request DTO for creating a channel.
type CreateChannelRequest struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Group        string `json:"group"`
	Models       string `json:"models"`
	ModelAliases string `json:"model_aliases"`
	Priority     int    `json:"priority"`
	APIFormat    string `json:"api_format"`
	ForceStream  bool   `json:"force_stream"`
	AffinityTTL  int    `json:"affinity_ttl"`
}

// UpdateChannelRequest is the request DTO for updating a channel.
type UpdateChannelRequest struct {
	Name         *string `json:"name,omitempty"`
	Type         *string `json:"type,omitempty"`
	Group        *string `json:"group,omitempty"`
	Models       *string `json:"models,omitempty"`
	ModelAliases *string `json:"model_aliases,omitempty"`
	Priority     *int    `json:"priority,omitempty"`
	APIFormat    *string `json:"api_format,omitempty"`
	ForceStream  *bool   `json:"force_stream,omitempty"`
	AffinityTTL  *int    `json:"affinity_ttl,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
}

// StartOAuthRequest asks the backend to create a provider authorization URL.
type StartOAuthRequest struct {
	ChannelID     uuid.UUID `json:"channel_id"`
	Provider      string    `json:"provider"`
	AccountName   string    `json:"account_name"`
	ClientID      string    `json:"client_id"`
	ClientSecret  string    `json:"client_secret"`
	TokenURL      string    `json:"token_url"`
	Mode          string    `json:"mode"`
	AdminUsername string    `json:"-"` // Set by middleware, not from request body
}

// OAuthAuthURLResponse is returned after creating an OAuth onboarding session.
type OAuthAuthURLResponse struct {
	AuthURL     string    `json:"auth_url"`
	State       string    `json:"state"`
	RedirectURI string    `json:"redirect_uri"`
	Mode        string    `json:"mode"`
	UserCode    string    `json:"user_code,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// CompleteOAuthRequest completes a manual OAuth flow from a copied callback URL.
type CompleteOAuthRequest struct {
	State       string `json:"state"`
	CallbackURL string `json:"callback_url"`
	Code        string `json:"code"`
	OAuthJSON   string `json:"oauth_json"`
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
	Endpoint    string    `json:"endpoint"`
	Weight      int       `json:"weight"`
	Enabled     bool      `json:"enabled"`
}

// UpdateAccountRequest is the request DTO for updating an account.
type UpdateAccountRequest struct {
	ChannelID     uuid.UUID  `json:"channel_id"`
	Name          string     `json:"name"`
	Credentials   string     `json:"credentials"`
	Endpoint      *string    `json:"endpoint,omitempty"`
	Weight        *int       `json:"weight"`
	Enabled       *bool      `json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until"`
}

// --- Access Policy DTOs ---

type CreateAccessPolicyRequest struct {
	AllowedModels  string `json:"allowed_models"`
	MaxConcurrency int    `json:"max_concurrency"`
	HourlyLimit    int    `json:"hourly_limit"`
	WeeklyLimit    int    `json:"weekly_limit"`
	MonthlyLimit   int    `json:"monthly_limit"`
	Enabled        *bool  `json:"enabled"`
}

type UpdateAccessPolicyRequest struct {
	AllowedModels  *string `json:"allowed_models,omitempty"`
	MaxConcurrency *int    `json:"max_concurrency,omitempty"`
	HourlyLimit    *int    `json:"hourly_limit,omitempty"`
	WeeklyLimit    *int    `json:"weekly_limit,omitempty"`
	MonthlyLimit   *int    `json:"monthly_limit,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
}

// --- Plan DTOs ---

// CreatePlanRequest is the request DTO for creating a plan.
type CreatePlanRequest struct {
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	PolicyID        *uuid.UUID `json:"policy_id"`
	ModelRatios     string     `json:"model_ratios"`
	CompletionRatio string     `json:"completion_ratio"`
	TokenQuota      int64      `json:"token_quota"`
	Enabled         bool       `json:"enabled"`
	DurationDays    int        `json:"duration_days"`
}

// UpdatePlanRequest is the request DTO for updating a plan.
type UpdatePlanRequest struct {
	Name            *string    `json:"name,omitempty"`
	Type            *string    `json:"type,omitempty"`
	PolicyID        *uuid.UUID `json:"policy_id,omitempty"`
	ModelRatios     *string    `json:"model_ratios,omitempty"`
	CompletionRatio *string    `json:"completion_ratio,omitempty"`
	TokenQuota      *int64     `json:"token_quota,omitempty"`
	Enabled         *bool      `json:"enabled,omitempty"`
	DurationDays    *int       `json:"duration_days,omitempty"`
}

// --- User DTOs ---

// UpdateUserRequest is the request DTO for updating a user.
type UpdateUserRequest struct {
	Status        *string    `json:"status,omitempty"`
	NewPassword   *string    `json:"new_password,omitempty"`
	PlanID        *uuid.UUID `json:"plan_id,omitempty"`
	PlanStartsAt  *time.Time `json:"plan_starts_at,omitempty"`
	PlanExpiresAt *time.Time `json:"plan_expires_at,omitempty"`
}

// --- Relay Node DTOs ---

type CreateRelayNodeRequest struct {
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	Region         string `json:"region"`
	EgressIP       string `json:"egress_ip"`
	Weight         int    `json:"weight"`
	MaxConcurrency int    `json:"max_concurrency"`
	Status         string `json:"status"`
	HealthStatus   string `json:"health_status"`
}

type UpdateRelayNodeRequest struct {
	Name           *string `json:"name,omitempty"`
	BaseURL        *string `json:"base_url,omitempty"`
	Region         *string `json:"region,omitempty"`
	EgressIP       *string `json:"egress_ip,omitempty"`
	Weight         *int    `json:"weight,omitempty"`
	MaxConcurrency *int    `json:"max_concurrency,omitempty"`
	Status         *string `json:"status,omitempty"`
	HealthStatus   *string `json:"health_status,omitempty"`
}

// --- Node Account Binding DTOs ---

type CreateNodeChannelRequest struct {
	RelayNodeID uuid.UUID `json:"relay_node_id"`
	ChannelID   uuid.UUID `json:"channel_id"`
	Weight      int       `json:"weight"`
	Enabled     *bool     `json:"enabled"`
}

type UpdateNodeChannelRequest struct {
	RelayNodeID *uuid.UUID `json:"relay_node_id,omitempty"`
	ChannelID   *uuid.UUID `json:"channel_id,omitempty"`
	Weight      *int       `json:"weight,omitempty"`
	Enabled     *bool      `json:"enabled,omitempty"`
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
