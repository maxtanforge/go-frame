package respond

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/stretchr/testify/require"
)

func TestSwitchingProtocolsCommitsResponse(t *testing.T) {
	for _, panicAfter := range []bool{false, true} {
		rec := httptest.NewRecorder()
		serve := func() {
			Adapter(func(w http.ResponseWriter, _ *http.Request) (any, error) {
				w.WriteHeader(http.StatusSwitchingProtocols)
				if panicAfter {
					panic("after switching protocols")
				}
				return "must not be written", nil
			})(rec, httptest.NewRequest("GET", "/", nil))
		}
		if panicAfter {
			require.PanicsWithValue(t, http.ErrAbortHandler, serve)
		} else {
			serve()
		}
		require.Equal(t, http.StatusSwitchingProtocols, rec.Code)
		require.Empty(t, rec.Body.String())
	}
}

type deadlineMarshaler struct{ ctx context.Context }

func (m deadlineMarshaler) MarshalJSON() ([]byte, error) {
	<-m.ctx.Done()
	return []byte(`"late"`), nil
}
func TestDeadlineDuringMarshal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return deadlineMarshaler{ctx}, nil })(rec, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	require.EqualValues(t, 504, decode(t, rec)["code"])
	require.NotContains(t, rec.Body.String(), "late")
}

func TestDecodeJSONExactBodyLimit(t *testing.T) {
	const payload = `{"n":1}`
	for _, padding := range []string{"", " "} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(payload+padding))
		r.Header.Set("Content-Type", "application/json")
		value, err := request.DecodeJSON[map[string]int](httptest.NewRecorder(), r, request.DecodeOptions{MaxBytes: int64(len(payload))})
		if padding == "" {
			require.NoError(t, err)
			require.Equal(t, 1, value["n"])
		} else {
			var app *httperror.Error
			require.ErrorAs(t, err, &app)
			require.Equal(t, http.StatusRequestEntityTooLarge, app.Status)
		}
	}
}

func TestWriteErrorTypedNilAppError(t *testing.T) {
	var app *httperror.Error
	for _, err := range []error{app, fmt.Errorf("wrapped: %w", app)} {
		rec := httptest.NewRecorder()
		require.NotPanics(t, func() { WriteError(rec, httptest.NewRequest("GET", "/", nil), err) })
		require.Equal(t, 500, rec.Code)
	}
	require.Equal(t, "", app.Error())
	require.Nil(t, app.Unwrap())
}

func TestWriteErrorRequestContextPrecedesReturnedError(t *testing.T) {
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		err    error
		status int
	}{
		{"expired cancellation", expired, context.Canceled, 504},
		{"expired wrapped cancellation", expired, fmt.Errorf("wrapped: %w", context.Canceled), 504},
		{"canceled deadline", canceled, context.DeadlineExceeded, 0},
		{"canceled wrapped deadline", canceled, fmt.Errorf("wrapped: %w", context.DeadlineExceeded), 0},
		{"live cancellation", context.Background(), context.Canceled, 500},
		{"live wrapped cancellation", context.Background(), fmt.Errorf("operation: %w", context.Canceled), 500},
		{"live application cancellation", context.Background(), &httperror.Error{Status: 409, Message: "operation stopped", Err: context.Canceled}, 409},
		{"live deadline", context.Background(), context.DeadlineExceeded, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, httptest.NewRequest("GET", "/", nil).WithContext(tc.ctx), tc.err)
			if tc.status == 0 {
				require.Empty(t, rec.Body.String())
			} else {
				require.Equal(t, tc.status, rec.Code)
			}
		})
	}
}
