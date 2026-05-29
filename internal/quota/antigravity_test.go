package quota

import "testing"

func TestConvertAntigravityModelsAggregatesKnownTiersAndKeepsUnknown(t *testing.T) {
	qd := convertAntigravityModels([]modelEntry{
		{Name: "gemini-pro-agent", RemainingFraction: 0.9, ResetTime: "2026-05-29T01:00:00Z"},
		{Name: "gemini-3.1-pro-low", RemainingFraction: 0.4, ResetTime: "2026-05-29T00:30:00Z"},
		{Name: "new-experimental-model", RemainingFraction: 0.7, ResetTime: "2026-05-29T02:00:00Z"},
	}, nil)

	if len(qd.Buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2: %#v", len(qd.Buckets), qd.Buckets)
	}
	if qd.Buckets[0].Label != "Gemini 3.1 Pro" {
		t.Fatalf("first label = %q, want Gemini 3.1 Pro", qd.Buckets[0].Label)
	}
	if qd.Buckets[0].RemainingPercent != 40 {
		t.Fatalf("aggregated percent = %d, want 40", qd.Buckets[0].RemainingPercent)
	}
	if qd.Buckets[0].ResetTime != "2026-05-29T00:30:00Z" {
		t.Fatalf("aggregated reset = %q, want earliest reset", qd.Buckets[0].ResetTime)
	}
	if qd.Buckets[1].Label != "new-experimental-model" {
		t.Fatalf("unknown label = %q, want new-experimental-model", qd.Buckets[1].Label)
	}
}
