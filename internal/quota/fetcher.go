package quota

import "github.com/google/uuid"

type Fetcher interface {
	FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error)
}

var registry = map[string]Fetcher{}

func Register(apiFormat string, f Fetcher) {
	registry[apiFormat] = f
}

func Get(apiFormat string) (Fetcher, bool) {
	f, ok := registry[apiFormat]
	return f, ok
}

// AccountRef identifies an account for batch refresh.
type AccountRef struct {
	AccountID uuid.UUID
	ChannelID uuid.UUID
	APIFormat string
}
