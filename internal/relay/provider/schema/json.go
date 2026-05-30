package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

var errNotStringOrArray = errors.New("schema: input must be a string or array")

// trimNull returns the data without leading/trailing whitespace and checks for null.
// Returns nil if the input is null or empty.
func trimNull(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	return trimmed
}

// unmarshalExtra extracts unknown fields from JSON data into the provided Extra map.
func unmarshalExtra(data []byte, v interface{}, extra *map[string]json.RawMessage) error {
	// Parse as generic map[string]interface{} to find unknown keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if *extra == nil {
		*extra = make(map[string]json.RawMessage)
	}

	// Get the known field names from the struct.
	knownFields := getKnownFields(v)

	for k, v := range raw {
		if _, ok := knownFields[k]; !ok {
			(*extra)[k] = v
		}
	}

	return nil
}

// getKnownFields uses reflection to get all JSON field names from a struct type
func getKnownFields(v interface{}) map[string]bool {
	t := reflect.TypeOf(v)
	// If v is a pointer, get the element type
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return make(map[string]bool)
	}
	known := make(map[string]bool)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if jsonTag := field.Tag.Get("json"); jsonTag != "" {
			// Handle json:"fieldname,omitempty" by extracting fieldname
			name := strings.Split(jsonTag, ",")[0]
			if name != "-" {
				known[name] = true
			}
		}
	}
	return known
}

// marshalExtra marshals the struct to JSON, then adds the Extra fields.
func marshalExtra(v interface{}, extra map[string]json.RawMessage) ([]byte, error) {
	// First marshal the main struct (without Extra).
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	if len(extra) == 0 {
		return data, nil
	}

	// Parse the result as a map to add extra fields.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	// Add extra fields.
	for k, v := range extra {
		m[k] = v
	}

	return json.Marshal(m)
}

// MarshalJSON emits a bare string if Text is set, or an array if Parts is set.
// If both are set it returns an error; if neither it emits null.
func (mc MessageContent) MarshalJSON() ([]byte, error) {
	hasText := mc.Text != nil
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
