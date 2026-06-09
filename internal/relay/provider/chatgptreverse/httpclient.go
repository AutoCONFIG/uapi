package chatgptreverse

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/http2"
)

// utlsRoundTripper is an http.RoundTripper that uses utls for TLS fingerprint
// impersonation. It auto-negotiates HTTP/1.1 vs HTTP/2 based on ALPN.
type utlsRoundTripper struct {
	helloID    utls.ClientHelloID
	h1         *http.Transport
	h2         *http2.Transport
	dialer     *net.Dialer
	connMu     sync.Mutex
	connPool   map[string]net.Conn // host:port -> reusable conn (h2 only, simple pool)
}

func newUTLSRoundTripper(helloID utls.ClientHelloID) *utlsRoundTripper {
	rt := &utlsRoundTripper{
		helloID: helloID,
		dialer: &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		connPool: make(map[string]net.Conn),
	}
	rt.h2 = &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			return rt.dialTLS(ctx, network, addr, true)
		},
		AllowHTTP: false,
	}
	rt.h1 = &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return rt.dialTLS(ctx, network, addr, false)
		},
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   false,
	}
	return rt
}

func (rt *utlsRoundTripper) dialTLS(ctx context.Context, network, addr string, preferH2 bool) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	rawConn, err := rt.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	uConfig := &utls.Config{
		ServerName: host,
		NextProtos: []string{"h2", "http/1.1"},
	}
	uConn := utls.UClient(rawConn, uConfig, rt.helloID)
	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	negotiated := uConn.ConnectionState().NegotiatedProtocol
	if preferH2 && negotiated != "h2" {
		_ = uConn.Close()
		return nil, fmt.Errorf("h2 not negotiated, got %q", negotiated)
	}
	return uConn, nil
}

func (rt *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Probe ALPN by establishing a connection first
	host := req.URL.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	// Try HTTP/2 first; on failure (ALPN didn't negotiate h2), fall back to HTTP/1.1
	resp, err := rt.h2.RoundTrip(req)
	if err == nil {
		return resp, nil
	}
	// If error indicates h2 not negotiated, fall back to h1
	if strings.Contains(err.Error(), "h2 not negotiated") || strings.Contains(err.Error(), "http2:") {
		return rt.h1.RoundTrip(req)
	}
	return nil, err
}

func (rt *utlsRoundTripper) CloseIdleConnections() {
	rt.h1.CloseIdleConnections()
	rt.h2.CloseIdleConnections()
}

// newUTLSClient creates an *http.Client using utls to impersonate Edge browser TLS.
func newUTLSClient() *http.Client {
	rt := newUTLSRoundTripper(utls.HelloChrome_Auto)
	return &http.Client{
		Transport: rt,
		// No client-level timeout; per-request timeouts are applied via context.
		Timeout: 0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
}

// doHTTP executes a buffered HTTP request via the utls client and returns the
// status code and full response body.
func (a *Adaptor) doHTTP(method, url string, headers map[string]string, body []byte, timeout time.Duration) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, buf, nil
}

// doHTTPStream executes a streaming HTTP request via the utls client and returns
// the *http.Response with body open for streaming. Caller must close resp.Body.
func (a *Adaptor) doHTTPStream(method, url string, headers map[string]string, body []byte, timeout time.Duration) (*http.Response, error) {
	// For streaming, use a context with a long deadline (timeout serves as overall cap)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		cancel()
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	// Wrap body so cancel runs on Close
	resp.Body = &cancelingReadCloser{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelingReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelingReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// fasthttpHeadersToMap converts a fasthttp.RequestHeader to a map[string]string.
func fasthttpHeadersToMap(h *fasthttp.RequestHeader) map[string]string {
	m := make(map[string]string, h.Len())
	h.VisitAll(func(key, value []byte) {
		k := string(key)
		// Skip pseudo headers / hop-by-hop that net/http rejects or manages itself.
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") {
			return
		}
		m[k] = string(value)
	})
	return m
}

// DoHTTPRequest implements the framework's HTTPDoer interface. It takes a
// fully-prepared fasthttp.Request, executes it via the utls client, and pipes
// the streaming response back into fasthttp.Response for SSE forwarding.
func (a *Adaptor) DoHTTPRequest(req *fasthttp.Request, resp *fasthttp.Response) error {
	method := string(req.Header.Method())
	url := req.URI().String()
	headers := fasthttpHeadersToMap(&req.Header)
	body := append([]byte(nil), req.Body()...)

	httpResp, err := a.doHTTPStream(method, url, headers, body, 300*time.Second)
	if err != nil {
		return err
	}

	resp.SetStatusCode(httpResp.StatusCode)
	for k, vs := range httpResp.Header {
		for _, v := range vs {
			resp.Header.Set(k, v)
		}
	}
	// Pipe body for streaming. fasthttp will read chunks from this stream.
	resp.SetBodyStream(httpResp.Body, -1)
	return nil
}
