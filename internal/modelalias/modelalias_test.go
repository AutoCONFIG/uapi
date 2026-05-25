package modelalias

import "testing"

func TestPublicListHidesAliasedUpstreamName(t *testing.T) {
	got := PublicList("gemini-pro-agent,gemini-3-flash", "gemini-pro-agent=gemini-3.1-pro")
	want := []string{"gemini-3.1-pro", "gemini-3-flash"}
	if len(got) != len(want) {
		t.Fatalf("PublicList len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PublicList[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

func TestSupportsAndUpstreamNameUsePublicAlias(t *testing.T) {
	aliases := "gemini-pro-agent=gemini-3.1-pro"
	if !Supports("gemini-3.1-pro", "gemini-pro-agent", aliases) {
		t.Fatal("Supports rejected public alias")
	}
	if Supports("gemini-pro-agent", "gemini-pro-agent", aliases) {
		t.Fatal("Supports allowed hidden upstream name")
	}
	if got := UpstreamName("gemini-3.1-pro", aliases); got != "gemini-pro-agent" {
		t.Fatalf("UpstreamName = %q, want gemini-pro-agent", got)
	}
}
