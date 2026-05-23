package relay

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/AutoCONFIG/uapi/internal/logger"
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
	finalized        bool
	failed           bool
	parseFailed      bool // true if ParseStreamUsage had errors
}

// streamTracker tracks usage in real-time from SSE chunks.
type streamTracker struct {
	mu                  sync.Mutex
	promptTokens        int
	completionTokens    int
	hasPromptTokens     bool
	hasCompletionTokens bool
	firstPromptTokens   int // first non-zero prompt tokens observed
	hasFirstPrompt      bool
	parseErrors         int
	adaptor             adaptorUsageParser
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
	if err != nil {
		t.mu.Lock()
		t.parseErrors++
		t.mu.Unlock()
		return
	}
	if pt > 0 || ct > 0 {
		t.mu.Lock()
		if pt > 0 || !t.hasPromptTokens {
			t.promptTokens = pt
			t.hasPromptTokens = pt > 0
		}
		if pt > 0 && !t.hasFirstPrompt {
			t.firstPromptTokens = pt
			t.hasFirstPrompt = true
		}
		if ct > 0 || !t.hasCompletionTokens {
			t.completionTokens = ct
			t.hasCompletionTokens = ct > 0
		}
		t.mu.Unlock()
	}
}

func (t *streamTracker) Result() (int, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pt := t.promptTokens
	// Fall back to first observed prompt tokens if the final value is zero
	// (e.g. Anthropic message_start input_tokens captured early but later
	// chunks overwrite with 0).
	if pt == 0 && t.firstPromptTokens > 0 {
		pt = t.firstPromptTokens
	}
	return pt, t.completionTokens, t.parseErrors > 0
}

// streamAndForward reads upstream SSE, optionally converts provider-specific
// lines, forwards downstream events, and tracks usage.
func streamAndForward(
	bodyStream io.Reader,
	reader *SSEStreamReader,
	tracker *streamTracker,
	inputConvert func([]byte) []byte,
	outputConvert func([]byte) []byte,
	sendDone bool,
) streamResult {
	defer reader.Done()
	closer, needClose := bodyStream.(io.Closer)
	if needClose {
		done := make(chan struct{})
		go func() {
			select {
			case <-reader.Closed():
				_ = closer.Close()
			case <-done:
			}
		}()
		defer close(done)
		defer closer.Close()
	}

	scanner := bufio.NewScanner(bodyStream)
	scanner.Buffer(make([]byte, 0, sseInitialBufSize), sseMaxBufSize)

	if inputConvert == nil && outputConvert == nil {
		return streamRawAndForward(scanner, reader, tracker, sendDone)
	}

	sawDone := false
	sawTerminal := false
	sawChatFinish := false
	failed := false
	var event []byte

	flush := func() (bool, error) {
		if len(event) == 0 {
			return true, nil
		}
		if len(event) < 2 || string(event[len(event)-2:]) != "\n\n" {
			event = append(event, '\n')
		}
		current := event
		event = nil

		normalized := normalizeSSEEventForConverterWithEvent(current)
		if len(normalized) == 0 {
			return true, nil
		}
		dataOnly := normalizeSSEEventForConverter(current)
		if streamHasFailureEvent(normalized) {
			failed = true
		}
		if streamHasTerminalEvent(normalized) {
			sawTerminal = true
		}

		var forwardLine []byte
		if inputConvert != nil {
			converted := inputConvert(normalized)
			if converted == nil {
				return true, nil // skip events the converter filters out
			}
			forwardLine = converted
		} else {
			forwardLine = normalized
			if len(forwardLine) == 0 {
				forwardLine = dataOnly
			}
		}
		if strings.TrimSpace(string(forwardLine)) == "data: [DONE]" {
			sawDone = true
		}
		if streamSawChatFinish(forwardLine) {
			sawChatFinish = true
		}
		if streamHasFailureEvent(forwardLine) {
			failed = true
		}

		// Track usage from normalized OpenAI data before the final client-format conversion.
		segments := splitSSEEvents(forwardLine)
		for _, segBytes := range segments {
			seg := strings.TrimSpace(string(segBytes))
			if strings.HasPrefix(seg, "data: ") {
				data := strings.TrimPrefix(seg, "data: ")
				if data == "[DONE]" {
					break
				}
				tracker.TrackChunk([]byte(data))
			}
		}

		// Stage 2: normalized OpenAI SSE → client format SSE (if needed)
		if outputConvert != nil {
			var out []byte
			for _, seg := range segments {
				converted := outputConvert(seg)
				if converted != nil {
					out = append(out, converted...)
				}
			}
			if len(out) == 0 {
				return true, nil
			}
			forwardLine = out
		}
		if streamHasFailureEvent(forwardLine) {
			failed = true
		}
		if streamSawChatFinish(forwardLine) {
			sawChatFinish = true
		}
		if !sendDone && streamHasTerminalEvent(forwardLine) {
			sawDone = true
		}

		// Forward to client
		if !reader.Send(forwardLine) {
			return false, io.ErrClosedPipe
		}
		return true, nil
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		lineStr := strings.TrimSpace(string(line))

		if lineStr == "" {
			ok, err := flush()
			if err != nil {
				return streamResult{err: err}
			}
			if !ok {
				return streamResult{err: io.ErrClosedPipe}
			}
			continue
		}

		event = append(event, line...)
		event = append(event, '\n')
	}

	if err := scanner.Err(); err != nil {
		logger.Warnf("relay.sse", "scanner failed", logger.Err(err))
		return streamResult{err: err}
	}
	if ok, err := flush(); err != nil {
		return streamResult{err: err}
	} else if !ok {
		return streamResult{err: io.ErrClosedPipe}
	}

	if sendDone && !sawDone {
		reader.SendDone()
		sawDone = sawChatFinish || sawTerminal
	} else if !sendDone && !sawDone && outputConvert != nil {
		if converted := outputConvert([]byte("data: [DONE]\n\n")); converted != nil {
			_ = reader.Send(converted)
			sawDone = true
		}
	}

	pt, ct, parseFailed := tracker.Result()
	return streamResult{promptTokens: pt, completionTokens: ct, finalized: sawDone || sawTerminal, failed: failed, parseFailed: parseFailed}
}

func streamSawChatFinish(event []byte) bool {
	s := string(event)
	if strings.Contains(s, `"finish_reason":"stop"`) ||
		strings.Contains(s, `"finish_reason":"length"`) ||
		strings.Contains(s, `"finish_reason":"tool_calls"`) ||
		strings.Contains(s, `"finish_reason":"content_filter"`) {
		return true
	}
	for _, payload := range sseDataPayloads(event) {
		var chunk struct {
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
			for _, choice := range chunk.Choices {
				if choice.FinishReason != nil && *choice.FinishReason != "" {
					return true
				}
			}
		}
	}
	return false
}

func normalizeSSEEventForConverter(event []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(event), "\n"), "\n")
	dataParts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = strings.TrimPrefix(data, " ")
			}
			dataParts = append(dataParts, data)
		}
	}
	if len(dataParts) == 0 {
		return nil
	}
	return []byte("data: " + strings.Join(dataParts, "\n") + "\n\n")
}

func normalizeSSEEventForConverterWithEvent(event []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(event), "\n"), "\n")
	dataParts := make([]string, 0, len(lines))
	eventName := ""
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = strings.TrimPrefix(data, " ")
			}
			dataParts = append(dataParts, data)
		}
	}
	if len(dataParts) == 0 {
		return nil
	}
	prefix := ""
	if eventName != "" {
		prefix = "event: " + eventName + "\n"
	}
	return []byte(prefix + "data: " + strings.Join(dataParts, "\n") + "\n\n")
}

func splitSSEEvents(buf []byte) [][]byte {
	parts := strings.Split(strings.TrimRight(string(buf), "\n"), "\n\n")
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, []byte(part+"\n\n"))
	}
	return out
}

func streamRawAndForward(scanner *bufio.Scanner, reader *SSEStreamReader, tracker *streamTracker, sendDone bool) streamResult {
	var event []byte
	sawDone := false
	sawTerminal := false
	sawChatFinish := false
	failed := false

	flush := func() bool {
		if len(event) == 0 {
			return true
		}
		if len(event) < 2 || string(event[len(event)-2:]) != "\n\n" {
			event = append(event, '\n')
		}
		ok := reader.Send(event)
		event = nil
		return ok
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		lineStr := strings.TrimSpace(string(line))
		if lineStr == "" {
			if !flush() {
				return streamResult{err: io.ErrClosedPipe}
			}
			continue
		}

		event = append(event, line...)
		event = append(event, '\n')

		if strings.HasPrefix(lineStr, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
			if data == "[DONE]" {
				sawDone = true
			} else {
				tracker.TrackChunk([]byte(data))
			}
		}
		if streamHasTerminalEvent(event) {
			sawTerminal = true
		}
		if streamSawChatFinish(event) {
			sawChatFinish = true
		}
		if streamHasFailureEvent(event) {
			failed = true
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Warnf("relay.sse", "scanner failed", logger.Err(err))
		return streamResult{err: err}
	}
	if !flush() {
		return streamResult{err: io.ErrClosedPipe}
	}
	if sendDone && !sawDone {
		reader.SendDone()
		sawDone = sawChatFinish || sawTerminal
	}
	pt, ct, parseFailed := tracker.Result()
	finalized := sawDone || sawTerminal
	return streamResult{promptTokens: pt, completionTokens: ct, finalized: finalized, failed: failed, parseFailed: parseFailed}
}

func streamHasTerminalEvent(event []byte) bool {
	s := string(event)
	if strings.Contains(s, "event: response.completed\n") ||
		strings.Contains(s, "event: response.completed\r\n") ||
		strings.Contains(s, `"type":"response.completed"`) ||
		strings.Contains(s, "event: response.incomplete\n") ||
		strings.Contains(s, "event: response.incomplete\r\n") ||
		strings.Contains(s, `"type":"response.incomplete"`) ||
		strings.Contains(s, "event: message_stop\n") ||
		strings.Contains(s, "event: message_stop\r\n") ||
		strings.Contains(s, `"type":"message_stop"`) ||
		strings.Contains(s, `"finish_reason":"`) ||
		strings.Contains(s, `"finishReason":"`) ||
		strings.Contains(s, "data: [DONE]") ||
		strings.Contains(s, "data:[DONE]") {
		return true
	}
	for _, payload := range sseDataPayloads(event) {
		var envelope struct {
			Type         string `json:"type"`
			FinishReason string `json:"finishReason"`
			Choices      []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case "response.completed", "response.incomplete", "message_stop":
			return true
		}
		if envelope.FinishReason != "" {
			return true
		}
		for _, choice := range envelope.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				return true
			}
		}
	}
	return false
}

func streamHasFailureEvent(event []byte) bool {
	s := string(event)
	if strings.Contains(s, "event: response.failed\n") ||
		strings.Contains(s, "event: response.failed\r\n") ||
		strings.Contains(s, `"type":"response.failed"`) ||
		strings.Contains(s, `"object":"error"`) ||
		strings.Contains(s, "event: error\n") ||
		strings.Contains(s, "event: error\r\n") {
		return true
	}
	for _, payload := range sseDataPayloads(event) {
		var envelope struct {
			Type   string      `json:"type"`
			Object string      `json:"object"`
			Error  interface{} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		if envelope.Type == "response.failed" || envelope.Type == "error" || envelope.Object == "error" || envelope.Error != nil {
			return true
		}
	}
	return false
}

func sseDataPayloads(event []byte) []string {
	normalized := normalizeSSEEventForConverter(event)
	if len(normalized) == 0 {
		return nil
	}
	raw := strings.TrimPrefix(strings.TrimSpace(string(normalized)), "data:")
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[DONE]" {
		return nil
	}
	// normalizeSSEEventForConverter joins multi-line data payloads with \n.
	// Split them back into individual JSON payloads so that terminal/failure
	// event detection can inspect each one independently.
	lines := strings.Split(raw, "\n")
	var payloads []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && line != "[DONE]" {
			payloads = append(payloads, line)
		}
	}
	return payloads
}
