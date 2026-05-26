package user

type RegisterRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  int64  `json:"access_expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type UpdatePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type UpdateEmailRequest struct {
	Password string `json:"password"`
	Email    string `json:"email"`
}

type CreateKeyRequest struct {
	Name        string  `json:"name"`
	IPWhitelist string  `json:"ip_whitelist"`
	ExpiresAt   *string `json:"expires_at"`
	Models      string  `json:"models"`
	Permissions string  `json:"permissions"`
}

type ProfileResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type KeyResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Key         string  `json:"key"`
	Enabled     bool    `json:"enabled"`
	IPWhitelist string  `json:"ip_whitelist"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
	Models      string  `json:"models"`
	Permissions string  `json:"permissions"`
	CreatedAt   string  `json:"created_at"`
}

type UsageSummaryResponse struct {
	TotalRequests    int64             `json:"total_requests"`
	FailedRequests   int64             `json:"failed_requests"`
	SuccessRate      float64           `json:"success_rate"`
	TotalTokens      int64             `json:"total_tokens"`
	PromptTokens     int64             `json:"prompt_tokens"`
	CompletionTokens int64             `json:"completion_tokens"`
	ByModel          []UsageModelPoint `json:"by_model"`
	Daily            []UsageDailyPoint `json:"daily"`
}

type UsageModelPoint struct {
	Model       string `json:"model"`
	Requests    int64  `json:"requests"`
	TotalTokens int64  `json:"total_tokens"`
}

type UsageDailyPoint struct {
	Date        string `json:"date"`
	Requests    int64  `json:"requests"`
	TotalTokens int64  `json:"total_tokens"`
}

type UsageLogItem struct {
	ID               int64  `json:"id"`
	CreatedAt        string `json:"created_at"`
	Model            string `json:"model"`
	ClientIP         string `json:"client_ip,omitempty"`
	IsStream         bool   `json:"is_stream"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	LatencyMs        int64  `json:"latency_ms"`
	StatusCode       int    `json:"status_code"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

type UsageLogsResponse struct {
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Limit int            `json:"limit"`
	Logs  []UsageLogItem `json:"logs"`
}

type SubscriptionResponse struct {
	PlanID    string               `json:"plan_id"`
	PlanName  string               `json:"plan_name"`
	PlanType  string               `json:"plan_type"`
	Windows   []SubscriptionWindow `json:"windows"`
	StartsAt  string               `json:"starts_at"`
	ExpiresAt string               `json:"expires_at"`
	Status    string               `json:"status"`
}

type SubscriptionWindow struct {
	Type      string `json:"type"`
	Limit     int    `json:"limit"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
	ResetAt   string `json:"reset_at"`
}

type RedeemRequest struct {
	Code string `json:"code"`
}
