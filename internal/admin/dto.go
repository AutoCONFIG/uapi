package admin

import (
	"time"

	"github.com/google/uuid"
)

// --- Channel DTOs ---

// ChannelDTO is the response DTO for channels.
type ChannelDTO struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Endpoint    string    `json:"endpoint"`
	Enabled     bool      `json:"enabled"`
	Models      string    `json:"models"`
	Priority    int       `json:"priority"`
	APIFormat   string    `json:"api_format"`
	ForceStream bool      `json:"force_stream"`
	AffinityTTL int       `json:"affinity_ttl"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

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

// --- Account DTOs ---

// AccountDTO is the response DTO for accounts (excludes sensitive credentials).
type AccountDTO struct {
	ID          uuid.UUID  `json:"id"`
	ChannelID   uuid.UUID  `json:"channel_id"`
	Name        string     `json:"name"`
	CredType    string     `json:"cred_type"`
	Weight      int        `json:"weight"`
	Enabled     bool       `json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

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
	Weight        int        `json:"weight"`
	Enabled       bool       `json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until"`
}

// --- Token DTOs ---

// TokenDTO is the response DTO for tokens.
type TokenDTO struct {
	ID          uuid.UUID `json:"id"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	Key         string    `json:"key"`
	Enabled     bool      `json:"enabled"`
	IPWhitelist string    `json:"ip_whitelist"`
	Unlimited   bool      `json:"unlimited"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateTokenRequest is the request DTO for creating a token.
type CreateTokenRequest struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Enabled     bool   `json:"enabled"`
	IPWhitelist string `json:"ip_whitelist"`
	Unlimited   bool   `json:"unlimited"`
}

// UpdateTokenRequest is the request DTO for updating a token.
type UpdateTokenRequest struct {
	Name        *string `json:"name,omitempty"`
	Key         *string `json:"key,omitempty"`
	IPWhitelist *string `json:"ip_whitelist,omitempty"`
	Unlimited   *bool   `json:"unlimited,omitempty"`
}

// --- Plan DTOs ---

// PlanDTO is the response DTO for plans.
type PlanDTO struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	Limits          string    `json:"limits"`
	ModelRatios     string    `json:"model_ratios"`
	CompletionRatio string    `json:"completion_ratio"`
	TokenQuota      int64     `json:"token_quota"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

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

// UserDTO is the response DTO for users (excludes password hash).
type UserDTO struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Username  string    `json:"username"`
	Status    string    `json:"status"`
	Balance   int64     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UpdateUserRequest is the request DTO for updating a user.
type UpdateUserRequest struct {
	Status  *string `json:"status,omitempty"`
	Balance *int64  `json:"balance,omitempty"`
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
