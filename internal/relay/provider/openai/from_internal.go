package openai

import "github.com/AutoCONFIG/cli-relay/internal/relay/provider"

// internalToOpenAIChat is already defined in to_internal.go.
// This file exists per the task spec for organizational clarity.
// The FromInternal converter is in to_internal.go as internalToOpenAIChat.
// The Responses API converter is also there as internalToResponses.

// Ensure the converters are registered at init time (done in adaptor.go).
// This file is intentionally minimal — all FromInternal logic lives in to_internal.go
// alongside the ToInternal logic for easy side-by-side review.

// Verify interface compliance at compile time.
var _ provider.Adaptor = (*OpenAIAdaptor)(nil)
