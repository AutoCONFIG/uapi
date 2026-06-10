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
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
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
	body := wsCreateToHTTPBody(msg)
	currentAccount := acc
	currentCreds := creds
	transientExcluded := make(map[string]bool)
	maxAttempts := h.relayer.accountAttemptLimit(ch)
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		releaseAccountAttempt := h.relayer.beginAccountAttempt(ch, currentAccount, nil, "ws_bridge")
		adaptor.Init(ch, currentAccount)
		adaptor.SetRequestParams(model, true) // always stream in bridge mode

		convertedBody, err := convertWSHTTPBridgeRequestBody(ch, currentAccount, adaptor, msg, sess.id)
		if err != nil {
			releaseAccountAttempt()
			WriteWSErrorSession(sess, 400, "convert_error", "request conversion failed")
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			return false
		}

		upReq := fasthttp.AcquireRequest()
		upResp := fasthttp.AcquireResponse()

		upstreamURL, err := adaptor.GetRequestURL("/v1/responses")
		if err != nil {
			releaseAccountAttempt()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			WriteWSErrorSession(sess, 500, "url_error", "build upstream url failed")
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			return false
		}

		currentCreds, err = h.relayer.ensureCredentials(ch, currentAccount)
		if err != nil {
			releaseAccountAttempt()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			WriteWSErrorSession(sess, 500, "cred_error", "credential refresh failed")
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			return false
		}

		upReq.SetRequestURI(upstreamURL)
		upReq.Header.SetMethodBytes([]byte("POST"))
		upReq.SetBody(convertedBody)
		if err := adaptor.SetupRequestHeader(upReq, currentCreds); err != nil {
			releaseAccountAttempt()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			WriteWSErrorSession(sess, 500, "header_error", "setup headers failed")
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			return false
		}
		applyCodexMetadataHeaders(upReq, channelUpstreamFormat(ch), convertedBody)

		if err := doStreamingRequest(upReq, upResp); err != nil {
			releaseAccountAttempt()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			WriteWSErrorSession(sess, 502, "upstream_error", "upstream request failed")
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			return false
		}

		statusCode := upResp.StatusCode()
		if statusCode >= 400 {
			bodyCopy := readUpstreamErrorBody(upResp)
			releaseAccountAttempt()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			errClass := ClassifyUpstreamError(statusCode, bodyCopy)
			if errClass == ErrServerSide || errClass == ErrConfigSide {
				h.relayer.prepareChannelFailover(ch, statusCode, bodyCopy, model)
			}
			if (errClass == ErrAccountSide || errClass == ErrAccountTerminal) && attempt < maxAttempts-1 {
				failoverReason := errClass.String()
				h.relayer.prepareAccountFailover(ch, currentAccount, statusCode, bodyCopy, isUpstreamQuotaExhausted(statusCode, bodyCopy))
				transientExcluded = addExcludedAccount(transientExcluded, currentAccount)
				next := h.relayer.pickNextForModelExcluding(ch, poolFromChannel(h.relayer.pools, ch), model, transientExcluded)
				if next != nil {
					logger.Debugf("relay.ws", "bridge switching account after upstream account error",
						logger.F("channel_id", ch.ID.String()),
						logger.F("from_account_id", currentAccount.ID.String()),
						logger.F("account_id", next.ID.String()),
						logger.F("status", statusCode),
						logger.F("reason", failoverReason),
					)
					currentAccount = next
					continue
				}
			}
			WriteWSErrorSession(sess, statusCode, "upstream_error", extractErrorMessage(bodyCopy))
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			h.writeWSLog(sess.tokenID, ch.ID, currentAccount.ID, model, 0, 0, start, statusCode)
			return false
		}

		bodyStream := upResp.BodyStream()
		if bodyStream == nil {
			releaseAccountAttempt()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			WriteWSErrorSession(sess, 502, "upstream_error", "upstream stream body missing")
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			h.writeWSLog(sess.tokenID, ch.ID, currentAccount.ID, model, 0, 0, start, 502)
			return false
		}
		peekedBodyStream, bootstrapMessage, bootstrapFailed, peekErr := peekStreamBootstrapError(bodyStream)
		if peekErr != nil {
			releaseAccountAttempt()
			if closer, ok := peekedBodyStream.(io.Closer); ok {
				_ = closer.Close()
			}
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			WriteWSErrorSession(sess, 502, "upstream_error", "read upstream stream failed")
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			h.writeWSLog(sess.tokenID, ch.ID, currentAccount.ID, model, 0, 0, start, 502)
			return false
		}
		if bootstrapFailed {
			bootstrapStatus := streamErrorHTTPStatus(bootstrapMessage)
			if bootstrapStatus == 0 {
				bootstrapStatus = fasthttp.StatusBadGateway
			}
			bodyCopy := formatOpenAIError(bootstrapMessage)
			errClass := ClassifyUpstreamError(bootstrapStatus, bodyCopy)
			if errClass == ErrServerSide || errClass == ErrConfigSide {
				h.relayer.prepareChannelFailover(ch, bootstrapStatus, bodyCopy, model)
			}
			if (errClass == ErrAccountSide || errClass == ErrAccountTerminal) && attempt < maxAttempts-1 {
				failoverReason := errClass.String()
				h.relayer.prepareAccountFailover(ch, currentAccount, bootstrapStatus, bodyCopy, isUpstreamQuotaExhausted(bootstrapStatus, bodyCopy))
				transientExcluded = addExcludedAccount(transientExcluded, currentAccount)
				next := h.relayer.pickNextForModelExcluding(ch, poolFromChannel(h.relayer.pools, ch), model, transientExcluded)
				if next != nil {
					logger.Debugf("relay.ws", "bridge switching account after stream bootstrap error",
						logger.F("channel_id", ch.ID.String()),
						logger.F("from_account_id", currentAccount.ID.String()),
						logger.F("account_id", next.ID.String()),
						logger.F("status", bootstrapStatus),
						logger.F("reason", failoverReason),
					)
					if closer, ok := peekedBodyStream.(io.Closer); ok {
						_ = closer.Close()
					}
					releaseAccountAttempt()
					fasthttp.ReleaseRequest(upReq)
					fasthttp.ReleaseResponse(upResp)
					currentAccount = next
					continue
				}
			}
			if closer, ok := peekedBodyStream.(io.Closer); ok {
				_ = closer.Close()
			}
			releaseAccountAttempt()
			fasthttp.ReleaseRequest(upReq)
			fasthttp.ReleaseResponse(upResp)
			WriteWSErrorSession(sess, fasthttp.StatusBadGateway, "upstream_error", bootstrapMessage)
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			h.writeWSLog(sess.tokenID, ch.ID, currentAccount.ID, model, 0, 0, start, fasthttp.StatusBadGateway)
			return false
		}

		go h.bridgeSSEToWS(sess, upReq, upResp, peekedBodyStream, adaptor, ch, currentAccount, model, estTokens, tokenPlanID, start, body, releaseAccountAttempt)
		return true
	}

	WriteWSErrorSession(sess, 503, "upstream_error", "all accounts exhausted")
	h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
	h.writeWSLog(sess.tokenID, ch.ID, currentAccount.ID, model, 0, 0, start, 503)
	return false
}

func convertWSHTTPBridgeRequestBody(ch *db.Channel, acc *db.Account, adaptor provider.Adaptor, msg []byte, seedScope ...string) ([]byte, error) {
	body := wsCreateToHTTPBody(msg)
	upstreamFormat := channelUpstreamFormat(ch)
	clientFormat := provider.FormatOpenAIResponses
	var convertedBody []byte
	var err error
	if clientFormat == upstreamFormat {
		convertedBody, err = provider.NormalizeRequestSameProtocol(upstreamFormat, body)
	} else {
		convertedBody, err = provider.ConvertRequestWithAdaptor(clientFormat, upstreamFormat, body, adaptor)
	}
	if err != nil {
		return nil, err
	}
	if isCodexAPIFormat(ch.APIFormat) && upstreamFormat == provider.FormatCodexResponses {
		convertedBody = normalizeCodexResponsesRequest(convertedBody, true, codexClientMetadataSeed(ch, acc, firstString(seedScope...)))
	}
	bodyWithCachePolicy, _, policyErr := upstreamconfig.ApplyCachePassthroughPolicy(ch, upstreamFormat, convertedBody)
	if policyErr != nil {
		return nil, policyErr
	}
	return bodyWithCachePolicy, nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// bridgeSSEToWS reads SSE from upstream and forwards each event as a WS text message.
func (h *WSHandler) bridgeSSEToWS(
	sess *Session,
	upReq *fasthttp.Request,
	upResp *fasthttp.Response,
	bodyStream io.Reader,
	adaptor provider.Adaptor,
	ch *db.Channel,
	acc *db.Account,
	model string,
	estTokens int,
	tokenPlanID uuid.UUID,
	start time.Time,
	requestBody []byte,
	releaseAccountAttempt func(),
) {
	turnFinalized := false
	defer func() {
		if releaseAccountAttempt != nil {
			releaseAccountAttempt()
		}
		sess.ReleaseTurn()
		if r := recover(); r != nil {
			logger.Default().Panic("relay.ws", "panic in SSE bridge", r, logger.F("session", sess.id))
		}
		if !turnFinalized {
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 500)
		}
	}()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	tracker := newStreamTracker(adaptor)

	if bodyStream == nil {
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
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

	upstreamFormat := channelUpstreamFormat(ch)
	var inputConvert func([]byte) []byte
	responsesConvert := newStreamConverterFunc(upstreamFormat, provider.FormatOpenAIResponses)
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
			if h.toolCalls != nil {
				h.toolCalls.RecordResponseEvent(sess.id, []byte(data))
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
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 502)
		turnFinalized = true
		return
	}
	if failed {
		h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 502)
		turnFinalized = true
		return
	}
	if interrupted || !completed {
		pt, ct, _ := tracker.Result()
		cacheCreationTokens := tracker.CacheCreationTokens()
		cacheReadTokens := tracker.CacheReadTokens()
		estimatedOutputTokens := tracker.EstimatedOutputTokens()
		if pt > 0 || ct > 0 || cacheCreationTokens > 0 || cacheReadTokens > 0 || estimatedOutputTokens > 0 {
			estimateMissingUsage(&pt, &ct, requestBody, nil, estimatedOutputTokens)
			h.settleBilling(sess.tokenID, tokenPlanID, estTokens, pt, ct, model, cacheCreationTokens, cacheReadTokens)
			h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, pt, ct, start, 499, cacheCreationTokens, cacheReadTokens)
		} else {
			h.refundBilling(sess.tokenID, tokenPlanID, estTokens, model)
			h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, 499)
		}
		turnFinalized = true
		return
	}

	pt, ct, _ := tracker.Result()
	cacheCreationTokens := tracker.CacheCreationTokens()
	cacheReadTokens := tracker.CacheReadTokens()
	if pt == 0 && ct == 0 && (responsesPromptTokens > 0 || responsesCompletionTokens > 0) {
		pt, ct = responsesPromptTokens, responsesCompletionTokens
	}
	estimateMissingUsage(&pt, &ct, requestBody, nil, tracker.EstimatedOutputTokens())
	h.settleBilling(sess.tokenID, tokenPlanID, estTokens, pt, ct, model, cacheCreationTokens, cacheReadTokens)
	if ch.AffinityTTL > 0 {
		h.relayer.affinity.Set(sess.tokenID, model, sess.id, ch.ID.String(), acc.ID.String(), ch.AffinityTTL)
	}
	h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, pt, ct, start, 200, cacheCreationTokens, cacheReadTokens)
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
	var bodyMap map[string]interface{}
	if err := provider.DecodeJSONUseNumber(msg, &bodyMap); err != nil {
		return msg
	}
	if nested, _ := bodyMap["response"].(map[string]interface{}); nested != nil {
		bodyMap = nested
	}
	delete(bodyMap, "type")
	delete(bodyMap, "event_id")
	delete(bodyMap, "response")
	bodyMap["stream"] = true

	data, _ := json.Marshal(bodyMap)
	return cleanJSONUndefinedPlaceholders(data)
}

// wsCreateToNativeMessage converts a downstream response.create payload to the
// official Responses WebSocket create event. The WS protocol carries
// type=response.create but does not need HTTP-only stream/background flags.
func wsCreateToNativeMessage(msg []byte) []byte {
	var bodyMap map[string]interface{}
	if err := provider.DecodeJSONUseNumber(msg, &bodyMap); err != nil {
		return cleanJSONUndefinedPlaceholders(msg)
	}
	if nested, _ := bodyMap["response"].(map[string]interface{}); nested != nil {
		flattened := make(map[string]interface{}, len(nested)+2)
		for key, value := range nested {
			flattened[key] = value
		}
		if eventID, ok := bodyMap["event_id"]; ok {
			flattened["event_id"] = eventID
		}
		bodyMap = flattened
	}
	bodyMap["type"] = WSEventResponseCreate
	delete(bodyMap, "stream")
	delete(bodyMap, "background")
	delete(bodyMap, "response")

	data, _ := json.Marshal(bodyMap)
	return cleanJSONUndefinedPlaceholders(data)
}

// adaptorUpstreamFormat returns the upstream API format based on channel config.
// This uses the channel's Type and APIFormat rather than guessing —
// the admin configures these explicitly when creating channels.
func channelUpstreamFormat(ch *db.Channel) provider.Format {
	switch ch.Type {
	case "openai":
		if ch.APIFormat == "codex" {
			return provider.FormatCodexResponses
		}
		if ch.APIFormat == "responses" {
			return provider.FormatOpenAIResponses
		}
		return provider.FormatOpenAIChatCompletions
	case "anthropic":
		if ch.APIFormat == "claude_code" {
			return provider.FormatClaudeCode
		}
		return provider.FormatAnthropic
	case "gemini":
		if ch.APIFormat == "gemini_code" {
			return provider.FormatGeminiCode
		}
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
