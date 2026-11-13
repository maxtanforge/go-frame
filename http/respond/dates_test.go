package respond

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/stretchr/testify/require"
)

type embeddedA struct {
	Clash string
	When  time.Time
}
type embeddedB struct{ Clash string }

func TestAdapterStandardJSON(t *testing.T) {
	v := struct {
		embeddedA
		embeddedB
		Empty  string `json:"empty,omitempty"`
		Number int    `json:"number,string"`
	}{embeddedA: embeddedA{When: time.Unix(123, 0).UTC()}, Number: 42}
	want, err := json.Marshal(v)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return v, nil })(rec, httptest.NewRequest("GET", "/", nil))
	var env struct{ Data json.RawMessage }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.JSONEq(t, string(want), string(env.Data))
}
func TestDeadlineOverridesNilResult(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	rec := httptest.NewRecorder()
	Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return "late", nil })(rec, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	require.Equal(t, 504, rec.Code)
}
func TestWrappedChildDeadline(t *testing.T) {
	rec := httptest.NewRecorder()
	Adapter(func(http.ResponseWriter, *http.Request) (any, error) {
		return nil, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)
	})(rec, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 500, rec.Code)
}
func TestInvalidAppStatus(t *testing.T) {
	for _, status := range []int{1, 200, 399, 600, 999} {
		rec := httptest.NewRecorder()
		Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return nil, &httperror.Error{Status: status} })(rec, httptest.NewRequest("GET", "/", nil))
		require.Equal(t, 500, rec.Code)
	}
}

func BenchmarkAdapterJSON(b *testing.B) {
	data := struct {
		When   time.Time
		Values []int
	}{time.Unix(123, 0), []int{1, 2, 3}}
	h := Adapter(func(http.ResponseWriter, *http.Request) (any, error) { return data, nil })
	req := httptest.NewRequest("GET", "/", nil)
	b.ReportAllocs()
	for b.Loop() {
		h(httptest.NewRecorder(), req)
	}
}
