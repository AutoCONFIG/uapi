package plan_contracts_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/google/uuid"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func TestBillingRequiresTokenPlanForSettlement(t *testing.T) {
	if err := relay.RefundAndSettleTxForPlan(nil, uuid.NewString(), uuid.Nil, 100, 80, 20, 0, 0, "gpt-test"); !errors.Is(err, relay.ErrNoActiveSubscription) {
		t.Fatalf("settle without token plan error = %v, want ErrNoActiveSubscription", err)
	}
	if err := relay.RefundTxForPlan(nil, uuid.NewString(), uuid.Nil, 100); !errors.Is(err, relay.ErrNoActiveSubscription) {
		t.Fatalf("refund without token plan error = %v, want ErrNoActiveSubscription", err)
	}
}

func TestZeroQuotaIsNotTreatedAsUnlimited(t *testing.T) {
	billing := readRepoFile(t, "internal", "relay", "billing.go")
	forbidden := []string{
		"TokenQuota > 0",
		"plan.TokenQuota > 0",
		"token_quota <= 0",
		"no plan = unlimited",
		"no rate limit",
	}
	for _, pattern := range forbidden {
		if strings.Contains(billing, pattern) {
			t.Fatalf("billing code still contains unlimited zero-quota pattern %q", pattern)
		}
	}
	if !strings.Contains(billing, "tp.UsedQuota >= plan.TokenQuota") {
		t.Fatal("billing code must deny used quota greater than or equal to plan quota, including zero quota")
	}
}

func TestUserRoutesDoNotExposePlanCatalogOrSelfSubscribe(t *testing.T) {
	server := readRepoFile(t, "internal", "server", "server.go")
	for _, route := range []string{`"/api/user/plans"`, `"/api/user/subscription/:planID"`} {
		if strings.Contains(server, route) {
			t.Fatalf("user self-service plan route must not be registered: %s", route)
		}
	}
	for _, route := range []string{`"/api/user/subscription"`, `"/api/user/redeem"`} {
		if !strings.Contains(server, route) {
			t.Fatalf("expected user route to be registered: %s", route)
		}
	}
}
