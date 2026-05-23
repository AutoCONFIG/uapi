package admin

import (
	"encoding/binary"
	"sync"
	"time"

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
func StartLogCleanup(database *gorm.DB, retentionDays int) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := CleanupOldLogs(database, retentionDays); err != nil {
				logger.Warnf("admin.scheduler", "log cleanup failed", logger.Err(err))
			}
		}
	}()
}

// OAuthIdleMaintainer keeps idle Claude Code/Gemini Code OAuth accounts from
// expiring before their next user request. It is expiry-driven: each account gets one
// timer derived from token_expiry instead of a periodic table scan. Codex is
// intentionally excluded because Codex has its own upstream-aligned on-use
// proactive refresh rule.
type OAuthIdleMaintainer struct {
	db          *gorm.DB
	refreshPool func(channelID string)

	mu      sync.Mutex
	timers  map[uuid.UUID]*time.Timer
	stopped bool
}

// StartOAuthIdleMaintenance restores timers for existing OAuth accounts.
func StartOAuthIdleMaintenance(database *gorm.DB, refreshPool func(channelID string)) *OAuthIdleMaintainer {
	m := &OAuthIdleMaintainer{
		db:          database,
		refreshPool: refreshPool,
		timers:      make(map[uuid.UUID]*time.Timer),
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
	if account.CredType != "oauth_token" || !account.Enabled || account.DeletedAt != nil || account.TokenExpiry == nil || !relay.IsIdleRefreshProvider(account.TokenURL) {
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
}

func (m *OAuthIdleMaintainer) runAccount(accountID uuid.UUID) {
	m.mu.Lock()
	delete(m.timers, accountID)
	m.mu.Unlock()

	var account db.Account
	if err := m.db.Where("id = ? AND deleted_at IS NULL", accountID).First(&account).Error; err != nil {
		return
	}
	if account.CredType != "oauth_token" || !account.Enabled || account.TokenExpiry == nil || !relay.IsIdleRefreshProvider(account.TokenURL) {
		return
	}
	if time.Now().Before(idleRefreshAfter(&account)) {
		m.ScheduleAccount(&account)
		return
	}
	if _, err := relay.RefreshOAuthCredentials(&account, m.db); err != nil {
		logger.Warnf("admin.scheduler", "oauth idle maintenance failed", logger.F("account_id", account.ID.String()), logger.Err(err))
		m.ScheduleRetry(account.ID, account.TokenExpiry)
		return
	}
	if m.refreshPool != nil {
		m.refreshPool(account.ChannelID.String())
	}
	m.ScheduleAccountID(account.ID)
}

// ScheduleRetry retries failed maintenance conservatively without falling back
// to polling. The next successful refresh replaces this timer with one based on
// the provider's new expiry.
func (m *OAuthIdleMaintainer) ScheduleRetry(accountID uuid.UUID, expiry *time.Time) {
	if expiry == nil || time.Now().After(*expiry) {
		return
	}
	remaining := time.Until(*expiry)
	if remaining <= time.Minute {
		return
	}
	delay := 15 * time.Minute
	if remaining < delay*2 {
		delay = remaining / 2
	}
	if delay < time.Minute {
		delay = time.Minute
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	if timer, ok := m.timers[accountID]; ok {
		timer.Stop()
	}
	m.timers[accountID] = time.AfterFunc(delay, func() {
		m.runAccount(accountID)
	})
}

func idleRefreshAfter(account *db.Account) time.Time {
	// Spread accounts across the final hour before expiry. The jitter is stable
	// per account so restarts do not cluster refreshes.
	jitterMinutes := 5 + int(binary.BigEndian.Uint64(account.ID[:8])%56)
	return account.TokenExpiry.Add(-time.Duration(jitterMinutes) * time.Minute)
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
