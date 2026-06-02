package httputil

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrStreamIdleTimeout = errors.New("stream idle timeout")
var ErrStreamClosed = errors.New("stream closed")

type closeWithError interface {
	CloseWithError(error) error
}

type IdleTimeoutReader struct {
	reader     io.Reader
	bodyStream io.Reader
	timeout    time.Duration
	timer      *time.Timer
	fired      atomic.Bool
	closed     atomic.Bool
	closeOnce  sync.Once
	timerOnce  sync.Once
	timerDone  chan struct{}
}

func NewIdleTimeoutReader(reader io.Reader, bodyStream io.Reader, timeout time.Duration) (*IdleTimeoutReader, func()) {
	r := &IdleTimeoutReader{
		reader:     reader,
		bodyStream: bodyStream,
		timeout:    timeout,
		timerDone:  make(chan struct{}),
	}
	if timeout <= 0 {
		return r, func() {}
	}
	r.timer = time.AfterFunc(timeout, func() {
		defer r.timerOnce.Do(func() { close(r.timerDone) })
		r.fired.Store(true)
		r.closeBody(ErrStreamIdleTimeout)
	})
	return r, r.cleanup
}

func (r *IdleTimeoutReader) Read(p []byte) (n int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if r.fired.Load() {
				n = 0
				err = ErrStreamIdleTimeout
				return
			}
			if r.closed.Load() {
				n = 0
				err = ErrStreamClosed
				return
			}
			if isClosedStreamPanic(recovered) {
				n = 0
				err = ErrStreamClosed
				return
			}
			panic(recovered)
		}
	}()
	if r.closed.Load() {
		return 0, ErrStreamClosed
	}
	n, err = r.reader.Read(p)
	if n > 0 && r.timer != nil {
		r.timer.Reset(r.timeout)
	}
	if err != nil && err != io.EOF && r.fired.Load() {
		return n, ErrStreamIdleTimeout
	}
	if err != nil && err != io.EOF && r.closed.Load() {
		return n, ErrStreamClosed
	}
	return n, err
}

func isClosedStreamPanic(recovered interface{}) bool {
	msg := fmt.Sprint(recovered)
	return strings.Contains(msg, "slice bounds out of range") ||
		strings.Contains(msg, "body closed") ||
		strings.Contains(msg, "use of closed network connection")
}

func (r *IdleTimeoutReader) Close() error {
	r.closeBody(io.ErrClosedPipe)
	r.cleanup()
	return nil
}

func (r *IdleTimeoutReader) cleanup() {
	if r.timer == nil {
		return
	}
	if r.timer.Stop() {
		r.timerOnce.Do(func() { close(r.timerDone) })
		return
	}
	<-r.timerDone
}

func (r *IdleTimeoutReader) closeBody(err error) {
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		if closer, ok := r.bodyStream.(closeWithError); ok {
			_ = closer.CloseWithError(err)
			return
		}
		if closer, ok := r.bodyStream.(io.Closer); ok {
			_ = closer.Close()
		}
	})
}
