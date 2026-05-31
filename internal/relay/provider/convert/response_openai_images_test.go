package convert

import (
	"encoding/json"
	"testing"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func TestEmitOpenAIResponsesResponseMapsInlineImageToImageGenerationCall(t *testing.T) {
	imageURL := "data:image/png;base64,aGVsbG8="
	body, err := emitOpenAIResponsesResponse(&responseDraft{
		ID:    "resp_1",
		Model: "nano-banana-2",
		Choices: []responseChoiceDraft{{
			Role: "model",
			Items: []requestItemDraft{{
				Kind: contentItemKindContent,
				Content: schema.ContentPart{
					Type:     "image_url",
					ImageURL: &imageURL,
				},
			}},
			FinishReason: "end_turn",
		}},
	})
	if err != nil {
		t.Fatalf("convert response: %v", err)
	}

	var got struct {
		Output []struct {
			Type         string `json:"type"`
			Status       string `json:"status"`
			Result       string `json:"result"`
			OutputFormat string `json:"output_format"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Output) != 1 {
		t.Fatalf("output len = %d, body=%s", len(got.Output), body)
	}
	if got.Output[0].Type != "image_generation_call" || got.Output[0].Status != "completed" {
		t.Fatalf("unexpected output item: %+v body=%s", got.Output[0], body)
	}
	if got.Output[0].Result != "aGVsbG8=" || got.Output[0].OutputFormat != "png" {
		t.Fatalf("unexpected image payload: %+v body=%s", got.Output[0], body)
	}
}

func TestEmitOpenAIResponsesResponsePreservesTextBesideGeneratedImage(t *testing.T) {
	imageURL := "data:image/webp;base64,aW1n"
	body, err := emitOpenAIResponsesResponse(&responseDraft{
		ID:    "resp_1",
		Model: "nano-banana-2",
		Choices: []responseChoiceDraft{{
			Role: "model",
			Items: []requestItemDraft{
				{Kind: contentItemKindContent, Content: schema.ContentPart{Type: "text", Text: "Here is the image."}},
				{Kind: contentItemKindContent, Content: schema.ContentPart{Type: "image_url", ImageURL: &imageURL}},
			},
			FinishReason: "end_turn",
		}},
	})
	if err != nil {
		t.Fatalf("convert response: %v", err)
	}

	var got struct {
		Output []struct {
			Type         string               `json:"type"`
			OutputFormat string               `json:"output_format"`
			Content      []schema.ContentPart `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Output) != 2 {
		t.Fatalf("output len = %d, body=%s", len(got.Output), body)
	}
	if got.Output[0].Type != "message" || len(got.Output[0].Content) != 1 || got.Output[0].Content[0].Text != "Here is the image." {
		t.Fatalf("text message not preserved in order: %+v body=%s", got.Output[0], body)
	}
	if got.Output[1].Type != "image_generation_call" || got.Output[1].OutputFormat != "webp" {
		t.Fatalf("unexpected image output: %+v body=%s", got.Output[1], body)
	}
}
