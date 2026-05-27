package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// MarshalJSON emits a bare string if Text is set, or an array if Parts is set.
// If both are set it returns an error; if neither it emits null.
func (mc MessageContent) MarshalJSON() ([]byte, error) {
	hasText := mc.Text != nil && *mc.Text != ""
	hasParts := len(mc.Parts) > 0

	if hasText && hasParts {
		return nil, errors.New("schema: MessageContent has both Text and Parts set")
	}

	if hasText {
		return json.Marshal(*mc.Text)
	}

	if hasParts {
		return json.Marshal(mc.Parts)
	}

	return json.Marshal(nil)
}

// UnmarshalJSON tries bare string first, then []ContentPart.
func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		mc.Text = nil
		mc.Parts = nil
		return nil
	}

	// Try bare string.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		mc.Text = &s
		mc.Parts = nil
		return nil
	}

	// Try array of ContentPart.
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err == nil {
		mc.Text = nil
		mc.Parts = parts
		return nil
	}

	return errors.New("schema: MessageContent must be a string or array of ContentPart")
}

// IsEmpty returns true if the content is nil, or both Text is nil/empty and Parts is empty.
func (mc MessageContent) IsEmpty() bool {
	if mc.Text == nil && len(mc.Parts) == 0 {
		return true
	}
	if mc.Text != nil && *mc.Text == "" && len(mc.Parts) == 0 {
		return true
	}
	return false
}

// ExtractText returns all text from the content.
// For bare string Text it returns the string value.
// For Parts it joins all text-type parts with "\n".
func (mc MessageContent) ExtractText() string {
	if mc.Text != nil {
		return *mc.Text
	}

	var sb strings.Builder
	for i, p := range mc.Parts {
		if p.Type == "text" && p.Text != "" {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// NewTextContent creates a MessageContent with bare string content.
func NewTextContent(text string) MessageContent {
	return MessageContent{Text: &text}
}

// NewPartsContent creates a MessageContent with array content.
func NewPartsContent(parts ...ContentPart) MessageContent {
	return MessageContent{Parts: parts}
}

// TextPart creates a ContentPart of type "text".
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// ImageURLPart creates a ContentPart of type "image_url".
func ImageURLPart(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &url}
}
