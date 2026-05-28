package relay

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/valyala/fasthttp"
)

func TestAntigravityImageGenerationRequestConvertsOpenAIImagesAPI(t *testing.T) {
	body := []byte(`{"model":"nano-banana-2","prompt":"draw a dashboard","n":2,"size":"1280x720","quality":"hd","response_format":"b64_json"}`)
	account := &db.Account{Metadata: map[string]interface{}{"project_id": "project-1"}}

	converted, err := antigravityImageGenerationRequest(body, account, "")
	if err != nil {
		t.Fatalf("convert image request: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("decode converted request: %v", err)
	}
	if got["model"] != "gemini-3.1-flash-image" {
		t.Fatalf("model = %#v, want gemini-3.1-flash-image; body=%s", got["model"], converted)
	}
	if got["requestType"] != "image_gen" {
		t.Fatalf("requestType = %#v, want image_gen; body=%s", got["requestType"], converted)
	}
	if got["project"] != "project-1" {
		t.Fatalf("project = %#v, want project-1; body=%s", got["project"], converted)
	}
	request := got["request"].(map[string]interface{})
	genConfig := request["generationConfig"].(map[string]interface{})
	imageConfig := genConfig["imageConfig"].(map[string]interface{})
	if imageConfig["aspectRatio"] != "16:9" {
		t.Fatalf("aspectRatio = %#v, want 16:9; body=%s", imageConfig["aspectRatio"], converted)
	}
	if imageConfig["imageSize"] != "4K" {
		t.Fatalf("imageSize = %#v, want 4K; body=%s", imageConfig["imageSize"], converted)
	}
	if genConfig["candidateCount"] != float64(2) {
		t.Fatalf("candidateCount = %#v, want 2; body=%s", genConfig["candidateCount"], converted)
	}
}

func TestAntigravityImageMultipartRequestConvertsOpenAIImageEdit(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", "nano-banana-2")
	_ = writer.WriteField("prompt", "make it blue")
	_ = writer.WriteField("n", "3")
	_ = writer.WriteField("size", "1280x720")
	part, err := writer.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte("fake-png"))
	refPart, err := writer.CreateFormFile("image_reference_1", "reference.png")
	if err != nil {
		t.Fatalf("create reference form file: %v", err)
	}
	_, _ = refPart.Write([]byte("fake-ref"))
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	var req fasthttp.Request
	var ctx fasthttp.RequestCtx
	req.Header.SetContentType(writer.FormDataContentType())
	req.SetBody(buf.Bytes())
	ctx.Init(&req, nil, nil)

	converted, err := antigravityImageMultipartRequest(&ctx, &db.Account{}, "", requestTypeImageEdit)
	if err != nil {
		t.Fatalf("convert multipart image edit: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("decode converted request: %v", err)
	}
	if got["requestType"] != "image_gen" {
		t.Fatalf("requestType = %#v, body=%s", got["requestType"], converted)
	}
	request := got["request"].(map[string]interface{})
	genConfig := request["generationConfig"].(map[string]interface{})
	if genConfig["candidateCount"] != float64(3) {
		t.Fatalf("candidateCount = %#v, want 3; body=%s", genConfig["candidateCount"], converted)
	}
	contents := request["contents"].([]interface{})
	content := contents[0].(map[string]interface{})
	parts := content["parts"].([]interface{})
	if len(parts) != 3 {
		t.Fatalf("parts len = %d, want text + inline images; body=%s", len(parts), converted)
	}
	inline := parts[1].(map[string]interface{})["inlineData"].(map[string]interface{})
	if inline["data"] != "ZmFrZS1wbmc=" {
		t.Fatalf("inline image data = %#v, body=%s", inline["data"], converted)
	}
	refInline := parts[2].(map[string]interface{})["inlineData"].(map[string]interface{})
	if refInline["data"] != "ZmFrZS1yZWY=" {
		t.Fatalf("reference image data = %#v, body=%s", refInline["data"], converted)
	}
}

func TestAntigravityImagesOpenAIResponseConvertsInlineData(t *testing.T) {
	resp := []byte(`{"response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}}]}}`)

	converted, err := antigravityImagesOpenAIResponse(resp, "url")
	if err != nil {
		t.Fatalf("convert image response: %v", err)
	}

	var got struct {
		Data []map[string]string `json:"data"`
	}
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("decode converted response: %v", err)
	}
	if len(got.Data) != 1 || got.Data[0]["url"] != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("converted response = %s", converted)
	}
}
