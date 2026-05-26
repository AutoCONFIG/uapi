package quota

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	stalenessTTL = 5 * time.Minute
	batchSize    = 3
	jitterMin    = 2 * time.Second
	jitterMax    = 8 * time.Second
)

type Scheduler struct {
	db *gorm.DB
	mu sync.Map // per-channel mutex: channelID -> *sync.Mutex
}

func NewScheduler(database *gorm.DB) *Scheduler {
	return &Scheduler{db: database}
}

func (s *Scheduler) channelMu(channelID uuid.UUID) *sync.Mutex {
	mu, _ := s.mu.LoadOrStore(channelID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// RefreshAccount refreshes a single account. Returns the quota data or error.
// This is used for admin frontend trigger and 429 trigger.
func (s *Scheduler) RefreshAccount(accountID uuid.UUID) (*QuotaData, error) {
	var acc db.Account
	if err := s.db.First(&acc, "id = ?", accountID).Error; err != nil {
		return nil, err
	}
	var ch db.Channel
	if err := s.db.First(&ch, "id = ?", acc.ChannelID).Error; err != nil {
		return nil, err
	}
	return s.refreshOne(acc, ch)
}

// On429 is called when upstream returns 429. Runs refresh in background.
func (s *Scheduler) On429(accountID, channelID uuid.UUID) {
	go func() {
		_, _ = s.RefreshAccount(accountID)
	}()
}

// RefreshChannel refreshes all OAuth accounts under a channel in small batches with jitter.
func (s *Scheduler) RefreshChannel(channelID uuid.UUID) ([]*QuotaData, []error) {
	mu := s.channelMu(channelID)
	mu.Lock()
	defer mu.Unlock()

	var accounts []db.Account
	if err := s.db.Where("channel_id = ? AND cred_type = ?", channelID, "oauth_token").Find(&accounts).Error; err != nil {
		return nil, []error{err}
	}

	// Filter stale accounts
	var stale []db.Account
	cutoff := time.Now().Add(-stalenessTTL)
	for _, acc := range accounts {
		if !s.isStale(acc, cutoff) {
			continue
		}
		stale = append(stale, acc)
	}

	var ch db.Channel
	if err := s.db.First(&ch, "id = ?", channelID).Error; err != nil {
		return nil, []error{err}
	}

	var results []*QuotaData
	var errs []error
	for i := 0; i < len(stale); i += batchSize {
		end := min(i+batchSize, len(stale))
		batch := stale[i:end]

		var wg sync.WaitGroup
		batchResults := make([]*QuotaData, len(batch))
		batchErrs := make([]error, len(batch))
		for j, acc := range batch {
			wg.Add(1)
			go func(idx int, a db.Account) {
				defer wg.Done()
				q, err := s.refreshOne(a, ch)
				batchResults[idx] = q
				batchErrs[idx] = err
			}(j, acc)
		}
		wg.Wait()

		for j, q := range batchResults {
			if batchErrs[j] != nil {
				errs = append(errs, batchErrs[j])
			} else if q != nil {
				results = append(results, q)
			}
		}

		// Jitter between batches (skip after last batch)
		if end < len(stale) {
			d := jitterMin + time.Duration(rand.Int64N(int64(jitterMax-jitterMin)))
			time.Sleep(d)
		}
	}
	return results, errs
}

func (s *Scheduler) isStale(acc db.Account, cutoff time.Time) bool {
	meta := acc.Metadata
	if meta == nil {
		return true
	}
	quota, ok := meta["quota"].(map[string]interface{})
	if !ok {
		return true
	}
	fetchedAt, ok := quota["fetched_at"].(string)
	if !ok {
		return true
	}
	t, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		return true
	}
	return t.Before(cutoff)
}

func (s *Scheduler) refreshOne(acc db.Account, ch db.Channel) (*QuotaData, error) {
	fetcher, ok := Get(ch.APIFormat)
	if !ok {
		return nil, nil // no fetcher for this format, skip silently
	}

	credential, err := crypto.Decrypt(acc.Credentials)
	if err != nil {
		return nil, err
	}

	accessToken := credential

	qd, err := fetcher.FetchQuota(accessToken, acc.Metadata)
	if err != nil {
		return nil, err
	}
	if qd == nil {
		return nil, nil
	}

	qd.FetchedAt = time.Now().UTC()

	// Write quota into metadata
	if acc.Metadata == nil {
		acc.Metadata = map[string]interface{}{}
	}
	acc.Metadata["quota"] = qd
	if err := s.db.Model(&db.Account{}).Where("id = ?", acc.ID).Update("metadata", acc.Metadata).Error; err != nil {
		return nil, err
	}
	return qd, nil
}
