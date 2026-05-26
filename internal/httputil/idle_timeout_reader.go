package httputil

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

var ErrStreamIdleTimeout = errors.New("stream idle timeout")

type closeWithError interface {
	CloseWithError(error) error
}

type IdleTimeoutReader struct {
	reader     io.Reader
	bodyStream io.Reader
	timeout    time.Duration
	timer      *time.Timer
	fired      atomic.Bool
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
			panic(recovered)
		}
	}()
	n, err = r.reader.Read(p)
	if n > 0 && r.timer != nil {
		r.timer.Reset(r.timeout)
	}
	if err != nil && err != io.EOF && r.fired.Load() {
		return n, ErrStreamIdleTimeout
	}
	return n, err
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
		if closer, ok := r.bodyStream.(io.Closer); ok {
			_ = closer.Close()
			return
		}
		if closer, ok := r.bodyStream.(closeWithError); ok {
			_ = closer.CloseWithError(err)
		}
	})
}
