package httpclient

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/telemetry/trace"
)

func TestOptions(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if c.client.Timeout != 30*time.Second || c.transport.MaxConnsPerHost != 100 || c.transport.MaxIdleConns != 100 || c.transport.MaxIdleConnsPerHost != 10 {
		t.Fatal("unbounded or incorrect defaults")
	}
	other, _ := New(Options{})
	defer other.CloseIdleConnections()
	if other.transport == c.transport || c.transport == http.DefaultTransport {
		t.Fatal("shared transport")
	}
	for _, o := range []Options{{Timeout: -1}, {DialTimeout: -1}, {TLSHandshakeTimeout: -1}, {ResponseHeaderTimeout: -1}, {IdleConnTimeout: -1}, {MaxConnsPerHost: -1}, {MaxIdleConns: -1}, {MaxIdleConnsPerHost: -1}, {DisableTimeout: true, Timeout: time.Second}} {
		if _, err := New(o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	streaming, err := New(Options{DisableTimeout: true})
	if err != nil || streaming.client.Timeout != 0 {
		t.Fatal("streaming opt out", err)
	}
}

func TestTraceAndRedirect(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Seen", r.Header.Get(trace.HeaderTraceID))
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	c, _ := New(Options{})
	defer c.CloseIdleConnections()
	for _, tc := range []struct{ ctx, explicit, want string }{{"abc-1", "", "abc-1"}, {"abc", "mine", "mine"}, {"bad\nvalue", "", ""}, {strings.Repeat("a", 129), "", ""}, {"", "", ""}} {
		r, _ := http.NewRequestWithContext(trace.IntoContext(context.Background(), tc.ctx), http.MethodPost, s.URL, strings.NewReader("body"))
		if tc.explicit != "" {
			r.Header.Set(trace.HeaderTraceID, tc.explicit)
		}
		resp, err := c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 307 || resp.Header.Get("Seen") != tc.want {
			t.Fatalf("unexpected response: %+v", resp)
		}
		if r.Header.Get(trace.HeaderTraceID) != tc.explicit {
			t.Fatal("mutated caller")
		}
	}
	if calls.Load() != 5 {
		t.Fatal("redirect replay", calls.Load())
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	c, _ := New(Options{Timeout: 30 * time.Millisecond})
	defer c.CloseIdleConnections()
	r, _ := http.NewRequest(http.MethodGet, s.URL+"?secret=hidden", nil)
	_, err := c.Do(r)
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "hidden") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Do(r.WithContext(ctx))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestConnectionBound(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer s.Close()
	defer close(release)
	c, _ := New(Options{MaxConnsPerHost: 1})
	defer c.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := c.Do(r)
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	_, err := c.Do(r.WithContext(ctx2))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-entered:
		t.Fatal("connection limit exceeded")
	default:
	}
	cancel()
	<-done
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type body struct {
	io.Reader
	closed   bool
	closeErr error
}

func (b *body) Close() error { b.closed = true; return b.closeErr }

func TestJSON(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		code          int
		limit         int64
		output        bool
		wantErr       bool
	}{{"valid", `{"n":2}`, 200, 0, true, false}, {"trailing", `{} {}`, 200, 0, true, true}, {"oversize", `{"n":2}`, 200, 3, true, true}, {"discard bound", "12345", 200, 3, false, true}, {"discard", "abc", 200, 3, false, false}, {"empty", "", 204, 0, true, true}, {"empty discard", "", 204, 0, false, false}, {"status", "private response", 403, 0, true, true}, {"invalid", "secret", 200, 0, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			b := &body{Reader: strings.NewReader(tc.content)}
			d := doerFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Accept") != "application/json" || r.Body != nil {
					t.Fatal("request defaults")
				}
				return &http.Response{StatusCode: tc.code, Body: b}, nil
			})
			var out any
			var target any
			if tc.output {
				target = &out
			}
			err := DoJSON(context.Background(), d, "GET", "http://user:pass@example.test/?token=secret", nil, target, JSONOptions{MaxResponseBytes: tc.limit})
			if (err != nil) != tc.wantErr {
				t.Fatal(err)
			}
			if !b.closed {
				t.Fatal("body not closed")
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "pass")) {
				t.Fatal("leaked data", err)
			}
			if tc.code == 403 {
				var status *StatusError
				if !errors.As(err, &status) || status.StatusCode != 403 {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestJSONInputAndErrors(t *testing.T) {
	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		if string(data) != `{"n":1}` || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("input", string(data))
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := DoJSON(context.Background(), d, "POST", "http://example.test", map[string]int{"n": 1}, nil); err != nil {
		t.Fatal(err)
	}
	never := doerFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected Do"); return nil, nil })
	for _, opts := range [][]JSONOptions{{{MaxResponseBytes: -1}}, {{}, {}}} {
		if err := DoJSON(context.Background(), never, "GET", "http://example.test", nil, nil, opts...); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	if err := DoJSON(context.Background(), never, "GET", ":invalid-secret", nil, nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	if err := DoJSON(context.Background(), never, "GET", "http://example.test", make(chan int), nil); err == nil {
		t.Fatal("marshal error missing")
	}
	b := &body{Reader: strings.NewReader(""), closeErr: errors.New("secret")}
	err := DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 204, Body: b}, nil }), "GET", "http://example.test", nil, nil)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	err = DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) { return nil, context.Canceled }), "GET", "http://example.test", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRootCAsTrustPrivateAnchor(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	plain, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.CloseIdleConnections()
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	if _, err = plain.Do(request); err == nil {
		t.Fatal("self-signed anchor must not be trusted by default")
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	pinned, err := New(Options{RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.CloseIdleConnections()
	request, _ = http.NewRequest(http.MethodGet, server.URL, nil)
	response, err := pinned.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
}

func TestOnRequestObservesCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()
	events := make(chan RequestEvent, 2)
	client, err := New(Options{OnRequest: func(e RequestEvent) { events <- e }})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/x", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	select {
	case e := <-events:
		if e.Method != http.MethodGet || e.Status != http.StatusTeapot || e.Host == "" || e.Duration < 0 || e.Err != nil {
			t.Fatalf("%+v", e)
		}
	default:
		t.Fatal("completion event missing")
	}
	dead, _ := New(Options{OnRequest: func(e RequestEvent) { events <- e }})
	defer dead.CloseIdleConnections()
	dead.CloseIdleConnections()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	request, _ = http.NewRequest(http.MethodGet, closedURL, nil)
	_, _ = dead.Do(request)
	select {
	case e := <-events:
		if e.Status != 0 || e.Err == nil {
			t.Fatalf("%+v", e)
		}
	default:
		t.Fatal("failure event missing")
	}
}

func TestOnRequestObservesMissingRequestURL(t *testing.T) {
	var events []RequestEvent
	client, err := New(Options{OnRequest: func(e RequestEvent) { events = append(events, e) }})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	request := &http.Request{Method: http.MethodGet}
	response, err := client.Do(request)
	if response != nil || err == nil {
		t.Fatalf("response=%v error=%v", response, err)
	}
	if err.Error() != "httpclient: request failed" {
		t.Fatalf("unsanitized error: %v", err)
	}
	var cause *url.Error
	if !errors.As(err, &cause) {
		t.Fatalf("standard client error lost: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d completion events", len(events))
	}
	event := events[0]
	if event.Method != http.MethodGet || event.Host != "" || event.Status != 0 || event.Duration < 0 || event.Err == nil {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Err.Error() != err.Error() || !errors.Is(event.Err, cause) {
		t.Fatalf("observer error differs: %v", event.Err)
	}
	if request.URL != nil {
		t.Fatal("caller URL changed")
	}
}
