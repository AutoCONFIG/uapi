package stream

import (
	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	relayir "github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

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

type streamIRParser interface {
	Parse(line []byte) []relayir.StreamEvent
	Done() []relayir.StreamEvent
	Reset()
}

type streamIREmitter interface {
	Emit(event relayir.StreamEvent) []byte
	Done() []byte
	Reset()
}

var streamIRParsers = map[convert.Format]func() streamIRParser{}
var streamIREmitters = map[convert.Format]func() streamIREmitter{}

func RegisterIRParser(format convert.Format, factory func() streamIRParser) {
	streamIRParsers[format] = factory
}

func RegisterIREmitter(format convert.Format, factory func() streamIREmitter) {
	streamIREmitters[format] = factory
}

// NewConverter creates a StreamConverter for the given direction.
// Returns nil if no converter is registered (same-format passthrough).
func NewConverter(upstream, client convert.Format) StreamConverter {
	if parserFactory, ok := streamIRParsers[upstream]; ok {
		if emitterFactory, ok := streamIREmitters[client]; ok && upstream != client {
			return &irStreamConverter{parser: parserFactory(), emitter: emitterFactory()}
		}
	}
	return nil
}

type irStreamConverter struct {
	parser  streamIRParser
	emitter streamIREmitter
}

func (c *irStreamConverter) Convert(line []byte) []byte {
	var out []byte
	for _, event := range c.parser.Parse(line) {
		out = append(out, c.emitter.Emit(event)...)
	}
	return out
}

func (c *irStreamConverter) Done() []byte {
	var out []byte
	for _, event := range c.parser.Done() {
		out = append(out, c.emitter.Emit(event)...)
	}
	out = append(out, c.emitter.Done()...)
	return out
}

func (c *irStreamConverter) Reset() {
	c.parser.Reset()
	c.emitter.Reset()
}
