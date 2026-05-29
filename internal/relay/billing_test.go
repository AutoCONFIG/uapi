package relay

import "testing"

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
