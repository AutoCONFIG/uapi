package relay

import (
	"io"
	"sync"
)

// SSEStreamReader is an io.ReadCloser that delivers one SSE event per Read call,
// bypassing fasthttp's internal pipe which batches multiple events into single TCP segments.
type SSEStreamReader struct {
	eventCh   chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once
	closed    bool
	mu        sync.Mutex
	current   []byte
}

func NewSSEStreamReader() *SSEStreamReader {
	return &SSEStreamReader{
		eventCh: make(chan []byte, 1),
		closeCh: make(chan struct{}),
	}
}

func (r *SSEStreamReader) Read(p []byte) (int, error) {
	if len(r.current) == 0 {
		event, ok := <-r.eventCh
		if !ok {
			return 0, io.EOF
		}
		r.current = event
	}
	n := copy(p, r.current)
	r.current = r.current[n:]
	return n, nil
}

func (r *SSEStreamReader) Close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		close(r.closeCh)
	})
	return nil
}

// Send delivers a pre-formatted SSE event. Returns false if reader is closed (client disconnected).
func (r *SSEStreamReader) Send(event []byte) bool {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	// Hold lock while sending to prevent Close() from closing closeCh
	// between our closed check and the select statement.
	select {
	case r.eventCh <- event:
		r.mu.Unlock()
		return true
	case <-r.closeCh:
		r.mu.Unlock()
		return false
	}
}

// SendDone sends the standard SSE done marker.
func (r *SSEStreamReader) SendDone() bool {
	return r.Send([]byte("data: [DONE]\n\n"))
}

// Done closes the event channel, signaling Read that the stream is finished.
func (r *SSEStreamReader) Done() {
	close(r.eventCh)
}
