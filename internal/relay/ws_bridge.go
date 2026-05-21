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
	ws "github.com/fasthttp/websocket"
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
	start time.Time,
) {
	// 1. Convert WS response.create → HTTP request body
	body := wsCreateToHTTPBody(msg)

	// 2. Determine upstream format from channel config.
	// Channel Type + APIFormat are admin-configured; no need to guess.
	upstreamFormat := channelUpstreamFormat(ch)
	clientFormat := provider.FormatOpenAIResp

	adaptor.Init(ch, acc)
	adaptor.SetRequestParams(model, true) // always stream in bridge mode
	convertedBody, err := provider.ConvertRequestWithAdaptor(clientFormat, upstreamFormat, body, adaptor)
	if err != nil {
		WriteWSError(sess.clientConn, 400, "convert_error", "request conversion failed")
		h.refundBilling(sess.tokenID, estTokens)
		return
	}

	// 4. Build and send upstream HTTP request
	upReq := fasthttp.AcquireRequest()
	upResp := fasthttp.AcquireResponse()

	upstreamURL, err := adaptor.GetRequestURL("/v1/responses")
	if err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSError(sess.clientConn, 500, "url_error", "build upstream url failed")
		h.refundBilling(sess.tokenID, estTokens)
		return
	}

	// Refresh credentials (handles OAuth token expiry)
	creds, err = EnsureValidCredentials(acc, h.db)
	if err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSError(sess.clientConn, 500, "cred_error", "credential refresh failed")
		h.refundBilling(sess.tokenID, estTokens)
		return
	}

	upReq.SetRequestURI(upstreamURL)
	upReq.Header.SetMethodBytes([]byte("POST"))
	upReq.SetBody(convertedBody)
	if err := adaptor.SetupRequestHeader(upReq, creds); err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSError(sess.clientConn, 500, "header_error", "setup headers failed")
		h.refundBilling(sess.tokenID, estTokens)
		return
	}

	// 5. Execute streaming request
	if err := streamingClient.Do(upReq, upResp); err != nil {
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSError(sess.clientConn, 502, "upstream_error", "upstream request failed")
		h.refundBilling(sess.tokenID, estTokens)
		return
	}

	statusCode := upResp.StatusCode()
	if statusCode >= 400 {
		bodyCopy := make([]byte, len(upResp.Body()))
		copy(bodyCopy, upResp.Body())
		fasthttp.ReleaseRequest(upReq)
		fasthttp.ReleaseResponse(upResp)
		WriteWSError(sess.clientConn, statusCode, "upstream_error", extractErrorMessage(bodyCopy))
		h.refundBilling(sess.tokenID, estTokens)
		h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, 0, 0, start, statusCode)
		return
	}

	// 5. Bridge SSE response → WS messages in a goroutine
	go h.bridgeSSEToWS(sess, upReq, upResp, adaptor, ch, acc, model, estTokens, start)
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
	start time.Time,
) {
	defer func() {
		if r := recover(); r != nil {
			logger.Default().Panic("relay.ws", "panic in SSE bridge", r, logger.F("session", sess.id))
		}
	}()
	defer fasthttp.ReleaseRequest(upReq)
	defer fasthttp.ReleaseResponse(upResp)

	tracker := newStreamTracker(adaptor)

	bodyStream := upResp.BodyStream()
	if bodyStream == nil {
		h.refundBilling(sess.tokenID, estTokens)
		return
	}
	closer, _ := bodyStream.(io.Closer)
	if closer != nil {
		defer closer.Close()
	}

	scanner := bufio.NewScanner(bodyStream)
	scanner.Buffer(make([]byte, 0, 8*1024), 10*1024*1024)

	var inputConvert func([]byte) []byte
	if adaptor.GetChannelType() != "openai" {
		inputConvert = adaptor.ConvertStreamLine
	}

	for scanner.Scan() {
		if sess.IsClosed() {
			break
		}

		line := scanner.Bytes()
		lineStr := strings.TrimSpace(string(line))

		if lineStr == "" {
			continue
		}

		// Convert upstream SSE → OpenAI SSE if needed
		var forwardLine []byte
		if inputConvert != nil {
			converted := inputConvert(line)
			if converted == nil {
				continue
			}
			forwardLine = converted
		} else {
			forwardLine = []byte(lineStr)
		}

		// Extract SSE data payload and forward as WS text message
		dataStr := string(forwardLine)
		if strings.HasPrefix(dataStr, "data: ") {
			data := strings.TrimPrefix(dataStr, "data: ")
			if data == "[DONE]" {
				break
			}

			// Track usage from SSE events
			tracker.TrackChunk([]byte(data))

			// Forward as WS text message
			if err := sess.WriteMessage(ws.TextMessage, []byte(data)); err != nil {
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Component("relay.ws").Warn("SSE scanner error", logger.F("session", sess.id), logger.Err(err))
	}

	pt, ct := tracker.Result()
	h.settleBilling(sess.tokenID, estTokens, pt, ct, model)
	if ch.AffinityTTL > 0 {
		h.relayer.affinity.Set(sess.tokenID, model, ch.ID.String(), ch.AffinityTTL)
	}
	h.writeWSLog(sess.tokenID, ch.ID, acc.ID, model, pt, ct, start, 200)
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
		if ch.APIFormat == "responses" {
			return provider.FormatOpenAIResp
		}
		return provider.FormatOpenAIChat
	case "anthropic":
		return provider.FormatAnthropic
	case "gemini":
		return provider.FormatGemini
	default:
		return provider.FormatOpenAIChat
	}
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
