package quota

import "testing"

func TestConvertCodexUsagePlusPrimaryLimitReachedMeansFiveHourExhausted(t *testing.T) {
	qd := convertCodexUsage(map[string]interface{}{
		"plan_type": "plus",
		"rate_limit": map[string]interface{}{
			"allowed":       false,
			"limit_reached": true,
			"primary_window": map[string]interface{}{
				"used_percent":         0.0,
				"limit_window_seconds": 5 * 60 * 60,
				"reset_after_seconds":  120.0,
			},
			"secondary_window": map[string]interface{}{
				"used_percent":         20.0,
				"limit_window_seconds": 7 * 24 * 60 * 60,
			},
		},
	})

	if len(qd.Buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2: %#v", len(qd.Buckets), qd.Buckets)
	}
	if qd.Buckets[0].Label != "Codex 5小时窗口" {
		t.Fatalf("primary label = %q, want Codex 5小时窗口", qd.Buckets[0].Label)
	}
	if qd.Buckets[0].RemainingPercent != 0 {
		t.Fatalf("primary remaining = %d, want 0", qd.Buckets[0].RemainingPercent)
	}
	if qd.Buckets[0].UsedPercent == nil || *qd.Buckets[0].UsedPercent != 100 {
		t.Fatalf("primary used = %#v, want 100", qd.Buckets[0].UsedPercent)
	}
	if qd.Buckets[1].Label != "Codex 每周窗口" {
		t.Fatalf("secondary label = %q, want Codex 每周窗口", qd.Buckets[1].Label)
	}
	if qd.Buckets[1].RemainingPercent != 80 {
		t.Fatalf("secondary remaining = %d, want 80", qd.Buckets[1].RemainingPercent)
	}
}

func TestConvertCodexUsagePlusAllowedFalseWithPrimaryOnePercent(t *testing.T) {
	qd := convertCodexUsage(map[string]interface{}{
		"plan_type": "plus",
		"rate_limit": map[string]interface{}{
			"allowed":       false,
			"limit_reached": true,
			"primary_window": map[string]interface{}{
				"used_percent":         1.0,
				"limit_window_seconds": 5 * 60 * 60,
				"reset_after_seconds":  18000.0,
			},
			"secondary_window": map[string]interface{}{
				"used_percent":         100.0,
				"limit_window_seconds": 7 * 24 * 60 * 60,
			},
		},
	})

	if len(qd.Buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2: %#v", len(qd.Buckets), qd.Buckets)
	}
	if qd.Buckets[0].RemainingPercent != 0 {
		t.Fatalf("primary remaining = %d, want 0", qd.Buckets[0].RemainingPercent)
	}
	if qd.Buckets[0].UsedPercent == nil || *qd.Buckets[0].UsedPercent != 100 {
		t.Fatalf("primary used = %#v, want 100", qd.Buckets[0].UsedPercent)
	}
	if qd.Buckets[1].RemainingPercent != 0 {
		t.Fatalf("secondary remaining = %d, want 0", qd.Buckets[1].RemainingPercent)
	}
}

func TestConvertCodexUsageWeeklyExhaustedAlsoForcesPlusPrimary(t *testing.T) {
	qd := convertCodexUsage(map[string]interface{}{
		"plan_type": "plus",
		"rate_limit": map[string]interface{}{
			"allowed":       false,
			"limit_reached": true,
			"primary_window": map[string]interface{}{
				"used_percent":         0.0,
				"limit_window_seconds": 5 * 60 * 60,
			},
			"secondary_window": map[string]interface{}{
				"used_percent":         100.0,
				"limit_window_seconds": 7 * 24 * 60 * 60,
			},
		},
	})

	if len(qd.Buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2: %#v", len(qd.Buckets), qd.Buckets)
	}
	if qd.Buckets[0].RemainingPercent != 0 {
		t.Fatalf("primary remaining = %d, want 0", qd.Buckets[0].RemainingPercent)
	}
	if qd.Buckets[1].RemainingPercent != 0 {
		t.Fatalf("secondary remaining = %d, want 0", qd.Buckets[1].RemainingPercent)
	}
}

func TestConvertCodexUsageSetsFetchedAtForDebugDump(t *testing.T) {
	qd := convertCodexUsage(map[string]interface{}{})
	if qd.FetchedAt.IsZero() {
		t.Fatal("FetchedAt is zero")
	}
}

func TestConvertCodexUsageDoesNotForceFreeMonthlyPrimaryOnLimitReached(t *testing.T) {
	qd := convertCodexUsage(map[string]interface{}{
		"plan_type": "free",
		"rate_limit": map[string]interface{}{
			"allowed":       false,
			"limit_reached": true,
			"primary_window": map[string]interface{}{
				"used_percent":         0.0,
				"limit_window_seconds": 30 * 24 * 60 * 60,
			},
		},
	})

	if len(qd.Buckets) != 1 {
		t.Fatalf("bucket count = %d, want 1: %#v", len(qd.Buckets), qd.Buckets)
	}
	if qd.Buckets[0].Label != "Codex 每月窗口" {
		t.Fatalf("primary label = %q, want Codex 每月窗口", qd.Buckets[0].Label)
	}
	if qd.Buckets[0].RemainingPercent != 100 {
		t.Fatalf("free primary remaining = %d, want 100", qd.Buckets[0].RemainingPercent)
	}
}

func TestConvertCodexUsageUsedPercentIsPercentNotFraction(t *testing.T) {
	qd := convertCodexUsage(map[string]interface{}{
		"plan_type": "plus",
		"rate_limit": map[string]interface{}{
			"primary_window": map[string]interface{}{
				"used_percent":         0.6,
				"limit_window_seconds": 5 * 60 * 60,
			},
		},
	})

	if len(qd.Buckets) != 1 {
		t.Fatalf("bucket count = %d, want 1: %#v", len(qd.Buckets), qd.Buckets)
	}
	if qd.Buckets[0].RemainingPercent != 100 {
		t.Fatalf("remaining = %d, want 100 after integer rounding of 0.6%% used", qd.Buckets[0].RemainingPercent)
	}
	if qd.Buckets[0].UsedPercent == nil || *qd.Buckets[0].UsedPercent != 0 {
		t.Fatalf("used = %#v, want 0 after integer rounding of 0.6%% used", qd.Buckets[0].UsedPercent)
	}
}
