package quota

import "time"

type QuotaData struct {
	Buckets         []QuotaBucket `json:"buckets"`
	Credits         *CreditsInfo  `json:"credits,omitempty"`
	Tier            string        `json:"tier,omitempty"`
	IsForbidden     bool          `json:"is_forbidden,omitempty"`
	ForbiddenReason string        `json:"forbidden_reason,omitempty"`
	FetchedAt       time.Time     `json:"fetched_at"`
}

type QuotaBucket struct {
	Label            string `json:"label"`
	RemainingPercent int    `json:"remaining_percent"`
	UsedPercent      *int   `json:"used_percent,omitempty"`
	ResetTime        string `json:"reset_time,omitempty"`
}

type CreditsInfo struct {
	Balance   string `json:"balance,omitempty"`
	Unlimited bool   `json:"unlimited"`
	Label     string `json:"label,omitempty"`
}
