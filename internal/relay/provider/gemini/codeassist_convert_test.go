package gemini

import (
	"encoding/json"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func TestEmitGeminiRequestCodeAssistAddsOfficialEnvelopeFields(t *testing.T) {
	req := &ir.Request{
		Model: "pro",
		Turns: []ir.Turn{{
			Role: ir.RoleUser,
			Items: []ir.Item{{
				Kind: ir.ItemText,
				Text: &ir.Text{Text: "hello"},
			}},
		}},
	}
	account := &db.Account{Metadata: map[string]interface{}{"project_id": "project-1"}}

	converted, err := internalToGeminiCodeAssistWithAccount(req, account)
	if err != nil {
		t.Fatalf("internalToGeminiCodeAssistWithAccount: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("unmarshal converted body: %v\n%s", err, converted)
	}
	if got["model"] != "gemini-2.5-pro" {
		t.Fatalf("model = %#v, want gemini-2.5-pro; body=%s", got["model"], converted)
	}
	if got["project"] != "project-1" {
		t.Fatalf("project = %#v, want project-1; body=%s", got["project"], converted)
	}
	if promptID, ok := got["user_prompt_id"].(string); !ok || promptID == "" {
		t.Fatalf("user_prompt_id missing: %s", converted)
	}
	request, ok := got["request"].(map[string]interface{})
	if !ok {
		t.Fatalf("request missing: %s", converted)
	}
	if sessionID, ok := request["session_id"].(string); !ok || sessionID == "" {
		t.Fatalf("request.session_id missing: %s", converted)
	}
	if _, ok := request["sessionId"]; ok {
		t.Fatalf("Gemini Code Assist inner request should use session_id, not sessionId: %s", converted)
	}
}
