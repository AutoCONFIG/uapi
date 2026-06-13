package helperclient

type LoginResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  int64  `json:"access_expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
}

type PublicSettings struct {
	PublicBaseURL string `json:"public_base_url"`
}

type Profile struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Status   string `json:"status"`
}

type APIKey struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

type Subscription struct {
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

type Summary struct {
	ServerURL     string
	PublicBaseURL string
	Profile       Profile
	Subscription  Subscription
	Keys          []APIKey
}

func (s Summary) DefaultKey() (APIKey, bool) {
	for _, key := range s.Keys {
		if key.Enabled && key.Key != "" {
			return key, true
		}
	}
	return APIKey{}, false
}
