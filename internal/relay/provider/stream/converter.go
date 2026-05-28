package stream

import "github.com/AutoCONFIG/uapi/internal/relay/provider/convert"

// StreamConverter converts SSE lines from one protocol format to another.
type StreamConverter interface {
	// Convert processes a single SSE data line and returns zero or more
	// SSE data lines in the target format.
	Convert(line []byte) []byte
	// Done returns any final events needed when the stream ends.
	Done() []byte
	// Reset clears all internal state for pool return.
	Reset()
}

// FormatPair identifies a conversion direction.
type FormatPair struct {
	Upstream convert.Format
	Client   convert.Format
}

var registry = map[FormatPair]func() StreamConverter{}

// Register registers a StreamConverter factory for a FormatPair.
func Register(pair FormatPair, factory func() StreamConverter) {
	registry[pair] = factory
}

// NewConverter creates a StreamConverter for the given direction.
// Returns nil if no converter is registered (same-format passthrough).
func NewConverter(upstream, client convert.Format) StreamConverter {
	factory, ok := registry[FormatPair{Upstream: upstream, Client: client}]
	if !ok {
		return nil
	}
	return factory()
}