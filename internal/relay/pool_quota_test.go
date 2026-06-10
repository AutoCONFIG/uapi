package relay

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
)

func TestAccountPoolSkipsCodexWhenAnyQuotaWindowExhausted(t *testing.T) {
	exhausted := &db.Account{
		Base:    db.Base{ID: uuid.New()},
		Name:    "codex-exhausted",
		Weight:  10,
		Enabled: true,
		Metadata: map[string]interface{}{
			"oauth_provider":      "codex",
			"chatgpt_account_id":  "acct-exhausted",
			"chatgpt_plan_type":   "plus",
			"chatgpt_account_key": "kept-for-debug",
			"quota": map[string]interface{}{
				"buckets": []interface{}{
					map[string]interface{}{"label": "Codex 5小时窗口", "remaining_percent": 90.0},
					map[string]interface{}{"label": "Codex 每周窗口", "remaining_percent": 0.0},
				},
			},
		},
	}
	available := &db.Account{
		Base:    db.Base{ID: uuid.New()},
		Name:    "codex-available",
		Weight:  1,
		Enabled: true,
		Metadata: map[string]interface{}{
			"oauth_provider":     "codex",
			"chatgpt_account_id": "acct-available",
			"chatgpt_plan_type":  "plus",
			"quota": map[string]interface{}{
				"buckets": []interface{}{
					map[string]interface{}{"label": "Codex 5小时窗口", "remaining_percent": 90.0},
					map[string]interface{}{"label": "Codex 每周窗口", "remaining_percent": 80.0},
				},
			},
		},
	}

	pool := NewAccountPool([]*db.Account{exhausted, available})
	for i := 0; i < 5; i++ {
		got, ok := pool.PickForModel("gpt-5.1-codex", nil)
		if !ok {
			t.Fatal("PickForModel returned no account")
		}
		if got.ID != available.ID {
			t.Fatalf("PickForModel picked %s, want available account", got.Name)
		}
	}
}

func TestAccountPoolRejectsAffinityCodexWhenAnyQuotaWindowExhausted(t *testing.T) {
	acc := &db.Account{
		Base:    db.Base{ID: uuid.New()},
		Name:    "codex-exhausted",
		Weight:  1,
		Enabled: true,
		Metadata: map[string]interface{}{
			"auth_mode":          "chatgpt",
			"chatgpt_account_id": "acct-exhausted",
			"chatgpt_plan_type":  "plus",
			"quota": map[string]interface{}{
				"buckets": []interface{}{
					map[string]interface{}{"label": "Codex 5小时窗口", "remaining_percent": 0.0},
					map[string]interface{}{"label": "Codex 每周窗口", "remaining_percent": 80.0},
				},
			},
		},
	}

	pool := NewAccountPool([]*db.Account{acc})
	if _, ok := pool.PickByIDForModel(acc.ID.String(), "gpt-5.1-codex"); ok {
		t.Fatal("PickByIDForModel returned exhausted codex account")
	}
	debugItems := pool.QuotaSkipDebugForAccount(acc.ID.String())
	if len(debugItems) != 1 {
		t.Fatalf("QuotaSkipDebugForAccount count = %d, want 1", len(debugItems))
	}
	if debugItems[0]["reason"] != "codex_quota_exhausted" {
		t.Fatalf("reason = %v", debugItems[0]["reason"])
	}
	if debugItems[0]["chatgpt_account_id"] != "acct-exhausted" {
		t.Fatalf("chatgpt_account_id = %v", debugItems[0]["chatgpt_account_id"])
	}
	buckets, ok := debugItems[0]["exhausted_buckets"].([]map[string]interface{})
	if !ok || len(buckets) != 1 {
		t.Fatalf("exhausted_buckets = %#v", debugItems[0]["exhausted_buckets"])
	}
	if buckets[0]["label"] != "Codex 5小时窗口" || buckets[0]["remaining_percent"] != 0 {
		t.Fatalf("bucket = %#v", buckets[0])
	}
}

func TestAccountPoolDoesNotTreatNonCodexWindowQuotaAsGlobal(t *testing.T) {
	acc := &db.Account{
		Base:    db.Base{ID: uuid.New()},
		Name:    "gemini",
		Weight:  1,
		Enabled: true,
		Metadata: map[string]interface{}{
			"oauth_provider": "gemini",
			"quota": map[string]interface{}{
				"buckets": []interface{}{
					map[string]interface{}{"label": "Gemini Flash", "remaining_percent": 0.0},
					map[string]interface{}{"label": "Gemini Pro", "remaining_percent": 80.0},
				},
			},
		},
	}

	pool := NewAccountPool([]*db.Account{acc})
	if _, ok := pool.PickForModel("gemini-pro", nil); !ok {
		t.Fatal("PickForModel should keep non-codex model-specific quota behavior")
	}
	if got := pool.QuotaSkipDebugForModel("gemini-pro", nil); len(got) != 0 {
		t.Fatalf("QuotaSkipDebugForModel = %#v, want empty for non-codex", got)
	}
}
