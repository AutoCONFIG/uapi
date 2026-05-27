package quota

import (
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	batchSize = 3
	jitterMin = 2 * time.Second
	jitterMax = 8 * time.Second
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
	if err := s.db.Where("channel_id = ? AND cred_type = ? AND deleted_at IS NULL", channelID, "oauth_token").Find(&accounts).Error; err != nil {
		return nil, []error{err}
	}

	var ch db.Channel
	if err := s.db.First(&ch, "id = ?", channelID).Error; err != nil {
		return nil, []error{err}
	}

	var results []*QuotaData
	var errs []error
	for i := 0; i < len(accounts); i += batchSize {
		end := min(i+batchSize, len(accounts))
		batch := accounts[i:end]

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
		if end < len(accounts) {
			d := jitterMin + time.Duration(rand.Int64N(int64(jitterMax-jitterMin)))
			time.Sleep(d)
		}
	}
	return results, errs
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
	if token, err := s.quotaAccessToken(&acc, ch, credential); err == nil && token != "" {
		accessToken = token
	} else if err != nil {
		logger.Warnf("quota.token", "failed to refresh oauth access token before quota fetch", logger.F("account_id", acc.ID.String()), logger.Err(err))
	}

	qd, err := fetcher.FetchQuota(accessToken, acc.Metadata)
	if err != nil {
		// Check if error suggests token expiration (401)
		errStr := err.Error()
		is401 := strings.Contains(errStr, "status 401") || strings.Contains(errStr, " \"401\"")
		if is401 {
			// Try to refresh token using OAuth provider
			if newToken, refreshErr := s.refreshOAuthAccessToken(&acc, ch); refreshErr == nil && newToken != "" {
				// Retry with new token
				qd, err = fetcher.FetchQuota(newToken, acc.Metadata)
			}
		}
		// If still error after retry, check if it's a 403 (forbidden)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "status 403") || strings.Contains(errStr, " \"403\"") || strings.Contains(errStr, "forbidden") {
				// Mark as forbidden
				qd = &QuotaData{
					IsForbidden:     true,
					ForbiddenReason: "account_forbidden",
					FetchedAt:       time.Now().UTC(),
				}
				err = nil
			}
		}
	}
	// Also check if qd returned from retry indicates forbidden
	if qd != nil && qd.IsForbidden {
		err = nil
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

	// Check quota alert: all buckets <= 20%
	allLow := len(qd.Buckets) > 0
	for _, b := range qd.Buckets {
		if b.RemainingPercent > 20 {
			allLow = false
			break
		}
	}
	if allLow {
		acc.Metadata["quota_alert"] = map[string]interface{}{
			"level":   "warning",
			"message": "所有模型额度低于 20%",
		}
	} else {
		delete(acc.Metadata, "quota_alert")
	}

	if err := s.db.Model(&db.Account{}).Where("id = ?", acc.ID).Update("metadata", acc.Metadata).Error; err != nil {
		return nil, err
	}
	return qd, nil
}

func (s *Scheduler) quotaAccessToken(acc *db.Account, ch db.Channel, currentCredential string) (string, error) {
	if acc.CredType != "oauth_token" {
		return currentCredential, nil
	}
	if ch.APIFormat == "codex" {
		return s.refreshOAuthAccessToken(acc, ch)
	}
	if acc.TokenExpiry != nil && time.Until(*acc.TokenExpiry) < time.Minute {
		return s.refreshOAuthAccessToken(acc, ch)
	}
	return currentCredential, nil
}
