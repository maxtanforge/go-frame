package operationhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/stretchr/testify/require"
)

type watchedBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *watchedBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (b *watchedBody) Close() error { b.closed = true; return nil }

func TestInterfaceRequestCapturePreservesAndRedactsBody(t *testing.T) {
	body := `{"name":"Ada","password":"body-secret","items":[{"refresh_token":"nested-secret","count":9007199254740993}]}`
	var event operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	require.NoError(t, err)
	source := &watchedBody{Reader: strings.NewReader(body)}
	req := httptest.NewRequest("POST", "/users?tag=one&tag=two&api_key=query-secret", nil)
	req.Body = source
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer header-secret")
	h := rec.Middleware(operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{
		Query: operationhttp.Enabled, Body: operationhttp.Enabled,
	}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Zero(t, source.reads, "logging must not read ahead")
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(data), "handler must receive the original body")
		require.NoError(t, r.Body.Close())
		w.WriteHeader(201)
	})))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, 201, w.Code)
	require.True(t, source.closed)
	require.Contains(t, string(event.Request), "9007199254740993")
	require.JSONEq(t, `{"query":{"tag":["one","two"],"api_key":["***"]},"body":{"name":"Ada","password":"***","items":[{"refresh_token":"***","count":9007199254740993}]}}`, string(event.Request))
	for _, secret := range []string{"body-secret", "nested-secret", "query-secret", "header-secret"} {
		require.NotContains(t, string(event.Request), secret)
	}
}

func TestInterfaceSelectsFieldsAndCustomRedaction(t *testing.T) {
	var event operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	require.NoError(t, err)
	metadata := operationhttp.Metadata{Request: operationhttp.RequestOptions{
		Query: operationhttp.Enabled, Body: operationhttp.Enabled,
		QueryFields: []string{"page", "email"}, BodyFields: []string{"profile", "count", "password"},
		SensitiveFields: []string{"email", "phone_number"},
	}}
	configured := operationhttp.With(metadata)
	// Construction must detach every list from application-owned slices.
	metadata.Request.QueryFields[0] = "unselected"
	metadata.Request.BodyFields[0] = "unselected"
	metadata.Request.SensitiveFields[0] = "unselected"
	body := `{"profile":{"Email":"private","items":[{"phone-number":"private"}]},"password":"private","count":9007199254740993,"unselected":"private"}`
	req := httptest.NewRequest("POST", "/?page=1&email=private&unselected=private", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h := rec.Middleware(configured(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(data))
	})))
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.JSONEq(t, `{"query":{"page":["1"],"email":["***"]},"body":{"profile":{"Email":"***","items":[{"phone-number":"***"}]},"password":"***","count":9007199254740993}}`, string(event.Request))
	require.Contains(t, string(event.Request), "9007199254740993")
	require.NotContains(t, string(event.Request), "private")
}

func TestEmptyFieldSelectionOverridesGroup(t *testing.T) {
	var event operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	require.NoError(t, err)
	group := operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{
		Query: operationhttp.Enabled, Body: operationhttp.Enabled,
		QueryFields: []string{"page"}, BodyFields: []string{"name"},
	}})
	endpoint := operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{
		QueryFields: []string{}, BodyFields: []string{},
	}})
	req := httptest.NewRequest("POST", "/?page=1", strings.NewReader(`{"name":"Ada"}`))
	req.Header.Set("Content-Type", "application/json")
	rec.Middleware(group(endpoint(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
	})))).ServeHTTP(httptest.NewRecorder(), req)
	require.JSONEq(t, `{"query":{},"body":{}}`, string(event.Request))
}

func TestInterfaceRequestCaptureDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outer, inner operationhttp.RequestOptions
		wantRequest  string
	}{
		{"default", operationhttp.RequestOptions{}, operationhttp.RequestOptions{}, `{"query":{"page":["1"]},"body_omitted":"not_read"}`},
		{"query only", operationhttp.RequestOptions{}, operationhttp.RequestOptions{Body: operationhttp.Disabled}, `{"query":{"page":["1"]}}`},
		{"inherit", operationhttp.RequestOptions{Body: operationhttp.Disabled}, operationhttp.RequestOptions{}, `{"query":{"page":["1"]}}`},
		{"disable", operationhttp.RequestOptions{}, operationhttp.RequestOptions{Query: operationhttp.Disabled, Body: operationhttp.Disabled}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event operation.Event
			rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
			require.NoError(t, err)
			source := &watchedBody{Reader: strings.NewReader(`{"password":"hidden"}`)}
			req := httptest.NewRequest("GET", "/search?page=1", nil)
			req.Body = source
			req.Header.Set("Content-Type", "application/json")
			h := rec.Middleware(operationhttp.With(operationhttp.Metadata{Request: tc.outer})(operationhttp.With(operationhttp.Metadata{Request: tc.inner})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))))
			h.ServeHTTP(httptest.NewRecorder(), req)
			require.Zero(t, source.reads)
			if tc.wantRequest != "" {
				require.JSONEq(t, tc.wantRequest, string(event.Request))
			} else {
				require.Empty(t, event.Request)
			}
		})
	}
}

func TestRequestCaptureOmissionDoesNotChangeResponse(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, reason string
		read                            bool
	}{
		{"unread", "application/json", `{"secret":"hidden"}`, "not_read", false},
		{"invalid", "application/json", `{"secret":"hidden"`, "invalid_json", true},
		{"invalid UTF-8", "application/json", "{\"name\":\"\xff\"}", "invalid_json", true},
		{"form", "application/x-www-form-urlencoded", "password=hidden", "unsupported_content_type", true},
		{"large", "application/json", `{"value":"` + strings.Repeat("x", 17<<10) + `"}`, "too_large", true},
		{"deep", "application/json", strings.Repeat("[", 12) + `"hidden"` + strings.Repeat("]", 12), "too_deep", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event operation.Event
			rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
			require.NoError(t, err)
			req := httptest.NewRequest("POST", "/upload", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			h := rec.Middleware(operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{Body: operationhttp.Enabled}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.read {
					data, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					require.Equal(t, tc.body, string(data))
				}
				w.WriteHeader(202)
			})))
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			require.Equal(t, 202, response.Code)
			var captured map[string]any
			require.NoError(t, json.Unmarshal(event.Request, &captured))
			require.Equal(t, tc.reason, captured["body_omitted"])
			require.NotContains(t, string(event.Request), "hidden")
		})
	}
}

func TestChunkedBodyIsNotDrainedForLogging(t *testing.T) {
	var event operation.Event
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{})
	require.NoError(t, err)
	source := &watchedBody{Reader: bytes.NewBufferString(`123456`)}
	req := httptest.NewRequest("POST", "/", nil)
	req.Body, req.ContentLength = source, -1
	req.Header.Set("Content-Type", "application/json")
	h := rec.Middleware(operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{Body: operationhttp.Enabled}})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		data := make([]byte, 3)
		_, err := io.ReadFull(r.Body, data)
		require.NoError(t, err)
		require.Equal(t, "123", string(data))
	})))
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, 1, source.reads)
	require.JSONEq(t, `{"query":{},"body_omitted":"incomplete"}`, string(event.Request))
}

func TestRecorderCaptureDefaultsAndOverrides(t *testing.T) {
	var event operation.Event
	options := operationhttp.Options{Request: operationhttp.RequestOptions{Query: operationhttp.Disabled, Body: operationhttp.Disabled, SensitiveFields: []string{"contact"}}}
	rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), options)
	require.NoError(t, err)
	options.Request.SensitiveFields[0] = "other"
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
	})
	req := func() *http.Request {
		r := httptest.NewRequest("POST", "/?contact=private", strings.NewReader(`{"name":"Ada","contact":"private"}`))
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	rec.Middleware(h).ServeHTTP(httptest.NewRecorder(), req())
	require.Empty(t, event.Request)
	rec.Middleware(operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{Query: operationhttp.Enabled, Body: operationhttp.Enabled}})(h)).ServeHTTP(httptest.NewRecorder(), req())
	require.JSONEq(t, `{"query":{"contact":["***"]},"body":{"name":"Ada","contact":"***"}}`, string(event.Request))
	_, err = operationhttp.New(operation.SinkFunc(func(context.Context, operation.Event) error { return nil }), operationhttp.Options{Request: operationhttp.RequestOptions{Query: operationhttp.Mode(255)}})
	require.Error(t, err)
}

func TestSensitiveFieldsAccumulateAcrossCapturePolicies(t *testing.T) {
	for _, inner := range [][]string{{"email"}, {}} {
		t.Run(strings.Join(inner, ","), func(t *testing.T) {
			var event operation.Event
			rec, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { event = e; return nil }), operationhttp.Options{
				Request: operationhttp.RequestOptions{SensitiveFields: []string{"phone"}},
			})
			require.NoError(t, err)
			h := rec.Middleware(operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{SensitiveFields: []string{"contact"}}})(
				operationhttp.With(operationhttp.Metadata{Request: operationhttp.RequestOptions{SensitiveFields: inner}})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					_, err := io.Copy(io.Discard, r.Body)
					require.NoError(t, err)
				}))))
			r := httptest.NewRequest("POST", "/?phone=private&contact=private", strings.NewReader(`{"phone":"private","contact":"private","email":"private","password":"private"}`))
			r.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(httptest.NewRecorder(), r)
			var captured struct {
				Query map[string][]string `json:"query"`
				Body  map[string]string   `json:"body"`
			}
			require.NoError(t, json.Unmarshal(event.Request, &captured))
			for _, key := range []string{"phone", "contact"} {
				require.Equal(t, []string{"***"}, captured.Query[key])
				require.Equal(t, "***", captured.Body[key])
			}
			require.Equal(t, "***", captured.Body["password"])
			if len(inner) > 0 {
				require.Equal(t, "***", captured.Body["email"])
			}
		})
	}
}
