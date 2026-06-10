package relay

import (
	"io"
	"runtime/debug"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/logger"
)

// SSEStreamReader is an io.ReadCloser that delivers one SSE event per Read call,
// bypassing fasthttp's internal pipe which batches multiple events into single TCP segments.
type SSEStreamReader struct {
	eventCh   chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once
	doneOnce  sync.Once
	closed    bool
	done      bool
	aborted   bool
	abortErr  error
	mu        sync.Mutex
	current   []byte
	trace     *relayDebugTrace
	readCount int
	sentCount int
}

func NewSSEStreamReader() *SSEStreamReader {
	return NewSSEStreamReaderWithTrace(nil)
}

func NewSSEStreamReaderWithTrace(trace *relayDebugTrace) *SSEStreamReader {
	return &SSEStreamReader{
		eventCh: make(chan []byte, 1),
		closeCh: make(chan struct{}),
		trace:   trace,
	}
}

func (r *SSEStreamReader) Read(p []byte) (int, error) {
	for {
		r.mu.Lock()
		if len(r.current) > 0 {
			n := copy(p, r.current)
			r.current = r.current[n:]
			r.readCount++
			r.mu.Unlock()
			return n, nil
		}
		r.mu.Unlock()

		select {
		case event, ok := <-r.eventCh:
			if !ok {
				r.trace.Event("downstream_read_eof",
					loggerReaderStateFields(r, "event_channel_closed")...,
				)
				return 0, io.EOF
			}
			r.mu.Lock()
			r.current = event
			fields := loggerReaderStateFieldsLocked(r, "event_dequeued")
			r.mu.Unlock()
			r.trace.Event("downstream_read_event_dequeued",
				append(fields, logger.F("sse", relayDebugSSEEventSummary(event)))...,
			)
		case <-r.closeCh:
			if err := r.abortError(); err != nil {
				r.trace.Event("downstream_read_abort",
					loggerReaderStateFields(r, "abort_channel_closed")...,
				)
				return 0, err
			}
			r.trace.Event("downstream_read_eof",
				loggerReaderStateFields(r, "close_channel_closed")...,
			)
			return 0, io.EOF
		}
	}
}

func (r *SSEStreamReader) Close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		fields := loggerReaderStateFieldsLocked(r, "close_called")
		close(r.closeCh)
		r.mu.Unlock()
		fields = append(fields, logger.F("stack", string(debug.Stack())))
		r.trace.Event("downstream_reader_close_called", fields...)
	})
	return nil
}

// Abort closes the downstream body with an error, so partial upstream failures
// are visible to the client as a broken stream instead of a clean EOF.
func (r *SSEStreamReader) Abort(err error) {
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.aborted = true
		r.abortErr = err
		fields := loggerReaderStateFieldsLocked(r, "abort_called")
		close(r.closeCh)
		r.mu.Unlock()
		fields = append(fields, logger.Err(err))
		r.trace.Event("downstream_reader_abort_called", fields...)
	})
}

// Send delivers a pre-formatted SSE event. Returns false if reader is closed (client disconnected).
func (r *SSEStreamReader) Send(event []byte) bool {
	r.mu.Lock()
	if r.closed {
		fields := loggerReaderStateFieldsLocked(r, "send_closed_before_select")
		r.mu.Unlock()
		r.trace.Event("downstream_send_closed", append(fields, logger.F("sse", relayDebugSSEEventSummary(event)))...)
		return false
	}
	closeCh := r.closeCh
	r.mu.Unlock()

	select {
	case r.eventCh <- event:
		r.mu.Lock()
		r.sentCount++
		fields := loggerReaderStateFieldsLocked(r, "send_enqueued")
		r.mu.Unlock()
		r.trace.Event("downstream_send_enqueued", append(fields, logger.F("sse", relayDebugSSEEventSummary(event)))...)
		return true
	case <-closeCh:
		r.trace.Event("downstream_send_closed",
			append(loggerReaderStateFields(r, "send_select_close"), logger.F("sse", relayDebugSSEEventSummary(event)))...,
		)
		return false
	}
}

// SendDone sends the standard SSE done marker.
func (r *SSEStreamReader) SendDone() bool {
	return r.Send([]byte("data: [DONE]\n\n"))
}

func (r *SSEStreamReader) Closed() <-chan struct{} {
	return r.closeCh
}

// Done closes the event channel, signaling Read that the stream is finished.
func (r *SSEStreamReader) Done() {
	r.doneOnce.Do(func() {
		r.mu.Lock()
		r.done = true
		fields := loggerReaderStateFieldsLocked(r, "done_called")
		close(r.eventCh)
		r.mu.Unlock()
		r.trace.Event("downstream_reader_done_called", fields...)
	})
}

func loggerReaderStateFields(r *SSEStreamReader, reason string) []logger.Field {
	r.mu.Lock()
	defer r.mu.Unlock()
	return loggerReaderStateFieldsLocked(r, reason)
}

func (r *SSEStreamReader) abortError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.abortErr
}

func loggerReaderStateFieldsLocked(r *SSEStreamReader, reason string) []logger.Field {
	return []logger.Field{
		logger.F("reason", reason),
		logger.F("closed", r.closed),
		logger.F("done", r.done),
		logger.F("aborted", r.aborted),
		logger.F("read_count", r.readCount),
		logger.F("sent_count", r.sentCount),
		logger.F("current_bytes", len(r.current)),
	}
}
