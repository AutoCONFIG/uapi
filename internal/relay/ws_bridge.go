package relay

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/logger"
	"github.com/AutoCONFIG/uapi/internal/relay/provider"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
	ws "github.com/fasthttp/websocket"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

// httpBridgeFallback handles a response.create event by converting it to an
// HTTP streaming request and bridging the SSE response back as WS messages.
func (h *WSHandler) httpBridgeFallback(
	sess *Session,
	msg []byte,
	ch *db.Channel,
	acc *db.Account,
	adaptor provider.Adaptor,
	creds string,
	model string,
	estTokens int,
	tokenPlanID uuid.UUID,
	start time.Time,
) bool {
	// 1. Convert WS response.create → HTTP request body
	body := wsCreateToHTTPBody(msg)

	// 2. Determine upstream format from channel config.
	// Channel Type + APIFormat are admin-configured; no need to guess.
	upstreamFormat := channelUpstreamFormat(ch)
	clientFormat := provider.FormatOpenAIResponses

	adaptor.Init(ch, acc)
	adaptor.SetRequestParams(model, true) // always stream in bridge mode
	convertedBody, err := provider.ConvertRequestWithAdaptor(clientFormat, upstreamFormat, body, adaptor)
	if err != nil {
		WriteWSErrorSession(sess, 400, "convert_error", "request conversion failed")
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		return false
	}

	// 4. Build and send upstream HTTP request
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()

	upstreamURL, err := adaptor.GetRequestURL("/v1/responses")
	if err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSErrorSession(sess, 500, "url_error", "build upstream url failed")
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		return false
	}

	// Refresh credentials (handles OAuth token expiry)
	creds, err = EnsureValidCredentials(acc, h.db)
	if err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSErrorSession(sess, 500, "cred_error", "credential refresh failed")
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		return false
	}

	upReq.SetRequestURI(upstreamURL)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(convertedBody)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSErrorSession(sess, 500, "header_error", "setup headers failed")
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		return false
	}

	// 5. Execute streaming request
	if err := streamingClient.Do(upReq, upResp); err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSErrorSession(sess, 502, "upstream_error", "upstream request failed")
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		return false
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		bodyCopy := make([]byte, len(upResp.Body()))
		copy(bodyCopy, upResp.Body())
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSErrorSession(sess, statusCode, "upstream_error", extractErrorMessage(bodyCopy))
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, statusCode)
		return false
	}

	// 5. Bridge SSE response → WS messages in a goroutine
	go h.bridgeSSEToWS(sess, upReq, upResp, adaptor, ch, acc, model, estTokens, tokenPlanID, start)
	return true
}

// bridgeSSEToWS reads SSE from upstream and forwards each event as a WS text message.
func (h *WSHandler) bridgeSSEToWS(
	sess *Session,
	upReq *fasthttp.Request,
	upResp *fasthttp.Response,
	adaptor provider.Adaptor,
	ch *db.Channel,
	acc *db.Account,
	model string,
	estTokens int,
	tokenPlanID uuid.UUID,
	start time.Time,
) {
	turnFinalized := false
	defer func() {
		sess.ReleaseTurn()
		if r := recover(); r != nil {
			logger.Default().Panic("relay.ws", "panic in SSE bridge", r, logger.F("session", sess.id))
		}
		if !turnFinalized {
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
			h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 500)
		}
	}()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	tracker := newStreamTracker(adaptor)

	bodyStream := upResp.BodyStream()
	if bodyStream == nil {
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 502)
		turnFinalized = true
		return
	}
	closer, _ := bodyStream.(io.Closer)
	if closer != nil {
		defer closer.Close()
	}

	scanner := bufio.NewScanner(bodyStream)
	scanner.Buffer(make([]byte, 0, 8*1024), 10*1024*1024)

	var inputConvert func([]byte) []byte
	upstreamFormat := channelUpstreamFormat(ch)
	if upstreamFormat != provider.FormatOpenAIChatCompletions && upstreamFormat != provider.FormatOpenAIResponses {
		inputConvert = adaptor.ConvertStreamLine
	}
	var responsesConvert func([]byte) []byte
	if upstreamFormat != provider.FormatOpenAIResponses {
		responsesConvert = openai.NewResponsesReverseStreamConverter()
	}
	completed := false
	interrupted := false
	failed := false
	responsesPromptTokens := 0
	responsesCompletionTokens := 0
	var event []byte

	processEvent := func(raw []byte) {
		if len(raw) == 0 || completed || interrupted || failed {
			return
		}
		normalized := normalizeSSEEventForConverterWithEvent(raw)
		if len(normalized) == 0 {
			return
		}

		var forwardLine []byte
		if inputConvert != nil {
			converted := inputConvert(normalized)
			if converted == nil {
				return
			}
			forwardLine = converted
		} else {
			forwardLine = normalized
		}

		forwardEvents := forwardLine
		if responsesConvert != nil {
			var convertedEvents []byte
			for _, seg := range splitSSEEvents(forwardLine) {
				converted := responsesConvert(seg)
				if len(converted) > 0 {
					convertedEvents = append(convertedEvents, converted...)
				}
			}
			if len(convertedEvents) == 0 {
				return
			}
			forwardEvents = convertedEvents
		}
		for _, event := range splitSSEEvents(forwardEvents) {
			data := sseDataPayload(event)
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				completed = true
				break
			}
			tracker.TrackChunk([]byte(data))
			if pt, ct := ParseResponsesUsage([]byte(data)); pt > 0 || ct > 0 {
				responsesPromptTokens = pt
				responsesCompletionTokens = ct
			}
			if err := sess.WriteMessage(ws.TextMessage, []byte(data)); err != nil {
				interrupted = true
				break
			}
			eventType := wsSSEEventType([]byte(data))
			if IsFailureTerminalEvent(eventType) {
				failed = true
				break
			}
			if IsSuccessfulTerminalEvent(eventType) {
				completed = true
			}
		}
	}

	for scanner.Scan() {
		if sess.IsClosed() {
			interrupted = true
			break
		}

		line := scanner.Bytes()
		lineStr := strings.TrimSpace(string(line))

		if lineStr == "" {
			processEvent(event)
			event = nil
			if completed || interrupted || failed {
				break
			}
			continue
		}

		event = append(event, line...)
		event = append(event, '\n')
	}
	if len(event) > 0 && !completed && !interrupted && !failed {
		processEvent(event)
		event = nil
	}
	if responsesConvert != nil && !completed && !interrupted && !failed {
		for _, doneEvent := range splitSSEEvents(responsesConvert([]byte("data: [DONE]\n\n"))) {
			data := sseDataPayload(doneEvent)
			if data == "" {
				continue
			}
			if err := sess.WriteMessage(ws.TextMessage, []byte(data)); err != nil {
				interrupted = true
				break
			}
			eventType := wsSSEEventType([]byte(data))
			if IsFailureTerminalEvent(eventType) {
				failed = true
				break
			}
			if IsSuccessfulTerminalEvent(eventType) {
				completed = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Component("relay.ws").Warn("SSE scanner error", logger.F("session", sess.id), logger.Err(err))
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 502)
		turnFinalized = true
		return
	}
	if failed {
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 502)
		turnFinalized = true
		return
	}
	if interrupted || !completed {
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 499)
		turnFinalized = true
		return
	}

	pt, ct, _ := tracker.Result()
	if pt == 0 && ct == 0 && (responsesPromptTokens > 0 || responsesCompletionTokens > 0) {
		pt, ct = responsesPromptTokens, responsesCompletionTokens
	}
	h.settleBilling(sess.tokenID, tokenPlanID, estTokens, pt, ct, model)
	if ch.AffinityTTL > 0 {
		h.relayer.affinity.Set(sess.tokenID, model, ch.ID.String(), ch.AffinityTTL)
	}
	h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, pt, ct, start, 200)
	turnFinalized = true
}

func wsSSEEventType(data []byte) string {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ""
	}
	return envelope.Type
}

// wsCreateToHTTPBody converts a WS response.create event to an HTTP request body.
// The client sends: {"type":"response.create","model":"...","input":[...],...}
// The HTTP endpoint expects just the request params with stream=true.
func wsCreateToHTTPBody(msg []byte) []byte {
	var evt WSResponseCreateEvent
	if err := json.Unmarshal(msg, &evt); err != nil {
		return msg
	}

	bodyMap := make(map[string]interface{})
	bodyMap["model"] = evt.Model
	bodyMap["stream"] = true
	if evt.Input != nil {
		bodyMap["input"] = json.RawMessage(evt.Input)
	}
	if evt.Instructions != "" {
		bodyMap["instructions"] = evt.Instructions
	}
	if evt.MaxOutputTokens > 0 {
		bodyMap["max_output_tokens"] = evt.MaxOutputTokens
	}
	if evt.Temperature != nil {
		bodyMap["temperature"] = *evt.Temperature
	}
	if evt.TopP != nil {
		bodyMap["top_p"] = *evt.TopP
	}
	if evt.Tools != nil {
		bodyMap["tools"] = json.RawMessage(evt.Tools)
	}
	if evt.ToolChoice != nil {
		bodyMap["tool_choice"] = json.RawMessage(evt.ToolChoice)
	}
	if evt.PreviousResponseID != "" {
		bodyMap["previous_response_id"] = evt.PreviousResponseID
	}
	if evt.Store != nil {
		bodyMap["store"] = *evt.Store
	}
	if evt.Metadata != nil {
		bodyMap["metadata"] = json.RawMessage(evt.Metadata)
	}

	data, _ := json.Marshal(bodyMap)
	return data
}

// adaptorUpstreamFormat returns the upstream API format based on channel config.
// This uses the channel's Type and APIFormat rather than guessing —
// the admin configures these explicitly when creating channels.
func channelUpstreamFormat(ch *db.Channel) provider.Format {
	switch ch.Type {
	case "openai":
		if ch.APIFormat == "responses" || ch.APIFormat == "codex" {
			return provider.FormatOpenAIResponses
		}
		return provider.FormatOpenAIChatCompletions
	case "anthropic":
		return provider.FormatAnthropic
	case "gemini":
		return provider.FormatGemini
	case "antigravity":
		return provider.FormatAntigravity
	default:
		return provider.FormatOpenAIChatCompletions
	}
}

func sseDataPayload(event []byte) string {
	lines := strings.Split(strings.TrimRight(string(event), "\n"), "\n")
	var data []string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			part := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(part, " ") {
				part = strings.TrimPrefix(part, " ")
			}
			data = append(data, part)
		}
	}
	return strings.Join(data, "\n")
}

// extractErrorMessage tries to extract an error message from an upstream error response.
func extractErrorMessage(body []byte) string {
	var errResp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(body, &errResp) == nil {
		if errResp.Error != nil && errResp.Error.Message != "" {
			return errResp.Error.Message
		}
		if errResp.Message != "" {
			return errResp.Message
		}
		if errResp.Detail != "" {
			return errResp.Detail
		}
	}
	return "upstream error"
}
