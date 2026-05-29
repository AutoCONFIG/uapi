package admin

import (
	"testing"
	"time"

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
