package channelcap

import (
	"testing"

	"github.com/AutoCONFIG/uapi/internal/db"
)

func TestChatGPTReverseSupportsMultimodalChat(t *testing.T) {
	ch := db.Channel{Type: "openai", APIFormat: "chatgpt_reverse"}
	tests := []struct {
		name string
		req  Request
		want bool
	}{
		{name: "plain chat", req: Request{Kind: KindChatCompletion}, want: true},
		{name: "tools", req: Request{Kind: KindChatCompletion, HasTools: true}, want: false},
		{name: "images", req: Request{Kind: KindChatCompletion, HasImages: true}, want: true},
		{name: "responses", req: Request{Kind: KindResponses}, want: true}, {name: "messages", req: Request{Kind: KindMessages}, want: true}, {name: "responses with tools", req: Request{Kind: KindResponses, HasTools: true}, want: false},
		{name: "responses with images", req: Request{Kind: KindResponses, HasImages: true}, want: true},
		{name: "image generation", req: Request{Kind: KindImageGeneration}, want: true},
		{name: "image edit", req: Request{Kind: KindImageEdit}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Supports(ch, tt.req); got != tt.want {
				t.Fatalf("Supports() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnalyzeJSONDetectsToolsAndImages(t *testing.T) {
	req := AnalyzeJSON(KindChatCompletion, []byte(`{"model":"gpt-5.5","tools":[{"type":"function","function":{"name":"x"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,xx"}}]}]}`))
	if !req.HasTools {
		t.Fatalf("HasTools = false, want true")
	}
	if !req.HasImages {
		t.Fatalf("HasImages = false, want true")
	}
}
