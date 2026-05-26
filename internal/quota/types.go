package quota

import "time"

type QuotaData struct {
	Buckets   []QuotaBucket `json:"buckets"`
	Credits   *CreditsInfo  `json:"credits,omitempty"`
	Tier      string        `json:"tier,omitempty"`
	FetchedAt time.Time     `json:"fetched_at"`
}

type QuotaBucket struct {
	Label            string `json:"label"`
	RemainingPercent int    `json:"remaining_percent"`
	ResetTime        string `json:"reset_time,omitempty"`
}

type CreditsInfo struct {
	Balance   string `json:"balance,omitempty"`
	Unlimited bool   `json:"unlimited"`
	Label     string `json:"label,omitempty"`
}
