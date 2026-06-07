package admin

import (
	"encoding/json"
	"testing"
)

func TestGoogleAccountEmailFromMetadata(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]interface{}
		want string
	}{
		{
			name: "direct email",
			meta: map[string]interface{}{"email": " user@example.com "},
			want: "user@example.com",
		},
		{
			name: "code assist manage subscription email",
			meta: map[string]interface{}{
				"load_code_assist": map[string]interface{}{
					"manageSubscriptionUri": "https://accounts.google.com/AccountChooser?Email=gemini%40example.com&continue=https%3A%2F%2Fone.google.com%2Fsettings",
				},
			},
			want: "gemini@example.com",
		},
		{
			name: "missing",
			meta: map[string]interface{}{"project_id": "project-1"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.meta)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}
			if got := googleAccountEmailFromMetadata(raw); got != tt.want {
				t.Fatalf("googleAccountEmailFromMetadata() = %q, want %q", got, tt.want)
			}
		})
	}
}
