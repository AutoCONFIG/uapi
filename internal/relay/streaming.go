package relay

import (
	"bufio"
	"io"
	"log"
	"strings"
	"sync"
)

const (
	sseInitialBufSize = 8 * 1024
	sseMaxBufSize     = 10 * 1024 * 1024
)

// streamResult carries streaming outcome from producer to main goroutine.
type streamResult struct {
	promptTokens     int
	completionTokens int
	err              error
}

// streamTracker tracks usage in real-time from SSE chunks.
type streamTracker struct {
	mu               sync.Mutex
	promptTokens     int
	completionTokens int
	adaptor          adaptorUsageParser
}

type adaptorUsageParser interface {
	ParseStreamUsage(lastChunk []byte) (promptTokens, completionTokens int, err error)
}

func newStreamTracker(adaptor adaptorUsageParser) *streamTracker {
	return &streamTracker{adaptor: adaptor}
}

func (t *streamTracker) TrackChunk(dataLine []byte) {
	if len(dataLine) == 0 || len(dataLine) > sseMaxBufSize {
		return
	}
	pt, ct, err := t.adaptor.ParseStreamUsage(dataLine)
	if err == nil && (pt > 0 || ct > 0) {
		t.mu.Lock()
		t.promptTokens = pt
		t.completionTokens = ct
		t.mu.Unlock()
	}
}

func (t *streamTracker) Result() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.promptTokens, t.completionTokens
}

// streamAndForward reads SSE from upstream bodyStream, converts each line
// via the adaptor if needed, forwards to SSEStreamReader (downstream),
// and tracks usage.
func streamAndForward(
	bodyStream io.Reader,
	reader *SSEStreamReader,
	tracker *streamTracker,
	convertLine func([]byte) []byte,
) streamResult {
	closer, needClose := bodyStream.(io.Closer)
	if needClose {
		defer closer.Close()
	}

	scanner := bufio.NewScanner(bodyStream)
	scanner.Buffer(make([]byte, 0, sseInitialBufSize), sseMaxBufSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		lineStr := strings.TrimSpace(string(line))

		if lineStr == "" {
			if !reader.Send([]byte("\n")) {
				return streamResult{err: io.ErrClosedPipe}
			}
			continue
		}

		// Convert line format if adaptor requires it
		var forwardLine []byte
		if convertLine != nil {
			converted := convertLine(line)
			if converted == nil {
				continue // skip lines the converter filters out
			}
			forwardLine = converted
		} else {
			forwardLine = append([]byte(lineStr), '\n', '\n')
		}

		// Forward to client
		if !reader.Send(forwardLine) {
			return streamResult{err: io.ErrClosedPipe}
		}

		// Track usage from data lines (after conversion, so data is in OpenAI format).
		// forwardLine may contain multiple SSE events separated by \n\n (e.g. Gemini convertChunk).
		for _, seg := range strings.Split(string(forwardLine), "\n\n") {
			seg = strings.TrimSpace(seg)
			if strings.HasPrefix(seg, "data: ") {
				data := strings.TrimPrefix(seg, "data: ")
				if data == "[DONE]" {
					break
				}
				tracker.TrackChunk([]byte(data))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("SSE scanner error: %v", err)
		reader.Done()
		return streamResult{err: err}
	}

	reader.SendDone()
	reader.Done()

	pt, ct := tracker.Result()
	return streamResult{promptTokens: pt, completionTokens: ct}
}
