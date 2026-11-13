// Package upstream implements the typed profile permission dependency.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	httpclient "github.com/maxtanforge/go-frame/http/client"
)

// Event contains only fixed labels and the complete call duration, including
// credentials, body decoding and envelope validation. Observe must be concurrency-safe.
type Event struct {
	Operation, Outcome string
	Duration           time.Duration
}

// Client is immutable after New and safe to share when its dependencies are.
// Configure timeouts, TLS, redirects and pooling on the injected Doer at startup.
type Client struct {
	endpoint    string
	doer        httpclient.Doer
	credentials func(context.Context) (string, error)
	observe     func(Event)
}

// Error retains a private cause for errors.Is/As without including it in public text.
type Error struct {
	Outcome string
	Err     error
}

func (e *Error) Error() string { return "profile permission call failed" }
func (e *Error) Unwrap() error { return e.Err }

// New accepts a trusted base URL and adds the fixed permission endpoint to its
// path prefix. Credentials are resolved anew on every call, never stored in headers.
func New(baseURL string, doer httpclient.Doer, credentials func(context.Context) (string, error), observe ...func(Event)) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u == nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(baseURL, "#") || u.Opaque != "" {
		return nil, errors.New("profile permission: invalid base URL")
	}
	if doer == nil || nilValue(doer) || credentials == nil || len(observe) > 1 {
		return nil, errors.New("profile permission: invalid dependencies")
	}
	endpoint := u.JoinPath("permissions/profile/edit").String()
	c := &Client{endpoint: endpoint, doer: doer, credentials: credentials}
	if len(observe) == 1 {
		c.observe = observe[0]
	}
	return c, nil
}
func nilValue(v any) bool {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}

type permissionRequest struct {
	ActorID  string `json:"actor_id"`
	TargetID string `json:"target_id"`
}
type permissionEnvelope struct {
	Success *bool           `json:"success"`
	Data    *permissionData `json:"data"`
}
type permissionData struct {
	Allowed *bool `json:"allowed"`
}

// CanEdit performs one POST with the caller's context and credentials. A valid
// false decision returns (false,nil); upstream rejection is an error, not denial.
// No automatic retries are performed.
func (c *Client) CanEdit(ctx context.Context, actorID, targetID string) (allowed bool, err error) {
	started := time.Now()
	outcome := "allowed"
	defer func() {
		if c.observe != nil {
			c.observe(Event{Operation: "profile.permission", Outcome: outcome, Duration: time.Since(started)})
		}
	}()
	fail := func(label string, cause error) (bool, error) {
		outcome = label
		return false, &Error{Outcome: label, Err: cause}
	}
	if err := ctx.Err(); err != nil {
		return fail(classify(err), err)
	}
	token, err := c.credentials(ctx)
	if err != nil {
		return fail("credentials", err)
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return fail("credentials", errors.New("invalid credential"))
	}
	body, err := json.Marshal(permissionRequest{ActorID: actorID, TargetID: targetID})
	if err != nil {
		return fail("encode", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fail("request", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	var envelope permissionEnvelope
	if err := httpclient.DoJSONRequest(c.doer, req, &envelope, httpclient.JSONOptions{MaxResponseBytes: 64 << 10}); err != nil {
		return fail(classify(err), err)
	}
	if envelope.Success == nil {
		return fail("protocol", errors.New("missing success flag"))
	}
	if !*envelope.Success {
		return fail("rejected", errors.New("upstream rejected permission request"))
	}
	if envelope.Data == nil || envelope.Data.Allowed == nil {
		return fail("protocol", errors.New("missing permission decision"))
	}
	if !*envelope.Data.Allowed {
		outcome = "denied"
	}
	return *envelope.Data.Allowed, nil
}
func classify(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, httpclient.ErrResponseTooLarge) {
		return "body_limit"
	}
	var status *httpclient.StatusError
	if errors.As(err, &status) {
		return "http"
	}
	var operation *httpclient.OperationError
	if errors.As(err, &operation) {
		switch operation.Stage {
		case httpclient.StageEncode:
			return "encode"
		case httpclient.StageRequest:
			return "request"
		case httpclient.StageTransport:
			return "transport"
		case httpclient.StageRead:
			return "read"
		case httpclient.StageDecode:
			return "protocol"
		case httpclient.StageClose:
			return "close"
		}
	}
	return "protocol"
}
