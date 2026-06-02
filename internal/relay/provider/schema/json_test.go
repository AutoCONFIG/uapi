package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStringRoundTrip(t *testing.T) {
	mc := NewTextContent("hello")

	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if string(data) != `"hello"` {
		t.Fatalf("expected %q, got %q", `"hello"`, string(data))
	}

	var mc2 MessageContent
	if err := json.Unmarshal(data, &mc2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if mc2.Text == nil || *mc2.Text != "hello" {
		t.Fatalf("expected Text==hello, got %v", mc2.Text)
	}

	if len(mc2.Parts) != 0 {
		t.Fatalf("expected no Parts, got %d", len(mc2.Parts))
	}
}

func TestArrayRoundTrip(t *testing.T) {
	mc := NewPartsContent(TextPart("hello"), ImageURLPart("url"))

	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var mc2 MessageContent
	if err := json.Unmarshal(data, &mc2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if mc2.Text != nil {
		t.Fatalf("expected nil Text, got %v", mc2.Text)
	}

	if len(mc2.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(mc2.Parts))
	}

	if mc2.Parts[0].Type != "text" || mc2.Parts[0].Text != "hello" {
		t.Fatalf("unexpected first part: %+v", mc2.Parts[0])
	}

	if mc2.Parts[1].Type != "image_url" || mc2.Parts[1].ImageURL == nil || *mc2.Parts[1].ImageURL != "url" {
		t.Fatalf("unexpected second part: %+v", mc2.Parts[1])
	}
}

func TestNullRoundTrip(t *testing.T) {
	mc := MessageContent{}

	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if string(data) != "null" {
		t.Fatalf("expected null, got %s", string(data))
	}

	var mc2 MessageContent
	if err := json.Unmarshal(data, &mc2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !mc2.IsEmpty() {
		t.Fatal("expected IsEmpty()==true")
	}
}

func TestExtractTextFromString(t *testing.T) {
	mc := NewTextContent("hello world")
	if got := mc.ExtractText(); got != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", got)
	}
}

func TestExtractTextFromParts(t *testing.T) {
	mc := NewPartsContent(
		TextPart("line1"),
		ImageURLPart("http://img"),
		TextPart("line2"),
	)

	got := mc.ExtractText()
	want := "line1\nline2"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBothTextAndPartsError(t *testing.T) {
	s := "oops"
	mc := MessageContent{
		Text:  &s,
		Parts: []ContentPart{{Type: "text", Text: "oops"}},
	}

	_, err := json.Marshal(mc)
	if err == nil {
		t.Fatal("expected error when both Text and Parts are set")
	}
}

func TestUnmarshalArrayOfParts(t *testing.T) {
	data := []byte(`[{"type":"text","text":"hello"},{"type":"image_url","image_url":"http://img"}]`)

	var mc MessageContent
	if err := json.Unmarshal(data, &mc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if mc.Text != nil {
		t.Fatalf("expected nil Text, got %v", mc.Text)
	}

	if len(mc.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(mc.Parts))
	}

	if mc.Parts[0].Type != "text" || mc.Parts[0].Text != "hello" {
		t.Fatalf("unexpected first part: %+v", mc.Parts[0])
	}

	if mc.Parts[1].Type != "image_url" || mc.Parts[1].Text != "" || mc.Parts[1].ImageURL == nil || *mc.Parts[1].ImageURL != "http://img" {
		t.Fatalf("unexpected second part: %+v", mc.Parts[1])
	}
}

func TestMarshalImageURLPartUsesObjectShape(t *testing.T) {
	url := "data:application/pdf;base64,AA=="
	mc := MessageContent{Parts: []ContentPart{{
		Type:        "image_url",
		ImageURL:    &url,
		ImageDetail: "high",
	}}}

	raw, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parts []map[string]interface{}
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("unmarshal marshaled content: %v\n%s", err, raw)
	}
	image := parts[0]["image_url"].(map[string]interface{})
	if image["url"] != url || image["detail"] != "high" {
		t.Fatalf("image_url object not preserved: %s", raw)
	}
	if !strings.Contains(string(raw), `{"type":"image_url","image_url":{"url":"data:application/pdf;base64,AA==","detail":"high"}}`) {
		t.Fatalf("image_url part must marshal type before image_url for compatible upstream parsers: %s", raw)
	}
}

func TestUnmarshalInvalidJSON(t *testing.T) {
	data := []byte(`{"unknown": true}`)

	var mc MessageContent
	err := json.Unmarshal(data, &mc)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestUnmarshalOverwritesExisting(t *testing.T) {
	// Start with a pre-existing MessageContent (string form).
	mc := NewTextContent("original")
	if mc.Text == nil || *mc.Text != "original" {
		t.Fatalf("setup: expected original content, got %+v", mc)
	}

	// Unmarshal an array — should overwrite the string content.
	data := []byte(`[{"type":"text","text":"new content"}]`)
	if err := json.Unmarshal(data, &mc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if mc.Text != nil {
		t.Fatalf("expected Text to be nil after array unmarshal, got %v", mc.Text)
	}

	if len(mc.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(mc.Parts))
	}

	if mc.Parts[0].Type != "text" || mc.Parts[0].Text != "new content" {
		t.Fatalf("unexpected part: %+v", mc.Parts[0])
	}
}
