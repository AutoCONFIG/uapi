package httputil_idle_timeout_test

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/httputil"
)

func TestIdleTimeoutReaderTimesOutBlockedRead(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	reader, cleanup := httputil.NewIdleTimeoutReader(pr, pr, 20*time.Millisecond)
	defer cleanup()

	buf := make([]byte, 8)
	_, err := reader.Read(buf)
	if !errors.Is(err, httputil.ErrStreamIdleTimeout) && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read error = %v, want idle timeout or closed pipe", err)
	}
}

func TestIdleTimeoutReaderResetsTimerOnData(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	reader, cleanup := httputil.NewIdleTimeoutReader(pr, pr, 50*time.Millisecond)
	defer cleanup()

	go func() {
		_, _ = pw.Write([]byte("a"))
		time.Sleep(25 * time.Millisecond)
		_, _ = pw.Write([]byte("b"))
		_ = pw.Close()
	}()

	buf := make([]byte, 1)
	if n, err := reader.Read(buf); n != 1 || err != nil {
		t.Fatalf("first Read = (%d, %v), want one byte", n, err)
	}
	if n, err := reader.Read(buf); n != 1 || err != nil {
		t.Fatalf("second Read = (%d, %v), want one byte", n, err)
	}
}
