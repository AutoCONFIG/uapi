package httputil

import (
	"errors"
	"testing"
)

type panicReader struct {
	value interface{}
}

func (r panicReader) Read(_ []byte) (int, error) {
	panic(r.value)
}

func TestIdleTimeoutReaderConvertsClosedStreamPanic(t *testing.T) {
	reader, cleanup := NewIdleTimeoutReader(panicReader{value: "runtime error: slice bounds out of range [:-190]"}, nil, 0)
	defer cleanup()

	_, err := reader.Read(make([]byte, 8))
	if !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Read error = %v, want ErrStreamClosed", err)
	}
}
