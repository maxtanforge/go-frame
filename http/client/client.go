// Package httpclient provides an owned outbound transport and bounded JSON calls.
package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/maxtanforge/go-frame/internal/isnil"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

// Options configures a private transport. Zero values select the documented
// defaults; negative durations and limits are invalid.
type Options struct {
	Timeout               time.Duration // Whole request, including body reads; default 30s.
	DialTimeout           time.Duration // Default 5s.
	TLSHandshakeTimeout   time.Duration // Default 5s.
	ResponseHeaderTimeout time.Duration // Default 10s.
	IdleConnTimeout       time.Duration // Default 90s.
	MaxConnsPerHost       int           // Default 100.
	MaxIdleConns          int           // Default 100.
	MaxIdleConnsPerHost   int           // Default 10.
	// DisableTimeout disables the whole-request timeout for streaming clients.
	// It cannot be combined with Timeout; callers must manage cancellation.
	DisableTimeout bool
	// RootCAs pins the TLS trust anchors; nil uses the system roots. Load the
	// pool from operator-controlled files only.
	RootCAs *x509.CertPool
	// OnRequest observes every completed exchange, including JSON helpers.
	// It must be fast and concurrency-safe. Duration covers the exchange up to
	// response headers; body reads owned by the caller are not included.
	OnRequest func(RequestEvent)
	// WrapTransport optionally decorates the owned, configured transport once.
	// It must return a nonnil RoundTripper. CloseIdleConnections retains ownership
	// of the underlying transport even when the decorator has no close method.
	WrapTransport func(http.RoundTripper) http.RoundTripper
}

// RequestEvent is one completed outbound exchange. Host comes from the request
// URL and stays bounded by the caller's endpoint set; Err is this package's
// sanitized error. Label discipline remains the caller's responsibility.
type RequestEvent struct {
	Method   string
	Host     string
	Status   int // 0 when the exchange never reached a response.
	Duration time.Duration
	Err      error
}

// Client owns its transport and is safe for concurrent use. Construct with New.
// It adds no application-level retries; net/http may transparently replay
// requests it considers replayable. Redirects are returned without following.
type Client struct {
	client    *http.Client
	transport *http.Transport
	onRequest func(RequestEvent)
}

// New validates options and creates a client without opening connections.
func New(o Options) (*Client, error) {
	durations := []*time.Duration{&o.Timeout, &o.DialTimeout, &o.TLSHandshakeTimeout, &o.ResponseHeaderTimeout, &o.IdleConnTimeout}
	defaults := []time.Duration{30 * time.Second, 5 * time.Second, 5 * time.Second, 10 * time.Second, 90 * time.Second}
	if o.DisableTimeout && o.Timeout != 0 {
		return nil, errors.New("httpclient: Timeout conflicts with DisableTimeout")
	}
	for i, p := range durations {
		if *p < 0 {
			return nil, errors.New("httpclient: negative timeout")
		}
		if *p == 0 {
			*p = defaults[i]
		}
	}
	limits := []*int{&o.MaxConnsPerHost, &o.MaxIdleConns, &o.MaxIdleConnsPerHost}
	limitDefaults := []int{100, 100, 10}
	for i, p := range limits {
		if *p < 0 {
			return nil, errors.New("httpclient: negative connection limit")
		}
		if *p == 0 {
			*p = limitDefaults[i]
		}
	}
	if o.DisableTimeout {
		o.Timeout = 0
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: o.DialTimeout, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxConnsPerHost: o.MaxConnsPerHost, MaxIdleConns: o.MaxIdleConns, MaxIdleConnsPerHost: o.MaxIdleConnsPerHost, IdleConnTimeout: o.IdleConnTimeout, TLSHandshakeTimeout: o.TLSHandshakeTimeout, ResponseHeaderTimeout: o.ResponseHeaderTimeout, ExpectContinueTimeout: time.Second, TLSClientConfig: &tls.Config{RootCAs: o.RootCAs}}
	var wrapped http.RoundTripper = transport
	if o.WrapTransport != nil {
		wrapped = o.WrapTransport(transport)
		if isnil.Check(wrapped) {
			transport.CloseIdleConnections()
			return nil, errors.New("httpclient: WrapTransport returned nil")
		}
	}
	return &Client{transport: transport, onRequest: o.OnRequest, client: &http.Client{Transport: wrapped, Timeout: o.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Do clones request metadata before propagating a valid context correlation ID.
// Any explicitly supplied trace header (including empty) is preserved. The body
// retains net/http ownership semantics and may be consumed and closed by Do.
// The caller owns the returned response body. Error text omits request URLs.
func (c *Client) Do(r *http.Request) (*http.Response, error) {
	if r == nil {
		return nil, errors.New("httpclient: nil request")
	}
	cloned := r.Clone(r.Context())
	if cloned.Header == nil {
		cloned.Header = make(http.Header)
	}
	explicit := false
	for name := range cloned.Header {
		if strings.EqualFold(name, trace.HeaderTraceID) {
			explicit = true
			break
		}
	}
	if tid := trace.FromContext(r.Context()); !explicit && trace.ValidID(tid) {
		cloned.Header.Set(trace.HeaderTraceID, tid)
	}
	start := time.Now()
	resp, err := c.client.Do(cloned)
	if err != nil {
		err = safeError(StageTransport, err)
	}
	if c.onRequest != nil {
		event := RequestEvent{Method: r.Method, Duration: time.Since(start)}
		if r.URL != nil {
			event.Host = r.URL.Host
		}
		if err != nil {
			event.Err = err
		} else {
			event.Status = resp.StatusCode
		}
		c.onRequest(event)
	}
	return resp, err
}

// CloseIdleConnections closes unused connections without interrupting active calls.
func (c *Client) CloseIdleConnections() { c.transport.CloseIdleConnections() }
