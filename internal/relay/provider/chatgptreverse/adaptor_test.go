package chatgptreverse

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func TestFromIRUploadsImageAndBuildsMultimodalPayload(t *testing.T) {
	const token = "test-token"
	var registered bool
	var uploaded bool
	var confirmed bool
	var uploadURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/files":
			if got := r.Header.Get("Authorization"); got != "Bearer "+token {
				t.Fatalf("Authorization = %q", got)
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode register body: %v", err)
			}
			if body["use_case"] != "multimodal" || body["file_name"] != "image.png" {
				t.Fatalf("unexpected register body: %#v", body)
			}
			registered = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file_id":"file_test","upload_url":"` + uploadURL + `"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			if got := r.Header.Get("x-ms-blob-type"); got != "BlockBlob" {
				t.Fatalf("x-ms-blob-type = %q", got)
			}
			uploaded = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/files/file_test/uploaded":
			confirmed = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	uploadURL = server.URL + "/upload"

	adaptor := &Adaptor{}
	adaptor.Init(&db.Channel{Type: "openai", APIFormat: "chatgpt_reverse", Endpoint: server.URL}, &db.Account{})
	adaptor.SetCredentials(token)

	pngData := mustBase64Decode(t, "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	body, err := adaptor.FromIR(&ir.Request{
		Model: "gpt-5.5",
		Turns: []ir.Turn{{
			Role: ir.RoleUser,
			Items: []ir.Item{
				{Kind: ir.ItemText, Text: &ir.Text{Text: "describe"}},
				{Kind: ir.ItemImage, Image: &ir.Image{DataURI: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	if !registered || !uploaded || !confirmed {
		t.Fatalf("upload flow registered=%v uploaded=%v confirmed=%v", registered, uploaded, confirmed)
	}
	text := string(body)
	for _, want := range []string{
		`"content_type":"multimodal_text"`,
		`"asset_pointer":"file-service://file_test"`,
		`"attachments":[{"`,
		`"mimeType":"image/png"`,
		`"describe"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("payload missing %s:\n%s", want, text)
		}
	}
}

func mustBase64Decode(t *testing.T, value string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return data
}
