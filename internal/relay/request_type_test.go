package relay

import "testing"

func TestDetectRelayRequestTypeCoversProtocolFamilies(t *testing.T) {
	cases := map[string]relayRequestType{
		"/v1/chat/completions":                  requestTypeChatCompletion,
		"/v1/responses":                         requestTypeResponses,
		"/v1/messages":                          requestTypeMessages,
		"/v1beta/models/gemini:generateContent": requestTypeGeminiGenerate,
		"/v1/images/generations":                requestTypeImageGeneration,
		"/v1/images/edits":                      requestTypeImageEdit,
		"/v1/audio/speech":                      requestTypeSpeech,
		"/v1/audio/transcriptions":              requestTypeTranscription,
		"/v1/embeddings":                        requestTypeEmbedding,
		"/v1/moderations":                       requestTypeModeration,
		"/v1/realtime/sessions":                 requestTypeRealtime,
		"/v1/videos":                            requestTypeVideoGeneration,
	}
	for path, want := range cases {
		if got := detectRelayRequestType(path); got != want {
			t.Fatalf("detectRelayRequestType(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSupportsRelayRequestTypeMakesNonTextCapabilitiesExplicit(t *testing.T) {
	if !supportsRelayRequestType("antigravity", requestTypeImageGeneration) {
		t.Fatalf("antigravity should support image generation")
	}
	if !supportsRelayRequestType("antigravity", requestTypeImageEdit) {
		t.Fatalf("antigravity should support image edit via image_gen reference images")
	}
	if !supportsRelayRequestType("openai", requestTypeSpeech) {
		t.Fatalf("openai should passthrough speech")
	}
	if supportsRelayRequestType("gemini", requestTypeEmbedding) {
		t.Fatalf("gemini embeddings should remain unsupported until a converter exists")
	}
	if supportsRelayRequestType("antigravity", requestTypeRealtime) {
		t.Fatalf("antigravity realtime passthrough should remain unsupported")
	}
	if !supportsRelayRequestType("openai", requestTypeVideoGeneration) {
		t.Fatalf("openai should passthrough video generation")
	}
}
