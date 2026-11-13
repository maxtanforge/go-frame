package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	httpclient "github.com/maxtanforge/go-frame/http/client"
	"github.com/stretchr/testify/require"
)

type doFunc func(*http.Request) (*http.Response, error)

func (f doFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}
func TestEnvelopeAndObservation(t *testing.T) {
	for _, tc := range []struct {
		body, outcome string
		allowed       bool
		bad           bool
	}{
		{`{"success":true,"data":{"allowed":true}}`, "allowed", true, false},
		{`{"success":true,"data":{"allowed":false}}`, "denied", false, false},
		{`{"success":false,"message":"secret"}`, "rejected", false, true},
		{`{}`, "protocol", false, true}, {`{"data":{"allowed":true}}`, "protocol", false, true},
		{`{"success":true}`, "protocol", false, true}, {`{"success":true,"data":null}`, "protocol", false, true},
		{`{"success":true,"data":{}}`, "protocol", false, true}, {`{"success":true,"data":{"allowed":null}}`, "protocol", false, true},
		{`{"success":"yes","data":{"allowed":true}}`, "protocol", false, true}, {`invalid secret`, "protocol", false, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var events []Event
			c, err := New("https://permission.example", doFunc(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "/permissions/profile/edit", r.URL.Path)
				return response(tc.body), nil
			}), func(context.Context) (string, error) { return "token", nil }, func(e Event) { events = append(events, e) })
			require.NoError(t, err)
			allowed, err := c.CanEdit(context.Background(), "alice", "bob")
			require.Equal(t, tc.allowed, allowed)
			if tc.bad {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
			} else {
				require.NoError(t, err)
			}
			require.Len(t, events, 1)
			require.Equal(t, "profile.permission", events[0].Operation)
			require.Equal(t, tc.outcome, events[0].Outcome)
			require.GreaterOrEqual(t, events[0].Duration, time.Duration(0))
		})
	}
}

type failingBody struct {
	readErr, closeErr error
	delay             time.Duration
}

func (b failingBody) Read([]byte) (int, error) { time.Sleep(b.delay); return 0, b.readErr }
func (b failingBody) Close() error             { return b.closeErr }

type closeFailure struct {
	io.Reader
	err error
}

func (b closeFailure) Close() error { return b.err }
func TestFailureClassification(t *testing.T) {
	secret := errors.New("secret URL or credential")
	for _, tc := range []struct {
		name     string
		do       doFunc
		tokenErr error
		outcome  string
		cause    error
	}{
		{"credentials", nil, secret, "credentials", secret},
		{"transport", func(*http.Request) (*http.Response, error) { return nil, secret }, nil, "transport", secret},
		{"deadline", func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded }, nil, "timeout", context.DeadlineExceeded},
		{"canceled", func(*http.Request) (*http.Response, error) { return nil, context.Canceled }, nil, "canceled", context.Canceled},
		{"HTTP", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("secret"))}, nil
		}, nil, "http", nil},
		{"read", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: failingBody{readErr: secret}}, nil
		}, nil, "read", secret},
		{"close", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: closeFailure{strings.NewReader(`{"success":true,"data":{"allowed":true}}`), secret}}, nil
		}, nil, "close", secret},
		{"decode and close", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: failingBody{readErr: io.EOF, closeErr: secret}}, nil
		}, nil, "protocol", secret},
		{"bounded", func(*http.Request) (*http.Response, error) { return response(strings.Repeat("x", (64<<10)+1)), nil }, nil, "body_limit", httpclient.ErrResponseTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event Event
			d := tc.do
			if d == nil {
				d = func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil }
			}
			c, err := New("https://permission.example", d, func(context.Context) (string, error) { return "token", tc.tokenErr }, func(e Event) { event = e })
			require.NoError(t, err)
			allowed, err := c.CanEdit(context.Background(), "alice", "bob")
			require.False(t, allowed)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}
			require.Equal(t, tc.outcome, event.Outcome)
		})
	}
}

type tokenKey struct{}

func TestConcurrentRequestCredentialsAndContextIsolation(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	c, err := New("https://permission.example", doFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			ActorID  string `json:"actor_id"`
			TargetID string `json:"target_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		token := r.Context().Value(tokenKey{}).(string)
		mu.Lock()
		seen[body.ActorID] = r.Header.Get("Authorization") + ":" + token + ":" + body.TargetID
		mu.Unlock()
		return response(`{"success":true,"data":{"allowed":true}}`), nil
	}), func(ctx context.Context) (string, error) { return ctx.Value(tokenKey{}).(string), nil })
	require.NoError(t, err)
	var wg sync.WaitGroup
	for _, actor := range []string{"alice", "carol"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.WithValue(context.Background(), tokenKey{}, actor+"-token")
			_, err := c.CanEdit(ctx, actor, "bob")
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, map[string]string{"alice": "Bearer alice-token:alice-token:bob", "carol": "Bearer carol-token:carol-token:bob"}, seen)
}
func TestConstructorRejectsUntrustedConfiguration(t *testing.T) {
	d := doFunc(func(*http.Request) (*http.Response, error) { return nil, nil })
	credentials := func(context.Context) (string, error) { return "token", nil }
	for _, base := range []string{"", "file:///tmp/x", "https:///missing", "https://user:pass@host", "https://host?secret=1", "https://host#fragment", "https://host#"} {
		_, err := New(base, d, credentials)
		require.Error(t, err)
	}
	var typedNil *http.Client
	_, err := New("https://host", typedNil, credentials)
	require.Error(t, err)
	_, err = New("https://host", nil, credentials)
	require.Error(t, err)
	_, err = New("https://host", d, nil)
	require.Error(t, err)
}
func TestObservationIncludesReadAndEnvelopeValidation(t *testing.T) {
	var event Event
	c, err := New("https://host", doFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: failingBody{readErr: io.EOF, delay: 10 * time.Millisecond}}, nil
	}), func(context.Context) (string, error) { return "token", nil }, func(e Event) { event = e })
	require.NoError(t, err)
	_, err = c.CanEdit(context.Background(), "alice", "bob")
	require.Error(t, err)
	require.Equal(t, "protocol", event.Outcome)
	require.GreaterOrEqual(t, event.Duration, 10*time.Millisecond)
}
