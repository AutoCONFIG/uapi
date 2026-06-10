package relay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/anthropic"
)

func TestBuildUpstreamRelayRequestReprojectsCrossChannelProtocol(t *testing.T) {
	ch := &db.Channel{
		Type:      "anthropic",
		APIFormat: "standard",
		Endpoint:  "https://volc.example/v1",
	}
	acc := &db.Account{CredType: "api_key"}
	adaptor := &anthropic.AnthropicAdaptor{}
	clientBody := []byte(`{"model":"kimi-k2.6","messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":8}`)

	req, err := (&Relayer{}).buildUpstreamRelayRequest(ch, acc, adaptor, "/v1/chat/completions", clientBody, "kimi-k2.6", provider.FormatOpenAIChatCompletions, true, false, false, "sk-test", "token-1")
	if err != nil {
		t.Fatalf("buildUpstreamRelayRequest: %v", err)
	}
	if req.URL != "https://volc.example/v1/messages" {
		t.Fatalf("URL = %q, want Anthropic messages endpoint", req.URL)
	}
	if req.UpstreamFormat != provider.FormatAnthropic {
		t.Fatalf("UpstreamFormat = %q, want anthropic", req.UpstreamFormat)
	}
	if strings.Contains(string(req.Body), "chat/completions") {
		t.Fatalf("rebuilt body leaked old endpoint/protocol: %s", req.Body)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatalf("rebuilt body is not JSON: %v\n%s", err, req.Body)
	}
	if _, ok := payload["messages"]; !ok {
		t.Fatalf("rebuilt body missing Anthropic messages: %s", req.Body)
	}
	if payload["max_tokens"] == nil {
		t.Fatalf("rebuilt body missing Anthropic max_tokens: %s", req.Body)
	}
	if payload["stream"] != true {
		t.Fatalf("rebuilt body stream = %#v, want true", payload["stream"])
	}
}
