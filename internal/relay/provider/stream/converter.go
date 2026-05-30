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
	if ok {
		return factory()
	}
	if upstream == convert.FormatOpenAIChatCompletions || client == convert.FormatOpenAIChatCompletions {
		return nil
	}
	toChatFactory, ok := registry[FormatPair{Upstream: upstream, Client: convert.FormatOpenAIChatCompletions}]
	if !ok {
		return nil
	}
	fromChatFactory, ok := registry[FormatPair{Upstream: convert.FormatOpenAIChatCompletions, Client: client}]
	if !ok {
		return nil
	}
	return &chainedConverter{
		first:  toChatFactory(),
		second: fromChatFactory(),
	}
}

type chainedConverter struct {
	first  StreamConverter
	second StreamConverter
}

func (c *chainedConverter) Convert(line []byte) []byte {
	firstOut := c.first.Convert(line)
	return c.convertSecond(firstOut)
}

func (c *chainedConverter) Done() []byte {
	var out []byte
	if firstOut := c.first.Done(); len(firstOut) > 0 {
		out = append(out, c.convertSecond(firstOut)...)
	}
	out = append(out, c.second.Done()...)
	return out
}

func (c *chainedConverter) Reset() {
	c.first.Reset()
	c.second.Reset()
}

func (c *chainedConverter) convertSecond(events []byte) []byte {
	var out []byte
	for _, event := range splitStreamEvents(events) {
		out = append(out, c.second.Convert(event)...)
	}
	return out
}
