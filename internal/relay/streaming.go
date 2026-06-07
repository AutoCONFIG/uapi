package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/stream"
	"github.com/google/uuid"
)

const (
	sseInitialBufSize            = 8 * 1024
	sseMaxBufSize                = 10 * 1024 * 1024
	sseBootstrapMaxSkippedEvents = 16
)

type prependedReadCloser struct {
	prefix *bytes.Reader
	reader io.Reader
	closer io.Closer
}

func (r *prependedReadCloser) Read(p []byte) (int, error) {
	if r.prefix != nil && r.prefix.Len() > 0 {
		return r.prefix.Read(p)
	}
	return r.reader.Read(p)
}

func (r *prependedReadCloser) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

// streamResult carries streaming outcome from producer to main goroutine.
type streamResult struct {
	promptTokens     int
	completionTokens int
	err              error
	finalized        bool
	failed           bool
	emptyStream      bool
	parseFailed      bool // true if ParseStreamUsage had errors
}

// streamTracker tracks usage in real-time from SSE chunks.
type streamTracker struct {
	mu                  sync.Mutex
	promptTokens        int
	completionTokens    int
	cacheCreationTokens int
	cacheReadTokens     int
	hasPromptTokens     bool
	hasCompletionTokens bool
	firstPromptTokens   int // first non-zero prompt tokens observed
	hasFirstPrompt      bool
	estimatedOutput     int
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
	estimatedOutput := estimateStreamOutputTokens(dataLine)
	pt, ct, err := t.adaptor.ParseStreamUsage(dataLine)
	if err != nil {
		t.mu.Lock()
		t.estimatedOutput += estimatedOutput
		t.parseErrors++
		t.mu.Unlock()
		return
	}
	cacheCreationTokens, cacheReadTokens := extractStreamCacheTokens(dataLine)
	if pt > 0 || ct > 0 || estimatedOutput > 0 || cacheCreationTokens > 0 || cacheReadTokens > 0 {
		t.mu.Lock()
		t.estimatedOutput += estimatedOutput
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
		if cacheCreationTokens > 0 {
			t.cacheCreationTokens = cacheCreationTokens
		}
		if cacheReadTokens > 0 {
			t.cacheReadTokens = cacheReadTokens
		}
		t.mu.Unlock()
	}
}

func (t *streamTracker) EstimatedOutputTokens() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.estimatedOutput
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

func (t *streamTracker) CacheCreationTokens() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cacheCreationTokens
}

func (t *streamTracker) CacheReadTokens() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cacheReadTokens
}

func extractStreamCacheReadTokens(dataLine []byte) int {
	_, read := extractStreamCacheTokens(dataLine)
	return read
}

func extractStreamCacheCreationTokens(dataLine []byte) int {
	creation, _ := extractStreamCacheTokens(dataLine)
	return creation
}

func extractStreamCacheTokens(dataLine []byte) (int, int) {
	var root map[string]interface{}
	if err := json.Unmarshal(dataLine, &root); err != nil {
		return 0, 0
	}
	if usage, _ := root["usage"].(map[string]interface{}); usage != nil {
		if creation, read := usageCacheTokens(usage); creation > 0 || read > 0 {
			return creation, read
		}
	}
	if usageMetadata, _ := root["usageMetadata"].(map[string]interface{}); usageMetadata != nil {
		if creation, read := usageCacheTokens(usageMetadata); creation > 0 || read > 0 {
			return creation, read
		}
	}
	if response, _ := root["response"].(map[string]interface{}); response != nil {
		if usage, _ := response["usage"].(map[string]interface{}); usage != nil {
			if creation, read := usageCacheTokens(usage); creation > 0 || read > 0 {
				return creation, read
			}
		}
		if usageMetadata, _ := response["usageMetadata"].(map[string]interface{}); usageMetadata != nil {
			if creation, read := usageCacheTokens(usageMetadata); creation > 0 || read > 0 {
				return creation, read
			}
		}
	}
	return 0, 0
}

func usageCacheTokens(usage map[string]interface{}) (int, int) {
	creation := firstJSONInt(usage["cache_creation_input_tokens"], usage["cache_write_input_tokens"])
	if creation == 0 {
		creation = usageNestedCacheCreationTokens(usage["cache_creation"])
	}
	read := firstJSONInt(usage["cache_read_input_tokens"], usage["prompt_cache_hit_tokens"], usage["cached_tokens"])
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		if details, _ := usage[key].(map[string]interface{}); details != nil {
			if creation == 0 {
				creation = firstJSONInt(details["cached_write_tokens"], details["cache_creation_input_tokens"])
			}
			if read == 0 {
				read = firstJSONInt(details["cached_read_tokens"], details["cached_tokens"], details["cache_read_input_tokens"])
			}
		}
	}
	if read == 0 {
		read = jsonNumberToInt(usage["cachedContentTokenCount"])
	}
	return creation, read
}

func usageNestedCacheCreationTokens(value interface{}) int {
	nested, _ := value.(map[string]interface{})
	if nested == nil {
		return 0
	}
	total := 0
	for _, key := range []string{"ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"} {
		total += jsonNumberToInt(nested[key])
	}
	return total
}

func firstJSONInt(values ...interface{}) int {
	for _, value := range values {
		if parsed := jsonNumberToInt(value); parsed > 0 {
			return parsed
		}
	}
	return 0
}

func jsonNumberToInt(value interface{}) int {
	switch n := value.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func estimateStreamOutputTokens(dataLine []byte) int {
	var root map[string]interface{}
	if err := json.Unmarshal(dataLine, &root); err != nil {
		return 0
	}
	if choices, ok := root["choices"].([]interface{}); ok {
		total := 0
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]interface{})
			delta, _ := choice["delta"].(map[string]interface{})
			total += estimateJSONValueTokens(delta, false, "")
		}
		return total
	}
	if candidates, ok := root["candidates"].([]interface{}); ok {
		total := 0
		for _, rawCandidate := range candidates {
			candidate, _ := rawCandidate.(map[string]interface{})
			content, _ := candidate["content"].(map[string]interface{})
			total += estimateJSONValueTokens(content, false, "")
		}
		return total
	}
	eventType, _ := root["type"].(string)
	switch eventType {
	case "response.output_text.delta", "response.reasoning.delta", "response.function_call_arguments.delta":
		return estimateJSONValueTokens(root["delta"], true, "delta")
	case "content_block_delta":
		return estimateJSONValueTokens(root["delta"], false, "delta")
	}
	return 0
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
	return streamAndForwardWithTrace(bodyStream, reader, tracker, inputConvert, outputConvert, sendDone, nil)
}

func streamAndForwardWithTrace(
	bodyStream io.Reader,
	reader *SSEStreamReader,
	tracker *streamTracker,
	inputConvert func([]byte) []byte,
	outputConvert func([]byte) []byte,
	sendDone bool,
	trace *relayDebugTrace,
) streamResult {
	aborted := false
	defer func() {
		if !aborted {
			reader.Done()
		}
	}()
	closer, needClose := bodyStream.(io.Closer)
	var closedByDownstream atomic.Bool
	if needClose {
		done := make(chan struct{})
		go func() {
			select {
			case <-reader.Closed():
				closedByDownstream.Store(true)
				trace.Event("downstream_reader_closed_before_upstream_close")
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
		result := streamRawAndForward(scanner, reader, tracker, sendDone, trace, closedByDownstream.Load)
		if result.err != nil && !errors.Is(result.err, io.ErrClosedPipe) {
			aborted = true
		}
		return result
	}

	sawDone := false
	sawTerminal := false
	sawChatFinish := false
	failed := false
	nonDonePayloads := 0
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
		trace.StreamChunk("stream.upstream.sse", current)

		normalized := normalizeSSEEventForConverterWithEvent(current)
		if len(normalized) == 0 {
			return true, nil
		}
		for _, payload := range sseDataPayloads(normalized) {
			if strings.TrimSpace(payload) != "[DONE]" {
				nonDonePayloads++
			}
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
				trace.Event("stream_event_filtered",
					logger.F("mode", "converted"),
					logger.F("stage", "input_convert"),
					logger.F("normalized_bytes", len(normalized)),
					logger.F("sse", relayDebugSSEEventSummary(normalized)),
				)
				return true, nil // skip events the converter filters out
			}
			forwardLine = converted
		} else {
			forwardLine = normalized
			if len(forwardLine) == 0 {
				forwardLine = dataOnly
			}
		}
		trace.StreamChunk("stream.normalized.sse", forwardLine)
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
			for _, payload := range sseDataPayloads(segBytes) {
				tracker.TrackChunk([]byte(payload))
			}
		}

		// Stage 2: normalized OpenAI SSE → client format SSE (if needed)
		if outputConvert != nil {
			var out []byte
			for _, seg := range segments {
				converted := outputConvert(seg)
				if converted != nil {
					out = append(out, converted...)
				} else {
					trace.Event("stream_event_filtered",
						logger.F("mode", "converted"),
						logger.F("stage", "output_convert"),
						logger.F("segment_bytes", len(seg)),
						logger.F("sse", relayDebugSSEEventSummary(seg)),
					)
				}
			}
			if len(out) == 0 {
				trace.Event("stream_event_filtered",
					logger.F("mode", "converted"),
					logger.F("stage", "output_convert_all"),
					logger.F("segments", len(segments)),
				)
				return true, nil
			}
			forwardLine = out
		}
		trace.StreamChunk("stream.downstream.sse", forwardLine)
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
			trace.Event("downstream_send_closed",
				logger.F("mode", "converted"),
				logger.F("event_bytes", len(forwardLine)),
				logger.F("saw_done", sawDone),
				logger.F("saw_terminal", sawTerminal),
				logger.F("saw_chat_finish", sawChatFinish),
			)
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
		if closedByDownstream.Load() {
			trace.Event("scanner_closed_by_downstream",
				logger.F("mode", "converted"),
				logger.Err(err),
				logger.F("saw_done", sawDone),
				logger.F("saw_terminal", sawTerminal),
				logger.F("saw_chat_finish", sawChatFinish),
			)
			return streamResult{err: io.ErrClosedPipe}
		}
		if (sawDone || sawTerminal || sawChatFinish) && isBenignStreamCloseError(err) {
			trace.Event("scanner_benign_close_after_terminal",
				logger.F("mode", "converted"),
				logger.Err(err),
				logger.F("saw_done", sawDone),
				logger.F("saw_terminal", sawTerminal),
				logger.F("saw_chat_finish", sawChatFinish),
			)
			pt, ct, parseFailed := tracker.Result()
			return streamResult{promptTokens: pt, completionTokens: ct, finalized: true, failed: failed, emptyStream: nonDonePayloads == 0, parseFailed: parseFailed}
		}
		if isBenignStreamCloseError(err) {
			trace.Event("scanner_benign_close_before_terminal",
				logger.F("mode", "converted"),
				logger.Err(err),
				logger.F("saw_done", sawDone),
				logger.F("saw_terminal", sawTerminal),
				logger.F("saw_chat_finish", sawChatFinish),
			)
			return streamResult{err: io.ErrClosedPipe}
		}
		trace.Event("scanner_error",
			logger.F("mode", "converted"),
			logger.Err(err),
			logger.F("saw_done", sawDone),
			logger.F("saw_terminal", sawTerminal),
			logger.F("saw_chat_finish", sawChatFinish),
		)
		logger.Warnf("relay.sse", "scanner failed", logger.Err(err))
		aborted = true
		reader.Abort(err)
		return streamResult{err: err}
	}
	trace.Event("scanner_eof",
		streamDebugStateFields(trace, "converted", sawDone, sawTerminal, sawChatFinish, failed, len(event))...,
	)
	if ok, err := flush(); err != nil {
		return streamResult{err: err}
	} else if !ok {
		return streamResult{err: io.ErrClosedPipe}
	}

	if sendDone && !sawDone {
		ok := reader.SendDone()
		trace.Event("stream_done_injected",
			streamDebugStateFields(trace, "converted", sawDone, sawTerminal, sawChatFinish, failed, 0, logger.F("send_ok", ok))...,
		)
		sawDone = sawChatFinish || sawTerminal
	} else if !sendDone && !sawDone && outputConvert != nil && (sawTerminal || sawChatFinish) {
		if converted := outputConvert([]byte("data: [DONE]\n\n")); converted != nil {
			ok := reader.Send(converted)
			trace.Event("stream_done_converted_from_eof",
				streamDebugStateFields(trace, "converted", sawDone, sawTerminal, sawChatFinish, failed, 0, logger.F("send_ok", ok), logger.F("converted_bytes", len(converted)))...,
			)
			sawDone = true
		}
	} else if !sendDone && !sawDone {
		trace.Event("stream_eof_without_terminal",
			streamDebugStateFields(trace, "converted", sawDone, sawTerminal, sawChatFinish, failed, 0,
				logger.F("non_done_payloads", nonDonePayloads),
			)...,
		)
	}

	pt, ct, parseFailed := tracker.Result()
	emptyStream := nonDonePayloads == 0 && (sawDone || sawTerminal)
	result := streamResult{promptTokens: pt, completionTokens: ct, finalized: sawDone || sawTerminal, failed: failed, emptyStream: emptyStream, parseFailed: parseFailed}
	trace.Event("stream_forward_finished",
		streamDebugStateFields(trace, "converted", sawDone, sawTerminal, sawChatFinish, failed, 0,
			logger.F("finalized", result.finalized),
			logger.F("empty_stream", result.emptyStream),
			logger.F("non_done_payloads", nonDonePayloads),
			logger.F("prompt_tokens", pt),
			logger.F("completion_tokens", ct),
			logger.F("parse_failed", parseFailed),
		)...,
	)
	return result
}

func peekStreamBootstrapError(bodyStream io.Reader) (io.Reader, string, bool, error) {
	buffered := bufio.NewReaderSize(bodyStream, sseInitialBufSize)
	var prefix []byte
	closer, _ := bodyStream.(io.Closer)
	wrapped := func() io.Reader {
		return &prependedReadCloser{
			prefix: bytes.NewReader(prefix),
			reader: buffered,
			closer: closer,
		}
	}

	skippedEvents := 0
	for len(prefix) <= sseMaxBufSize {
		event, err := readNextSSEEvent(buffered)
		if len(event) > 0 {
			prefix = append(prefix, event...)
			normalized := normalizeSSEEventForConverterWithEvent(event)
			if len(normalized) > 0 {
				if message, ok := streamErrorMessage(normalized); ok {
					return wrapped(), message, true, nil
				}
				return wrapped(), "", false, nil
			}
			skippedEvents++
			if skippedEvents >= sseBootstrapMaxSkippedEvents {
				return wrapped(), "", false, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return wrapped(), "", false, nil
			}
			return wrapped(), "", false, err
		}
	}
	return wrapped(), "", false, errors.New("upstream stream bootstrap event too large")
}

func readNextSSEEvent(reader *bufio.Reader) ([]byte, error) {
	var event []byte
	for len(event) <= sseMaxBufSize {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			event = append(event, line...)
			if strings.TrimSpace(string(line)) == "" {
				return event, nil
			}
		}
		if err != nil {
			return event, err
		}
	}
	return event, errors.New("upstream stream bootstrap event too large")
}

func streamDebugStateFields(trace *relayDebugTrace, mode string, sawDone, sawTerminal, sawChatFinish, failed bool, openEventBytes int, extra ...logger.Field) []logger.Field {
	fields := []logger.Field{
		logger.F("mode", mode),
		logger.F("saw_done", sawDone),
		logger.F("saw_terminal", sawTerminal),
		logger.F("saw_chat_finish", sawChatFinish),
		logger.F("failed", failed),
		logger.F("open_event_bytes", openEventBytes),
		logger.F("streams", trace.StreamStates("stream.upstream.sse", "stream.normalized.sse", "stream.downstream.sse")),
		logger.F("stream_timing", trace.TimingState()),
	}
	fields = append(fields, extra...)
	return fields
}

func isBenignStreamCloseError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "response body closed") ||
		strings.Contains(msg, "body closed") ||
		strings.Contains(msg, "stream closed")
}

func newStreamConverterFunc(upstreamFormat, clientFormat provider.Format) func([]byte) []byte {
	if upstreamFormat == clientFormat {
		return nil
	}
	converter := stream.NewConverter(upstreamFormat, clientFormat)
	if converter == nil {
		return nil
	}
	finished := false
	return func(line []byte) []byte {
		if finished {
			return nil
		}
		if strings.TrimSpace(string(line)) == "data: [DONE]" {
			finished = true
			out := converter.Done()
			converter.Reset()
			return out
		}
		out := converter.Convert(line)
		if len(out) == 0 && clientFormat == provider.FormatOpenAIResponses {
			out = openAIResponsesFailedEventFromStreamError(line)
		}
		if streamHasTerminalEvent(out) || streamSawChatFinish(out) {
			finished = true
			converter.Reset()
		}
		return out
	}
}

func openAIResponsesFailedEventFromStreamError(event []byte) []byte {
	message, ok := streamErrorMessage(event)
	if !ok {
		return nil
	}
	respID := "resp_error_" + uuid.NewString()
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "response.failed",
		"response": map[string]interface{}{
			"id":     respID,
			"object": "response",
			"status": "failed",
			"error": map[string]interface{}{
				"type":    "upstream_error",
				"message": message,
			},
		},
	})
	return []byte("event: response.failed\ndata: " + string(payload) + "\n\n")
}

func streamErrorMessage(event []byte) (string, bool) {
	for _, payload := range sseDataPayloads(event) {
		var envelope struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		if envelope.Type != "error" && envelope.Error.Message == "" {
			continue
		}
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Error.Message)
		}
		if message == "" {
			message = "upstream stream error"
		}
		return message, true
	}
	return "", false
}

func streamErrorHTTPStatus(message string) int {
	for _, status := range []int{400, 401, 403, 404, 408, 409, 413, 429, 500, 502, 503, 504} {
		if strings.Contains(message, strconv.Itoa(status)) {
			return status
		}
	}
	return 0
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

func streamRawAndForward(scanner *bufio.Scanner, reader *SSEStreamReader, tracker *streamTracker, sendDone bool, trace *relayDebugTrace, closedByDownstream func() bool) streamResult {
	var event []byte
	sawDone := false
	sawTerminal := false
	sawChatFinish := false
	failed := false
	nonDonePayloads := 0

	flush := func() bool {
		if len(event) == 0 {
			return true
		}
		if len(event) < 2 || string(event[len(event)-2:]) != "\n\n" {
			event = append(event, '\n')
		}
		trace.StreamChunk("stream.upstream.sse", event)
		for _, payload := range sseDataPayloads(event) {
			if strings.TrimSpace(payload) != "[DONE]" {
				nonDonePayloads++
			}
			tracker.TrackChunk([]byte(payload))
		}
		trace.StreamChunk("stream.downstream.sse", event)
		ok := reader.Send(event)
		event = nil
		return ok
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		lineStr := strings.TrimSpace(string(line))
		if lineStr == "" {
			if !flush() {
				trace.Event("downstream_send_closed",
					logger.F("mode", "raw"),
					logger.F("saw_done", sawDone),
					logger.F("saw_terminal", sawTerminal),
					logger.F("saw_chat_finish", sawChatFinish),
				)
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
		if closedByDownstream != nil && closedByDownstream() {
			trace.Event("scanner_closed_by_downstream",
				logger.F("mode", "raw"),
				logger.Err(err),
				logger.F("saw_done", sawDone),
				logger.F("saw_terminal", sawTerminal),
				logger.F("saw_chat_finish", sawChatFinish),
			)
			return streamResult{err: io.ErrClosedPipe}
		}
		if (sawDone || sawTerminal || sawChatFinish) && isBenignStreamCloseError(err) {
			trace.Event("scanner_benign_close_after_terminal",
				logger.F("mode", "raw"),
				logger.Err(err),
				logger.F("saw_done", sawDone),
				logger.F("saw_terminal", sawTerminal),
				logger.F("saw_chat_finish", sawChatFinish),
			)
			pt, ct, parseFailed := tracker.Result()
			return streamResult{promptTokens: pt, completionTokens: ct, finalized: true, failed: failed, emptyStream: nonDonePayloads == 0, parseFailed: parseFailed}
		}
		trace.Event("scanner_error",
			logger.F("mode", "raw"),
			logger.Err(err),
			logger.F("saw_done", sawDone),
			logger.F("saw_terminal", sawTerminal),
			logger.F("saw_chat_finish", sawChatFinish),
		)
		logger.Warnf("relay.sse", "scanner failed", logger.Err(err))
		reader.Abort(err)
		return streamResult{err: err}
	}
	trace.Event("scanner_eof",
		streamDebugStateFields(trace, "raw", sawDone, sawTerminal, sawChatFinish, failed, len(event))...,
	)
	if !flush() {
		trace.Event("downstream_send_closed",
			streamDebugStateFields(trace, "raw", sawDone, sawTerminal, sawChatFinish, failed, 0)...,
		)
		return streamResult{err: io.ErrClosedPipe}
	}
	if sendDone && !sawDone {
		ok := reader.SendDone()
		trace.Event("stream_done_injected",
			streamDebugStateFields(trace, "raw", sawDone, sawTerminal, sawChatFinish, failed, 0, logger.F("send_ok", ok))...,
		)
		sawDone = sawChatFinish || sawTerminal
	}
	pt, ct, parseFailed := tracker.Result()
	finalized := sawDone || sawTerminal
	emptyStream := nonDonePayloads == 0 && finalized
	trace.Event("stream_forward_finished",
		streamDebugStateFields(trace, "raw", sawDone, sawTerminal, sawChatFinish, failed, 0,
			logger.F("finalized", finalized),
			logger.F("empty_stream", emptyStream),
			logger.F("non_done_payloads", nonDonePayloads),
			logger.F("prompt_tokens", pt),
			logger.F("completion_tokens", ct),
			logger.F("parse_failed", parseFailed),
		)...,
	)
	return streamResult{promptTokens: pt, completionTokens: ct, finalized: finalized, failed: failed, emptyStream: emptyStream, parseFailed: parseFailed}
}

func streamHasTerminalEvent(event []byte) bool {
	s := string(event)
	if strings.Contains(s, "event: response.completed\n") ||
		strings.Contains(s, "event: response.completed\r\n") ||
		strings.Contains(s, `"type":"response.completed"`) ||
		strings.Contains(s, "event: response.failed\n") ||
		strings.Contains(s, "event: response.failed\r\n") ||
		strings.Contains(s, `"type":"response.failed"`) ||
		strings.Contains(s, "event: response.incomplete\n") ||
		strings.Contains(s, "event: response.incomplete\r\n") ||
		strings.Contains(s, `"type":"response.incomplete"`) ||
		strings.Contains(s, "event: message_stop\n") ||
		strings.Contains(s, "event: message_stop\r\n") ||
		strings.Contains(s, `"type":"message_stop"`) ||
		strings.Contains(s, `"finish_reason":"`) ||
		strings.Contains(s, `"stop_reason":"`) ||
		strings.Contains(s, "data: [DONE]") ||
		strings.Contains(s, "data:[DONE]") {
		return true
	}
	for _, payload := range sseDataPayloads(event) {
		var envelope struct {
			Type         string `json:"type"`
			FinishReason string `json:"finishReason"`
			Candidates   []struct {
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case "response.completed", "response.failed", "response.incomplete", "message_stop":
			return true
		}
		if isTerminalGeminiFinishReason(envelope.FinishReason) {
			return true
		}
		for _, candidate := range envelope.Candidates {
			if isTerminalGeminiFinishReason(candidate.FinishReason) {
				return true
			}
		}
		if envelope.Delta.StopReason != "" {
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

func isTerminalGeminiFinishReason(reason string) bool {
	switch reason {
	case "", "NOT_STARTED", "FINISH_REASON_UNSPECIFIED":
		return false
	default:
		return true
	}
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
	lines := strings.Split(strings.TrimRight(string(event), "\n"), "\n")
	dataParts := make([]string, 0, len(lines))
	inData := false
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = strings.TrimPrefix(data, " ")
			}
			dataParts = append(dataParts, data)
			inData = true
			continue
		}
		if inData && !strings.HasPrefix(line, "event:") && strings.TrimSpace(line) != "" {
			dataParts = append(dataParts, line)
		}
	}
	if len(dataParts) == 0 {
		return nil
	}
	raw := strings.Join(dataParts, "\n")
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[DONE]" {
		return nil
	}
	return []string{raw}
}
