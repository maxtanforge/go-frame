package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/telemetry/trace"
	"github.com/stretchr/testify/require"
)

type countingReader struct{ read int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.read += len(p)
	return len(p), nil
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestJSONDefaultBoundAndStatusDrainBounded(t *testing.T) {
	for _, status := range []int{200, 500} {
		reader := &countingReader{}
		b := &body{Reader: reader}
		d := doerFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: status, Body: b}, nil })
		err := DoJSON(context.Background(), d, "GET", "http://example.test", nil, nil)
		if err == nil || !b.closed {
			t.Fatal("expected bounded failure and closure")
		}
		want := 8 << 10 // 非 2xx 有界排空：保住 keep-alive，又不读无限错误体
		if status == 200 {
			want = (1 << 20) + 1
		}
		if reader.read != want {
			t.Fatalf("read %d, want %d", reader.read, want)
		}
	}
}

func TestJSONCleanupAndReadFailures(t *testing.T) {
	cause := errors.New("private data")
	for _, tc := range []struct {
		name   string
		reader io.Reader
		doErr  error
	}{{"read", failingReader{cause}, nil}, {"Do and response", strings.NewReader(""), cause}} {
		t.Run(tc.name, func(t *testing.T) {
			b := &body{Reader: tc.reader}
			d := doerFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: b}, tc.doErr })
			err := DoJSON(context.Background(), d, "GET", "http://example.test", nil, nil)
			if !errors.Is(err, cause) || strings.Contains(err.Error(), "private") || !b.closed {
				t.Fatal(err, b.closed)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExplicitEmptyAndNoncanonicalTraceHeaders(t *testing.T) {
	c, _ := New(Options{})
	defer c.CloseIdleConnections()
	for _, name := range []string{"x-trace-id", "X-Trace-Id", "X-TRACE-ID"} {
		r, _ := http.NewRequestWithContext(trace.IntoContext(context.Background(), "from-context"), "GET", "http://example.test", nil)
		r.Header[name] = []string{""}
		c.client.Transport = roundTripFunc(func(sent *http.Request) (*http.Response, error) {
			if len(sent.Header) != 1 || sent.Header[name][0] != "" {
				t.Fatal("explicit header overwritten", sent.Header)
			}
			sent.Header.Set("Mutation", "private")
			return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
		})
		resp, err := c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if r.Header.Get("Mutation") != "" {
			t.Fatal("header alias")
		}
	}
}

func TestNon2xxNilBodyReturnsStatusError(t *testing.T) {
	// 自定义 Doer 忘造 Body 是测试桩常见形态；非 2xx 必须仍返回 StatusError 而非 panic。
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500}, nil
	})
	var status *StatusError
	require.NotPanics(t, func() {
		err := DoJSON(context.Background(), d, "GET", "http://example.test", nil, nil)
		errors.As(err, &status)
	})
	require.NotNil(t, status)
	require.Equal(t, 500, status.StatusCode)
}
