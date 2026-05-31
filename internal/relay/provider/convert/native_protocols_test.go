package convert_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func TestNativeProtocolIdentityDoesNotCollapseToBaseProtocols(t *testing.T) {
	tests := []struct {
		name     string
		format   convert.Format
		body     []byte
		protocol ir.Protocol
		role     ir.Role
	}{
		{
			name:     "codex",
			format:   convert.FormatCodexResponses,
			body:     []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"hi"}]}`),
			protocol: ir.ProtocolCodex,
			role:     ir.RoleUser,
		},
		{
			name:     "claude code",
			format:   convert.FormatClaudeCode,
			body:     []byte(`{"model":"claude-sonnet-4-5","max_tokens":100,"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"}]}`),
			protocol: ir.ProtocolClaudeCode,
			role:     ir.RoleUser,
		},
		{
			name:     "gemini code",
			format:   convert.FormatGeminiCode,
			body:     []byte(`{"model":"gemini-2.5-pro","project":"p","request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`),
			protocol: ir.ProtocolGeminiCode,
			role:     ir.RoleUser,
		},
		{
			name:     "antigravity",
			format:   convert.FormatAntigravity,
			body:     []byte(`{"model":"gpt-oss-120b","userAgent":"antigravity","requestType":"generateContent","request":{"contents":[{"role":"model","parts":[{"text":"hi"}]}]}}`),
			protocol: ir.ProtocolAntigravity,
			role:     ir.RoleModel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convert.ToIR(tt.format, tt.body)
			if err != nil {
				t.Fatalf("ToIR: %v", err)
			}
			if got.SourceProtocol != tt.protocol || got.Native.Protocol != tt.protocol {
				t.Fatalf("protocol collapsed: source=%q native=%q want %q", got.SourceProtocol, got.Native.Protocol, tt.protocol)
			}
			if len(got.Turns) == 0 || got.Turns[0].Role != tt.role {
				t.Fatalf("role = %#v, want %q", got.Turns, tt.role)
			}
		})
	}
}

func TestDetailedConversionExposesLossRecordsForAdaptorPath(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"previous_response_id":"resp_1",
		"prompt_cache_key":"cache",
		"custom_source_only":true
	}`)

	converted, audit, err := provider.ConvertRequestDetailed(provider.FormatCodexResponses, provider.FormatAntigravity, body)
	if err != nil {
		t.Fatalf("ConvertRequestDetailed: %v", err)
	}
	if !json.Valid(converted) {
		t.Fatalf("converted body is not JSON: %s", converted)
	}
	if audit == nil || audit.TargetProtocol != ir.ProtocolAntigravity {
		t.Fatalf("audit target protocol = %#v", audit)
	}
	if len(audit.Losses) < 3 {
		t.Fatalf("loss records missing: %#v", audit.Losses)
	}
	for _, loss := range audit.Losses {
		if loss.SourceProtocol != ir.ProtocolCodex || loss.TargetProtocol != ir.ProtocolAntigravity {
			t.Fatalf("loss protocol audit not closed: %#v", audit.Losses)
		}
	}
	for _, want := range []string{"previous_response_id", "prompt_cache_key", "custom_source_only"} {
		if !hasLossField(audit.Losses, want) {
			t.Fatalf("loss field %q missing: %#v", want, audit.Losses)
		}
		if strings.Contains(string(converted), want) {
			t.Fatalf("source-only field %q leaked into target body: %s", want, converted)
		}
	}
}

func TestCodexSameProtocolPreservesOpaqueResponsesInputItem(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":[
			{"type":"file_search_call","id":"fs_1","status":"completed","queries":["q"],"results":[{"file_id":"f_1"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
		],
		"include":["reasoning.encrypted_content"]
	}`)

	converted, audit, err := convert.ConvertRequestDetailed(convert.FormatCodexResponses, convert.FormatCodexResponses, body)
	if err != nil {
		t.Fatalf("ConvertRequestDetailed: %v", err)
	}
	if audit.SourceProtocol != ir.ProtocolCodex || audit.TargetProtocol != ir.ProtocolCodex {
		t.Fatalf("protocols = %q -> %q", audit.SourceProtocol, audit.TargetProtocol)
	}
	if len(audit.Losses) != 1 || audit.Losses[0].Field != "file_search_call" || !audit.Losses[0].Preserved {
		t.Fatalf("opaque item loss not recorded/preserved: %#v", audit.Losses)
	}
	if audit.Losses[0].SourceProtocol != ir.ProtocolCodex || audit.Losses[0].TargetProtocol != ir.ProtocolCodex {
		t.Fatalf("opaque item loss protocol audit not closed: %#v", audit.Losses[0])
	}
	for _, want := range []string{`"type":"file_search_call"`, `"include":["reasoning.encrypted_content"]`} {
		if !strings.Contains(string(converted), want) {
			t.Fatalf("same-protocol Codex conversion dropped %s:\n%s", want, converted)
		}
	}
}

func hasLossField(losses []ir.Loss, field string) bool {
	for _, loss := range losses {
		if loss.Field == field && loss.ValueHash != "" && loss.Preserved {
			return true
		}
	}
	return false
}
