package admin

import (
	"crypto/rand"
	"math/big"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/appsettings"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CleanupOldLogs deletes logs older than retentionDays.
func CleanupOldLogs(database *gorm.DB, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	return database.Where("created_at < ?", cutoff).Delete(&db.Log{}).Error
}

// StartLogCleanup runs periodic log cleanup.
func CleanupOldRedeemCodes(database *gorm.DB, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	return database.Where("status = ? AND updated_at < ?", "used", cutoff).Delete(&db.RedeemCode{}).Error
}

func StartLogCleanup(database *gorm.DB) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			retentionDays := appsettings.GetInt(database, appsettings.LogRetentionDays, 180)
			if err := CleanupOldLogs(database, retentionDays); err != nil {
				logger.Warnf("admin.scheduler", "log cleanup failed", logger.Err(err))
			}
			redeemRetentionDays := appsettings.GetInt(database, appsettings.RedeemCodeRetentionDays, 180)
			if err := CleanupOldRedeemCodes(database, redeemRetentionDays); err != nil {
				logger.Warnf("admin.scheduler", "redeem cleanup failed", logger.Err(err))
			}
		}
	}()
}

// OAuthIdleMaintainer keeps idle OAuth accounts fresh with one timer per account.
// Accounts refresh at a random point in the final five minutes before expiry; if
// a request refreshes first, the relayer asks the maintainer to reschedule from
// the new expiry. Transient failures get a small number of randomized retries.
type OAuthIdleMaintainer struct {
	db          *gorm.DB
	refreshPool func(channelID string)

	mu          sync.Mutex
	timers      map[uuid.UUID]*time.Timer
	retryCounts map[uuid.UUID]int
	stopped     bool
}

const (
	oauthRefreshWindow = 5 * time.Minute
)

// StartOAuthIdleMaintenance restores timers for existing OAuth accounts.
func StartOAuthIdleMaintenance(database *gorm.DB, refreshPool func(channelID string)) *OAuthIdleMaintainer {
	m := &OAuthIdleMaintainer{
		db:          database,
		refreshPool: refreshPool,
		timers:      make(map[uuid.UUID]*time.Timer),
		retryCounts: make(map[uuid.UUID]int),
	}
	m.restore()
	return m
}

func (m *OAuthIdleMaintainer) restore() {
	var accounts []db.Account
	err := m.db.
		Where("cred_type = ? AND enabled = true AND deleted_at IS NULL AND token_expiry IS NOT NULL", "oauth_token").
		Order("token_expiry ASC").
		Find(&accounts).Error
	if err != nil {
		logger.Warnf("admin.scheduler", "oauth idle maintenance restore failed", logger.Err(err))
		return
	}
	for i := range accounts {
		m.ScheduleAccount(&accounts[i])
	}
}

// ScheduleAccount schedules or cancels idle maintenance for a single account.
// Call this after creating or updating OAuth accounts.
func (m *OAuthIdleMaintainer) ScheduleAccount(account *db.Account) {
	if m == nil || account == nil {
		return
	}
	if account.CredType != "oauth_token" || !account.Enabled || account.DeletedAt != nil || account.TokenExpiry == nil {
		m.CancelAccount(account.ID)
		return
	}
	delay := time.Until(idleRefreshAfter(account))
	if delay < 0 {
		delay = 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	if timer, ok := m.timers[account.ID]; ok {
		timer.Stop()
	}
	accountID := account.ID
	delete(m.retryCounts, accountID)
	m.timers[accountID] = time.AfterFunc(delay, func() {
		m.runAccount(accountID)
	})
}

// ScheduleAccountID reloads an account before scheduling. Use it after updates
// when only the id is known.
func (m *OAuthIdleMaintainer) ScheduleAccountID(accountID uuid.UUID) {
	if m == nil {
		return
	}
	var account db.Account
	if err := m.db.Where("id = ? AND deleted_at IS NULL", accountID).First(&account).Error; err != nil {
		m.CancelAccount(accountID)
		return
	}
	m.ScheduleAccount(&account)
}

// CancelAccount removes a pending maintenance timer.
func (m *OAuthIdleMaintainer) CancelAccount(accountID uuid.UUID) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if timer, ok := m.timers[accountID]; ok {
		timer.Stop()
		delete(m.timers, accountID)
	}
	delete(m.retryCounts, accountID)
}

// Stop cancels all maintenance timers.
func (m *OAuthIdleMaintainer) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	for accountID, timer := range m.timers {
		timer.Stop()
		delete(m.timers, accountID)
	}
	for accountID := range m.retryCounts {
		delete(m.retryCounts, accountID)
	}
}

func (m *OAuthIdleMaintainer) runAccount(accountID uuid.UUID) {
	m.mu.Lock()
	delete(m.timers, accountID)
	m.mu.Unlock()

	var account db.Account
	if err := m.db.Where("id = ? AND deleted_at IS NULL", accountID).First(&account).Error; err != nil {
		return
	}
	if account.CredType != "oauth_token" || !account.Enabled || account.TokenExpiry == nil {
		return
	}
	if time.Now().Before(idleRefreshWindowStart(&account)) {
		m.ScheduleAccount(&account)
		return
	}
	var channel db.Channel
	if err := m.db.Where("id = ? AND deleted_at IS NULL", account.ChannelID).First(&channel).Error; err != nil {
		logger.Warnf("admin.scheduler", "oauth idle maintenance channel lookup failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		m.ScheduleRetry(account.ID)
		return
	}
	if _, err := relay.RefreshOAuthCredentialsForChannel(&account, &channel, m.db); err != nil {
		logger.Warnf("admin.scheduler", "oauth idle maintenance failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		m.ScheduleRetry(account.ID)
		return
	}
	m.resetRetry(account.ID)
	if m.refreshPool != nil {
		m.refreshPool(account.ChannelID.String())
	}
	m.ScheduleAccountID(account.ID)
}

// ScheduleRetry retries failed maintenance at most twice with randomized delay.
func (m *OAuthIdleMaintainer) ScheduleRetry(accountID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	attempt := m.retryCounts[accountID]
	if attempt >= 2 {
		delete(m.retryCounts, accountID)
		return
	}
	m.retryCounts[accountID] = attempt + 1
	if timer, ok := m.timers[accountID]; ok {
		timer.Stop()
	}
	delay := randomOAuthRetryDelay(attempt)
	m.timers[accountID] = time.AfterFunc(delay, func() {
		m.runAccount(accountID)
	})
}

func (m *OAuthIdleMaintainer) resetRetry(accountID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.retryCounts, accountID)
}

func idleRefreshAfter(account *db.Account) time.Time {
	return account.TokenExpiry.Add(-randomIdleRefreshLead())
}

func idleRefreshWindowStart(account *db.Account) time.Time {
	return account.TokenExpiry.Add(-oauthRefreshWindow)
}

func randomIdleRefreshLead() time.Duration {
	maxSeconds := int64(oauthRefreshWindow / time.Second)
	n, err := rand.Int(rand.Reader, big.NewInt(maxSeconds+1))
	if err != nil {
		fallback := int64(time.Now().Nanosecond()) % (maxSeconds + 1)
		return time.Duration(fallback) * time.Second
	}
	return time.Duration(n.Int64()) * time.Second
}

func randomOAuthRetryDelay(attempt int) time.Duration {
	maxSeconds := int64(oauthRefreshWindow / time.Second)
	n, err := rand.Int(rand.Reader, big.NewInt(maxSeconds+1))
	if err != nil {
		fallback := int64(time.Now().Nanosecond()) % (maxSeconds + 1)
		return time.Duration(fallback) * time.Second
	}
	return time.Duration(n.Int64()) * time.Second
}

// InitPools loads all channels and their accounts into the pool manager at startup.
func InitPools(database *gorm.DB, setPool func(channelID string, accounts []*db.Account)) error {
	var channels []db.Channel
	if err := database.Where("enabled = true AND deleted_at IS NULL").Find(&channels).Error; err != nil {
		return err
	}
	for _, ch := range channels {
		var accounts []*db.Account
		database.Where("channel_id = ? AND enabled = true AND deleted_at IS NULL", ch.ID).Find(&accounts)
		setPool(ch.ID.String(), accounts)
	}
	return nil
}
