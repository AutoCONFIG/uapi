package gateway

import (
	"reflect"
	"testing"
)

func TestPublicModelsForAntigravityUsesTierGroupPublicModel(t *testing.T) {
	settings := `{
		"thinking_routing": true,
		"tier_groups": [{
			"public_model": "gemini-smart",
			"high": "gemini-3.1-pro-high",
			"medium": "gemini-3.1-pro",
			"low": "gemini-3.1-pro-low",
			"aliases": ["gemini-3-pro-high"]
		}]
	}`
	got := publicModelsForChannel("gemini-3.1-pro-high,gemini-3.1-pro-low,claude-sonnet-4-6", "", "antigravity", settings)
	want := []string{"gemini-smart"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publicModelsForChannel() = %#v, want %#v", got, want)
	}
}

func TestPublicModelsForAntigravityIgnoresGenericAliases(t *testing.T) {
	settings := `{
		"thinking_routing": true,
		"tier_groups": [{
			"public_model": "gemini-smart",
			"high": "gemini-3.1-pro-high",
			"low": "gemini-3.1-pro-low"
		}]
	}`
	got := publicModelsForChannel("gemini-public,gpt-oss-120b-medium", "gemini-public=gemini-3.1-pro-high", "antigravity", settings)
	want := []string{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publicModelsForChannel() = %#v, want %#v", got, want)
	}
}

func TestPublicModelsForAntigravityUsesOnlyTierGroups(t *testing.T) {
	settings := `{"tier_groups":[{"public_model":"gemini-smart","high":"gemini-3.1-pro-high","low":"gemini-3.1-pro-low"}]}`
	got := publicModelsForChannel("claude-sonnet-4-6,gemini-3.1-pro-high", "claude-sonnet-4-6=claude-sonnet-4-6", "antigravity", settings)
	want := []string{"gemini-smart"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publicModelsForChannel() = %#v, want %#v", got, want)
	}
}

func TestChannelSupportsGatewayModelUsesPublicAntigravityList(t *testing.T) {
	settings := `{"tier_groups":[{"public_model":"gemini-smart","high":"gemini-3.1-pro-high","low":"gemini-3.1-pro-low"}]}`
	if !channelSupportsGatewayModel("gemini-smart", "antigravity", "gemini-3.1-pro-high,gemini-3.1-pro-low", "", settings) {
		t.Fatal("direct tier model was not supported")
	}
	if !channelSupportsGatewayModel("gemini-3.1-pro-high", "antigravity", "gemini-3.1-pro-high,gemini-3.1-pro-low", "", settings) {
		t.Fatal("direct original model was not supported")
	}
}
