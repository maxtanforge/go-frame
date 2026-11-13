package respond

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/telemetry/trace"
	"github.com/stretchr/testify/require"
)

func TestMillisTime(t *testing.T) {
	original := time.UnixMilli(-12345)
	v := MillisTime(original)
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	require.Equal(t, "-12345", string(raw))
	var got MillisTime
	require.NoError(t, json.Unmarshal(raw, &got))
	require.True(t, original.Equal(time.Time(got)))
	for _, bad := range []string{`null`, `"123"`, `1.5`, `9223372036854775808`} {
		require.Error(t, json.Unmarshal([]byte(bad), &got))
	}
}

type validatedInput struct {
	Name string `json:"name"`
}

func (v *validatedInput) Validate() error {
	if v.Name == "" {
		return errors.New("private validation detail")
	}
	return nil
}
func TestDecodeJSON(t *testing.T) {
	for _, tc := range []struct {
		body, media string
		status      int
	}{{`{"name":"ok"}`, "application/json", 0}, {`{"name":"ok"}`, "application/problem+json", 0}, {`{"other":1}`, "application/json", 400}, {`{}`, "application/json", 400}, {`null`, "application/json", 400}, {`{} {}`, "application/json", 400}, {``, "application/json", 400}, {`{`, "application/json", 400}, {`{}`, "text/plain", 415}, {`{"name":"` + strings.Repeat("a", 100) + `"}`, "application/json", 413}} {
		t.Run(tc.body+tc.media, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.media)
			_, err := request.DecodeJSON[validatedInput](httptest.NewRecorder(), r, request.DecodeOptions{MaxBytes: 64})
			if tc.status == 0 {
				require.NoError(t, err)
			} else {
				var app *httperror.Error
				require.ErrorAs(t, err, &app)
				require.Equal(t, tc.status, app.Status)
				require.NotContains(t, app.Message, "private")
			}
		})
	}
}
func TestWriteErrorAndRecovery(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(trace.IntoContext(req.Context(), "trace-test"))
	rec := httptest.NewRecorder()
	WriteError(rec, req, &httperror.Error{Status: 404})
	require.Equal(t, 404, rec.Code)
	require.Equal(t, "trace-test", decode(t, rec)["traceId"])
	rec = httptest.NewRecorder()
	Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret") })).ServeHTTP(rec, req)
	require.Equal(t, 500, rec.Code)
	require.NotContains(t, rec.Body.String(), "secret")
	rec = httptest.NewRecorder()
	Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return make(chan int), nil })(rec, req)
	require.Equal(t, "trace-test", decode(t, rec)["traceId"])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec = httptest.NewRecorder()
	WriteError(rec, req.WithContext(ctx), context.Canceled)
	require.Empty(t, rec.Body.String())
}

type plainWriter struct {
	header http.Header
	status int
}

func (p *plainWriter) Header() http.Header         { return p.header }
func (p *plainWriter) WriteHeader(s int)           { p.status = s }
func (p *plainWriter) Write(b []byte) (int, error) { return len(b), nil }
func TestTrackerFinalAndFlush(t *testing.T) {
	rw := &wroteTracker{ResponseWriter: &plainWriter{header: make(http.Header)}}
	rw.WriteHeader(103)
	require.False(t, rw.wrote)
	require.ErrorIs(t, http.NewResponseController(rw).Flush(), http.ErrNotSupported)
	require.False(t, rw.wrote)
	rec := httptest.NewRecorder()
	rw = &wroteTracker{ResponseWriter: rec}
	require.NoError(t, http.NewResponseController(rw).Flush())
	require.True(t, rw.wrote)
	require.True(t, rec.Flushed)
}
