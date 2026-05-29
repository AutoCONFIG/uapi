package admin

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
)

func TestOAuthIdleMaintainerScheduleRetryStopsAfterTwoAttempts(t *testing.T) {
	m := &OAuthIdleMaintainer{
		timers:      make(map[uuid.UUID]*time.Timer),
		retryCounts: make(map[uuid.UUID]int),
	}
	accountID := uuid.New()

	m.ScheduleRetry(accountID)
	if got := m.retryCounts[accountID]; got != 1 {
		t.Fatalf("retry count after first retry = %d, want 1", got)
	}
	if timer := m.timers[accountID]; timer == nil {
		t.Fatalf("expected first retry timer")
	} else {
		timer.Stop()
	}

	m.ScheduleRetry(accountID)
	if got := m.retryCounts[accountID]; got != 2 {
		t.Fatalf("retry count after second retry = %d, want 2", got)
	}
	if timer := m.timers[accountID]; timer == nil {
		t.Fatalf("expected second retry timer")
	} else {
		timer.Stop()
	}

	m.ScheduleRetry(accountID)
	if _, ok := m.retryCounts[accountID]; ok {
		t.Fatalf("retry count should be cleared after max retries")
	}
}

func TestIdleRefreshAfterUsesFinalFifteenMinuteWindow(t *testing.T) {
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	for i := 0; i < 64; i++ {
		account := &db.Account{Base: db.Base{ID: uuid.New()}, TokenExpiry: &expiry}
		refreshAt := idleRefreshAfter(account)
		if refreshAt.After(expiry) {
			t.Fatalf("refresh time %s is after expiry %s", refreshAt, expiry)
		}
		if refreshAt.Before(expiry.Add(-15 * time.Minute)) {
			t.Fatalf("refresh time %s is before final 15 minute window ending at %s", refreshAt, expiry)
		}
	}
}

func TestRandomOAuthRetryDelayUsesFifteenMinuteWindow(t *testing.T) {
	for i := 0; i < 64; i++ {
		delay := randomOAuthRetryDelay(i % 2)
		if delay < 0 || delay > 15*time.Minute {
			t.Fatalf("retry delay %s outside 0-15 minute window", delay)
		}
	}
}
