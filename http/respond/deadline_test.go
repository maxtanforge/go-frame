package respond

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/stretchr/testify/require"
)

// Models a deadline timer that is due but has not published cancellation yet.
type unpublishedDeadline struct {
	context.Context
	at time.Time
}

func (c unpublishedDeadline) Deadline() (time.Time, bool) { return c.at, true }

func TestElapsedDeadlineBeforeCancellationPublished(t *testing.T) {
	for _, handler := range []Handler{
		func(http.ResponseWriter, *http.Request) (any, error) { return "late-success", nil },
		func(http.ResponseWriter, *http.Request) (any, error) {
			return nil, &httperror.Error{Status: http.StatusConflict, Message: "late-error"}
		},
	} {
		ctx := unpublishedDeadline{Context: context.Background(), at: time.Now().Add(-time.Second)}
		rec := httptest.NewRecorder()
		Adapter(handler)(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		require.Equal(t, http.StatusGatewayTimeout, rec.Code)
		require.NotContains(t, rec.Body.String(), "late-")
	}
}

func TestExplicitCancellationPrecedesUnpublishedDeadline(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := unpublishedDeadline{Context: base, at: time.Now().Add(-time.Second)}
	rec := httptest.NewRecorder()
	Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return "late", nil })(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	require.Empty(t, rec.Body.String())
}

type slowDeadlineJSON struct{}

func (slowDeadlineJSON) MarshalJSON() ([]byte, error) {
	time.Sleep(2 * time.Second)
	return []byte(`"late-marshal"`), nil
}

func TestDeadlineElapsesDuringMarshalBeforeCancellationPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := unpublishedDeadline{Context: context.Background(), at: time.Now().Add(time.Second)}
		rec := httptest.NewRecorder()
		Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return slowDeadlineJSON{}, nil })(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		require.Equal(t, http.StatusGatewayTimeout, rec.Code)
		require.NotContains(t, rec.Body.String(), "late-marshal")
	})
}
