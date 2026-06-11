package chatgptreverse

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
	"github.com/valyala/fasthttp"
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

func TestFromIRDoesNotReuseCachedConversationID(t *testing.T) {
	adaptor := &Adaptor{}
	adaptor.Init(
		&db.Channel{Type: "openai", APIFormat: "chatgpt_reverse"},
		&db.Account{Metadata: map[string]interface{}{
			"last_conversation_id":        "stale-conversation",
			"last_conversation_timestamp": "2099-01-01T00:00:00Z",
		}},
	)

	body, err := adaptor.FromIR(&ir.Request{
		Model: "gpt-5.5",
		Turns: []ir.Turn{{
			Role:  ir.RoleUser,
			Items: []ir.Item{{Kind: ir.ItemText, Text: &ir.Text{Text: "hello"}}},
		}},
	})
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	if strings.Contains(string(body), `"conversation_id"`) || strings.Contains(string(body), "stale-conversation") {
		t.Fatalf("reverse payload should not reuse cached conversation_id:\n%s", string(body))
	}
}

func TestFromIRUsesExplicitConversationID(t *testing.T) {
	adaptor := &Adaptor{}
	adaptor.Init(
		&db.Channel{Type: "openai", APIFormat: "chatgpt_reverse"},
		&db.Account{Metadata: map[string]interface{}{
			"last_conversation_id":        "stale-account-conversation",
			"last_conversation_timestamp": "2099-01-01T00:00:00Z",
		}},
	)

	body, err := adaptor.FromIR(&ir.Request{
		Model: "gpt-5.5",
		Metadata: map[string]json.RawMessage{
			"conversation_id": json.RawMessage(`"explicit-conversation"`),
		},
		Turns: []ir.Turn{{
			Role:  ir.RoleUser,
			Items: []ir.Item{{Kind: ir.ItemText, Text: &ir.Text{Text: "continue"}}},
		}},
	})
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"conversation_id":"explicit-conversation"`) {
		t.Fatalf("reverse payload should include explicit conversation_id:\n%s", text)
	}
	if strings.Contains(text, "stale-account-conversation") {
		t.Fatalf("reverse payload should not use account cached conversation_id:\n%s", text)
	}
}

func TestFromIRUsesScopedCachedConversationID(t *testing.T) {
	adaptor := &Adaptor{}
	adaptor.Init(
		&db.Channel{Type: "openai", APIFormat: "chatgpt_reverse"},
		&db.Account{Metadata: map[string]interface{}{
			"last_conversation_id": "unscoped-stale-conversation",
			"chatgpt_reverse_conversations": map[string]interface{}{
				"body:prompt_cache_key:thread-1": map[string]interface{}{
					"conversation_id": "scoped-conversation",
					"updated_at":      "2099-01-01T00:00:00Z",
				},
			},
		}},
	)

	body, err := adaptor.FromIR(&ir.Request{
		Model: "gpt-5.5",
		Metadata: map[string]json.RawMessage{
			"prompt_cache_key": json.RawMessage(`"thread-1"`),
		},
		Turns: []ir.Turn{{
			Role:  ir.RoleUser,
			Items: []ir.Item{{Kind: ir.ItemText, Text: &ir.Text{Text: "continue"}}},
		}},
	})
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `"conversation_id":"scoped-conversation"`) {
		t.Fatalf("reverse payload should include scoped cached conversation_id:\n%s", text)
	}
	if strings.Contains(text, "unscoped-stale-conversation") {
		t.Fatalf("reverse payload should not use unscoped last_conversation_id:\n%s", text)
	}
}

func TestConvertRequestWithAdaptorPreservesReverseConversationMetadata(t *testing.T) {
	adaptor := &Adaptor{}
	adaptor.Init(&db.Channel{Type: "openai", APIFormat: "chatgpt_reverse"}, &db.Account{})

	body, err := provider.ConvertRequestWithAdaptor(
		provider.FormatOpenAIChatCompletions,
		provider.FormatChatGPTReverse,
		[]byte(`{"model":"gpt-5.5","conversation_id":"explicit-conversation","messages":[{"role":"user","content":"continue"}]}`),
		adaptor,
	)
	if err != nil {
		t.Fatalf("ConvertRequestWithAdaptor: %v", err)
	}
	if !strings.Contains(string(body), `"conversation_id":"explicit-conversation"`) {
		t.Fatalf("converted reverse payload should preserve explicit conversation_id:\n%s", string(body))
	}
}

func TestFromIRUploadsFileAndUsesFastConversationPayload(t *testing.T) {
	const token = "test-token"
	var registered bool
	var uploaded bool
	var confirmed bool
	var prepared bool
	var finalConversation bool
	var uploadURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html data-build="test-build"></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"requirements-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/files":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode register body: %v", err)
			}
			if body["use_case"] != "my_files" || body["mime_type"] != "application/pdf" || body["store_in_library"] != true {
				t.Fatalf("unexpected file register body: %#v", body)
			}
			registered = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file_id":"file_pdf","library_file_id":"libfile_pdf","upload_url":"` + uploadURL + `"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			uploaded = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/files/file_pdf/uploaded":
			confirmed = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation/prepare":
			prepared = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-test"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation":
			if got := r.Header.Get("X-Conduit-Token"); got != "conduit-test" {
				t.Fatalf("final X-Conduit-Token = %q", got)
			}
			if got := r.Header.Get("X-OpenAI-Target-Path"); got != "/backend-api/f/conversation" {
				t.Fatalf("final X-OpenAI-Target-Path = %q", got)
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode final body: %v", err)
			}
			if body["client_prepare_state"] != "sent" {
				t.Fatalf("final client_prepare_state = %#v", body["client_prepare_state"])
			}
			finalConversation = true
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	uploadURL = server.URL + "/upload"

	adaptor := &Adaptor{}
	adaptor.Init(
		&db.Channel{Type: "openai", APIFormat: "chatgpt_reverse", Endpoint: server.URL},
		&db.Account{Metadata: map[string]interface{}{
			"last_conversation_id":        "stale-conversation",
			"last_conversation_timestamp": "2099-01-01T00:00:00Z",
		}},
	)
	adaptor.SetCredentials(token)

	pdfData := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n"))
	body, err := adaptor.FromIR(&ir.Request{
		Model: "auto",
		Turns: []ir.Turn{{
			Role: ir.RoleUser,
			Items: []ir.Item{
				{Kind: ir.ItemText, Text: &ir.Text{Text: "summarize"}},
				{Kind: ir.ItemFile, File: &ir.File{DataURI: "data:application/pdf;base64," + pdfData, Name: "paper.pdf", MimeType: "application/pdf"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("FromIR: %v", err)
	}
	if !registered || !uploaded || !confirmed || !prepared {
		t.Fatalf("flow registered=%v uploaded=%v confirmed=%v prepared=%v", registered, uploaded, confirmed, prepared)
	}
	text := string(body)
	for _, want := range []string{
		`"client_prepare_state":"sent"`,
		`"content_type":"text"`,
		`"library_file_id":"libfile_pdf"`,
		`"mime_type":"application/pdf"`,
		`"source":"local"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("payload missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"content_type":"file_attachment"`) {
		t.Fatalf("file payload should not include file_attachment content part:\n%s", text)
	}
	if strings.Contains(text, `"conversation_id"`) || strings.Contains(text, "stale-conversation") {
		t.Fatalf("file payload should not reuse cached conversation_id:\n%s", text)
	}

	var upReq fasthttp.Request
	upReq.SetRequestURI(server.URL + "/backend-api/conversation")
	upReq.Header.SetMethod("POST")
	if err := adaptor.SetupRequestHeader(&upReq, token); err != nil {
		t.Fatalf("SetupRequestHeader: %v", err)
	}
	if got := string(upReq.URI().Path()); got != "/backend-api/f/conversation" {
		t.Fatalf("request path = %q", got)
	}
	if got := string(upReq.Header.Peek("X-Conduit-Token")); got != "conduit-test" {
		t.Fatalf("X-Conduit-Token = %q", got)
	}
	upReq.SetBody(body)
	var upResp fasthttp.Response
	if err := adaptor.DoHTTPRequest(&upReq, &upResp); err != nil {
		t.Fatalf("DoHTTPRequest: %v", err)
	}
	if upResp.StatusCode() != http.StatusOK {
		t.Fatalf("final status = %d", upResp.StatusCode())
	}
	if !finalConversation {
		t.Fatalf("final /backend-api/f/conversation was not called")
	}
}

func TestChatGPTReverseDebugDumpWritesRedactedHTTPLog(t *testing.T) {
	const token = "test-token"
	dumpDir := t.TempDir()
	t.Setenv("UAPI_RELAY_DEBUG_DUMP_DIR", dumpDir)
	var uploadURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			_, _ = w.Write([]byte(`<html data-build="test-build"></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"requirements-secret"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/files":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"file_id":"file_pdf","library_file_id":"libfile_pdf","upload_url":"` + uploadURL + `?sig=secret"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/files/file_pdf/uploaded":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation/prepare":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-secret"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	uploadURL = server.URL + "/upload"

	adaptor := &Adaptor{}
	adaptor.Init(&db.Channel{Type: "openai", APIFormat: "chatgpt_reverse", Endpoint: server.URL}, &db.Account{})
	adaptor.SetCredentials(token)
	pdfData := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n"))
	if _, err := adaptor.FromIR(&ir.Request{
		Model: "auto",
		Turns: []ir.Turn{{
			Role: ir.RoleUser,
			Items: []ir.Item{
				{Kind: ir.ItemText, Text: &ir.Text{Text: "summarize"}},
				{Kind: ir.ItemFile, File: &ir.File{DataURI: "data:application/pdf;base64," + pdfData, Name: "paper.pdf", MimeType: "application/pdf"}},
			},
		}},
	}); err != nil {
		t.Fatalf("FromIR: %v", err)
	}

	var httpLog string
	var summaryPath string
	err := filepath.WalkDir(dumpDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == "summary.json" {
			summaryPath = path
		}
		if d.Name() == "http.jsonl" {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			httpLog = string(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dump dir: %v", err)
	}
	if httpLog == "" {
		t.Fatalf("http.jsonl was not written under %s", dumpDir)
	}
	if summaryPath == "" {
		t.Fatalf("summary.json was not written under %s", dumpDir)
	}
	dumpEntryDir := filepath.Base(filepath.Dir(summaryPath))
	if !strings.Contains(dumpEntryDir, "-chatgpt_reverse-") {
		t.Fatalf("dump entry dir = %q, want flat chatgpt_reverse suffix marker", dumpEntryDir)
	}
	if filepath.Base(filepath.Dir(filepath.Dir(summaryPath))) == "chatgpt_reverse" {
		t.Fatalf("dump should be flat under day dir, got nested path %s", summaryPath)
	}
	for _, forbidden := range []string{"Bearer test-token", "requirements-secret", "conduit-secret", "sig=secret", "%PDF-1.4"} {
		if strings.Contains(httpLog, forbidden) {
			t.Fatalf("debug log leaked %q:\n%s", forbidden, httpLog)
		}
	}
	for _, want := range []string{`"path":"/backend-api/files"`, `"path":"/backend-api/f/conversation/prepare"`, `"sha256"`} {
		if !strings.Contains(httpLog, want) {
			t.Fatalf("debug log missing %s:\n%s", want, httpLog)
		}
	}
}
