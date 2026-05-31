package relay

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestApplyModelRatio(t *testing.T) {
	if got := applyModelRatio(100, "gpt-5", `{"gpt-5":2}`); got != 200 {
		t.Fatalf("applyModelRatio = %d, want 200", got)
	}
	if got := applyModelRatio(101, "gpt-5", `{"gpt-5":3}`); got != 303 {
		t.Fatalf("applyModelRatio integer = %d, want 303", got)
	}
	if got := applyModelRatio(100, "other", `{"gpt-5":2}`); got != 100 {
		t.Fatalf("unmatched ratio = %d, want 100", got)
	}
	if got := applyModelRatio(1, "gpt-5", `{"gpt-5":2}`); got != 2 {
		t.Fatalf("count ratio = %d, want 2", got)
	}
	if got := applyModelRatio(1, "gpt-5", `{"gpt-5":0}`); got != 0 {
		t.Fatalf("zero ratio = %d, want 0", got)
	}
}

func TestPreConsumeChargeForPlanAppliesModelRatioOnce(t *testing.T) {
	const ratios = `{"gpt-5":20}`
	if got := preConsumeChargeForPlan("token_based", 100, "gpt-5", ratios); got != 2000 {
		t.Fatalf("token pre-consume charge = %d, want 2000", got)
	}
	if got := preConsumeChargeForPlan("token_based", 0, "gpt-5", ratios); got != 20 {
		t.Fatalf("zero estimate token pre-consume charge = %d, want 20", got)
	}
	if got := preConsumeChargeForPlan("count_based", 0, "gpt-5", ratios); got != 20 {
		t.Fatalf("count pre-consume charge = %d, want 20", got)
	}
}

func TestSettledDeltaSubtractsRatioAdjustedPreConsume(t *testing.T) {
	const ratios = `{"gpt-5":20}`
	preConsumed := preConsumeChargeForPlan("token_based", 100, "gpt-5", ratios)
	actual := applyModelRatio(100, "gpt-5", ratios)
	if delta := actual - preConsumed; delta != 0 {
		t.Fatalf("settlement delta = %d, want 0; successful calls should keep the pre-charge when estimate equals actual", delta)
	}
}

func TestBillableTokenEquivalentChargesCacheAtReducedRate(t *testing.T) {
	got := billableTokenEquivalent(100, 20, 0, 40, 1.25, 0.1)
	if got != 84 {
		t.Fatalf("billable tokens = %d, want 84", got)
	}
}

func TestPlanAnchoredWeekWindow(t *testing.T) {
	start := time.Date(2026, 5, 30, 11, 20, 0, 0, time.UTC)
	now := start.Add(8*24*time.Hour + time.Minute)
	want := start.Add(7 * 24 * time.Hour)
	if got := fixedWeekFromPlanStart(start, now); !got.Equal(want) {
		t.Fatalf("week window start = %s, want %s", got, want)
	}
}

func TestPlanAnchoredMonthWindow(t *testing.T) {
	start := time.Date(2026, 5, 15, 11, 20, 0, 0, time.UTC)
	now := start.Add(35*24*time.Hour + time.Minute)
	want := start.Add(30 * 24 * time.Hour)
	if got := fixedMonthFromPlanStart(start, now); !got.Equal(want) {
		t.Fatalf("month window start = %s, want %s", got, want)
	}
}

func TestBillingPreConsumeBlocksExhaustedCountPlan(t *testing.T) {
	dsn := os.Getenv("UAPI_TEST_DSN")
	if dsn == "" {
		t.Skip("set UAPI_TEST_DSN to run database-backed billing quota test")
	}
	database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := database.Exec(`CREATE EXTENSION IF NOT EXISTS "pgcrypto"`).Error; err != nil {
		t.Fatalf("create pgcrypto extension: %v", err)
	}
	if err := database.AutoMigrate(db.AllModels...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	userID := uuid.New().String()
	policyID := uuid.New()
	planID := uuid.New()
	tokenID := uuid.New()
	tokenPlanID := uuid.New()
	now := time.Now().UTC()

	cleanup := func() {
		database.Unscoped().Where("policy_id = ? OR user_id = ?", policyID, userID).Delete(&db.PolicyUsageWindow{})
		database.Unscoped().Where("id = ?", tokenPlanID).Delete(&db.TokenPlan{})
		database.Unscoped().Where("id = ?", tokenID).Delete(&db.Token{})
		database.Unscoped().Where("id = ?", planID).Delete(&db.Plan{})
		database.Unscoped().Where("id = ?", policyID).Delete(&db.AccessPolicy{})
		database.Unscoped().Where("id = ?", userID).Delete(&db.User{})
	}
	t.Cleanup(cleanup)
	cleanup()

	if err := database.Create(&db.User{
		Base:         db.Base{ID: uuid.MustParse(userID)},
		Email:        "quota-" + userID + "@example.test",
		Username:     "quota-" + strings.ReplaceAll(userID, "-", ""),
		PasswordHash: "test",
		Status:       "active",
	}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := database.Create(&db.Token{
		Base:    db.Base{ID: tokenID},
		UserID:  userID,
		Name:    "quota-test",
		Key:     "sk-test-" + tokenID.String(),
		Enabled: true,
	}).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := database.Create(&db.AccessPolicy{
		Base:         db.Base{ID: policyID},
		HourlyLimit:  1,
		WeeklyLimit:  1,
		MonthlyLimit: 1,
		Enabled:      true,
	}).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := database.Create(&db.Plan{
		Base:         db.Base{ID: planID},
		Name:         "quota-test",
		Type:         "count_based",
		PolicyID:     &policyID,
		Enabled:      true,
		DurationDays: 1,
	}).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if err := database.Create(&db.TokenPlan{
		Base:      db.Base{ID: tokenPlanID},
		UserID:    userID,
		PlanID:    planID,
		StartsAt:  now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("create token plan: %v", err)
	}

	billing := NewBillingService(database)
	if _, err := billing.PreConsume(tokenID.String(), "gpt-5", 0); err != nil {
		t.Fatalf("first pre-consume should pass: %v", err)
	}
	if _, err := billing.PreConsume(tokenID.String(), "gpt-5", 0); err == nil {
		t.Fatal("second pre-consume should be blocked after quota is exhausted")
	} else if errors.Is(err, ErrNoActiveSubscription) || !strings.Contains(err.Error(), "usage limit exceeded") {
		t.Fatalf("second pre-consume error = %v, want usage limit exceeded", err)
	}

	var windows []db.PolicyUsageWindow
	if err := database.Where("policy_id = ? AND user_id = ?", policyID, userID).Find(&windows).Error; err != nil {
		t.Fatalf("query usage windows: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("usage window count = %d, want 3", len(windows))
	}
	for _, window := range windows {
		if window.UsedCount != 1 {
			t.Fatalf("%s used_count = %d, want 1", window.WindowType, window.UsedCount)
		}
	}
}
