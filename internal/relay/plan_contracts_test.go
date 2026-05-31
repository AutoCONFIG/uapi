package relay_test

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

func readRepoFilesUnder(t *testing.T, parts ...string) map[string]string {
	t.Helper()
	root := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

func TestBillingRequiresTokenPlanForSettlement(t *testing.T) {
	if err := relay.RefundAndSettleTxForPlan(nil, uuid.NewString(), uuid.Nil, 100, 80, 20, 0, 0, "gpt-test"); !errors.Is(err, relay.ErrNoActiveSubscription) {
		t.Fatalf("settle without token plan error = %v, want ErrNoActiveSubscription", err)
	}
	if err := relay.RefundTxForPlan(nil, uuid.NewString(), uuid.Nil, 100); !errors.Is(err, relay.ErrNoActiveSubscription) {
		t.Fatalf("refund without token plan error = %v, want ErrNoActiveSubscription", err)
	}
}

func TestProtocolConversionHasNoLegacyDraftOrAdapterTypes(t *testing.T) {
	files := readRepoFilesUnder(t, "internal", "relay", "provider", "convert")
	forbidden := []string{
		"InternalRequest",
		"InternalMessage",
		"InternalResponse",
		"adapterRequest",
		"adapterResponse",
		"requestDraft",
		"responseDraft",
		"requestTurnDraft",
		"requestItemDraft",
		"responseChoiceDraft",
	}
	for path, content := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		for _, symbol := range forbidden {
			if strings.Contains(content, symbol) {
				t.Fatalf("legacy protocol conversion symbol %q survived in %s", symbol, path)
			}
		}
	}
}

func TestSameProtocolProductionPathUsesRawBodyNormalization(t *testing.T) {
	for _, target := range []struct {
		name string
		path []string
	}{
		{name: "http relay", path: []string{"internal", "relay", "handler.go"}},
		{name: "ws bridge", path: []string{"internal", "relay", "ws_bridge.go"}},
	} {
		content := readRepoFile(t, target.path...)
		if !strings.Contains(content, "NormalizeRequestSameProtocol") {
			t.Fatalf("%s same-protocol path must use NormalizeRequestSameProtocol", target.name)
		}
		if strings.Contains(content, "ConvertRequest(clientFormat, upstreamFormat") {
			t.Fatalf("%s same-protocol path must not use generic ConvertRequest", target.name)
		}
	}

	registry := readRepoFile(t, "internal", "relay", "provider", "convert", "registry.go")
	start := strings.Index(registry, "func NormalizeRequestSameProtocol")
	if start < 0 {
		t.Fatal("NormalizeRequestSameProtocol missing")
	}
	end := strings.Index(registry[start:], "\n}\n")
	if end < 0 {
		t.Fatal("NormalizeRequestSameProtocol body parse failed")
	}
	body := registry[start : start+end]
	for _, forbidden := range []string{"requestIREmitters", "ConvertRequestDetailed", "PrepareRequestForTarget"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("NormalizeRequestSameProtocol must validate raw body without emitter rebuild; found %q", forbidden)
		}
	}
}

func TestProviderConversionDoesNotInjectSyntheticThoughtSignature(t *testing.T) {
	files := readRepoFilesUnder(t, "internal", "relay", "provider")
	for path, content := range files {
		if strings.Contains(content, "skip_thought_signature_validator") {
			t.Fatalf("provider conversion must not inject synthetic thought signatures: %s", path)
		}
	}
}

func TestZeroQuotaIsNotTreatedAsUnlimited(t *testing.T) {
	billing := readRepoFile(t, "internal", "relay", "billing.go")
	forbidden := []string{
		"CountQuota",
		"TokenQuota > 0",
		"TokenQuota",
		"plan.TokenQuota > 0",
		"count_quota",
		"token_quota <= 0",
		"used_quota",
		"used_tokens",
		"no plan = unlimited",
		"no rate limit",
	}
	for _, pattern := range forbidden {
		if strings.Contains(billing, pattern) {
			t.Fatalf("billing code still contains unlimited zero-quota pattern %q", pattern)
		}
	}
	if !strings.Contains(billing, "next > window.limit") {
		t.Fatal("billing must deny usage above the policy window, including zero window quota")
	}
	planModel := readRepoFile(t, "internal", "db", "plan.go")
	for _, pattern := range []string{"CountQuota", "TokenQuota", "UsedTokens", "count_quota", "token_quota", "used_tokens"} {
		if strings.Contains(planModel, pattern) {
			t.Fatalf("plan schema still contains redundant quota field %q", pattern)
		}
	}
}

func TestSubscriptionsAreUserScopedNotKeyScoped(t *testing.T) {
	planModel := readRepoFile(t, "internal", "db", "plan.go")
	if strings.Contains(planModel, "TokenID") || strings.Contains(planModel, `json:"token_id"`) {
		t.Fatal("TokenPlan must be user-scoped, not API-key-scoped")
	}
	if !strings.Contains(planModel, "UserID") || !strings.Contains(planModel, `json:"user_id"`) {
		t.Fatal("TokenPlan must expose user_id as the package owner")
	}

	policyModel := readRepoFile(t, "internal", "db", "access_policy.go")
	if strings.Contains(policyModel, "TokenID") || strings.Contains(policyModel, `json:"token_id"`) {
		t.Fatal("policy usage windows must be user-scoped, not API-key-scoped")
	}
	if !strings.Contains(policyModel, "UserID") || !strings.Contains(policyModel, `json:"user_id"`) {
		t.Fatal("policy usage windows must expose user_id as the usage owner")
	}
}

func TestUserRoutesExposePublicPlanCatalogButNoSelfSubscribe(t *testing.T) {
	server := readRepoFile(t, "internal", "server", "server.go")
	for _, route := range []string{`"/api/user/subscription/:planID"`} {
		if strings.Contains(server, route) {
			t.Fatalf("user self-service plan route must not be registered: %s", route)
		}
	}
	for _, route := range []string{`"/api/user/subscription"`, `"/api/user/plans"`, `"/api/user/redeem"`} {
		if !strings.Contains(server, route) {
			t.Fatalf("expected user route to be registered: %s", route)
		}
	}
}

func TestInitialSchemaHasNoObsoleteRedundantFields(t *testing.T) {
	for _, target := range []struct {
		name string
		path []string
	}{
		{name: "database init", path: []string{"internal", "db", "db.go"}},
		{name: "token model", path: []string{"internal", "db", "token.go"}},
		{name: "redeem code model", path: []string{"internal", "db", "redeem_code.go"}},
	} {
		content := readRepoFile(t, target.path...)
		for _, pattern := range []string{
			"DROP COLUMN",
			"DROP TABLE",
			"obsolete",
			"balance",
			"Unlimited",
			"unlimited",
			"Value",
			`json:"value"`,
			"window_usage",
			"window_reset_at",
		} {
			if strings.Contains(content, pattern) {
				t.Fatalf("%s still contains obsolete or redundant field %q", target.name, pattern)
			}
		}
	}
}
