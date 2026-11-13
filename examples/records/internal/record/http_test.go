package record

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/database/audit"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestHTTPAuditTimesAreMilliseconds(t *testing.T) {
	const instant int64 = 1700000000123
	fields, err := audit.New(testActor, time.UnixMilli(instant))
	require.NoError(t, err)
	for _, tc := range []struct{ name, method, path, body string }{
		{"create", "POST", "/records", `{"title":"hello"}`},
		{"detail", "GET", "/records/9007199254740993", ""},
		{"list", "GET", "/records?size=1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := Record{ID: testIDs.value, Title: "hello", Fields: fields}
			store := &listStore{fakeStore: fakeStore{saved: item}, rows: []Record{item, item}}
			r := chi.NewRouter()
			Register(r, Service{IDs: testIDs, Repository: store, Transactions: store, Now: func() time.Time { return time.UnixMilli(instant) }}, testActor)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code)
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			data := envelope.Data
			if tc.name == "list" {
				var result struct {
					Current int64             `json:"current"`
					Size    int64             `json:"size"`
					HasMore bool              `json:"has_more"`
					Records []json.RawMessage `json:"records"`
				}
				require.NoError(t, json.Unmarshal(data, &result))
				require.Equal(t, int64(1), result.Current)
				require.Equal(t, int64(1), result.Size)
				require.True(t, result.HasMore)
				require.Len(t, result.Records, 1)
				data = result.Records[0]
			}
			var result struct {
				ID        string      `json:"id"`
				Title     string      `json:"title"`
				CreatedAt int64       `json:"created_at"`
				UpdatedAt int64       `json:"updated_at"`
				CreatedBy audit.Actor `json:"created_by"`
				UpdatedBy audit.Actor `json:"updated_by"`
			}
			require.NoError(t, json.Unmarshal(data, &result))
			require.Equal(t, "9007199254740993", result.ID)
			require.Equal(t, "hello", result.Title)
			require.Equal(t, instant, result.CreatedAt)
			require.Equal(t, instant, result.UpdatedAt)
			require.Equal(t, testActor, result.CreatedBy)
			require.Equal(t, testActor, result.UpdatedBy)
		})
	}
}

func TestHTTPBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		store                    *fakeStore
		status                   int
	}{
		{"create", "POST", "/records", `{"title":"hello"}`, &fakeStore{}, 200},
		{"invalid", "POST", "/records", `{"title":" "}`, &fakeStore{}, 400},
		{"nul title", "POST", "/records", `{"title":"hello\u0000world"}`, &fakeStore{}, 400},
		{"malformed", "POST", "/records", `{`, &fakeStore{}, 400},
		{"missing", "GET", "/records/9007199254740993", "", &fakeStore{}, 404},
		{"found", "GET", "/records/9007199254740993", "", &fakeStore{saved: Record{ID: 9007199254740993, Title: "hello"}}, 200},
		{"failure", "POST", "/records", `{"title":"hello"}`, &fakeStore{auditErr: errors.New("private database password")}, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := chi.NewRouter()
			Register(r, Service{IDs: testIDs, Repository: tc.store, Transactions: tc.store}, testActor)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Body.String(), "private database password")
		})
	}
}

func TestHTTPActorCannotBeSpoofed(t *testing.T) {
	for _, body := range []string{`{"title":"hello"}`, `{"title":"hello","actor":{"kind":"subject","id":"attacker"}}`} {
		f := &fakeStore{}
		r := chi.NewRouter()
		Register(r, Service{IDs: testIDs, Transactions: f}, testActor)
		req := httptest.NewRequest("POST", "/records", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "attacker")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if strings.Contains(body, "actor") {
			require.Equal(t, 400, w.Code)
			require.Empty(t, f.events)
		} else {
			require.Equal(t, 200, w.Code)
			require.Equal(t, testActor, f.saved.CreatedBy)
			require.Contains(t, w.Body.String(), `"created_by":{"kind":"system","id":"records-test"}`)
		}
	}
}

func TestHTTPDecimalIDBoundaries(t *testing.T) {
	for _, value := range []string{"9007199254740993", "0", "-1", "9223372036854775808", "%2B1", "1.2", "not-id"} {
		f := &fakeStore{saved: Record{ID: 9007199254740993, Title: "hello"}}
		r := chi.NewRouter()
		Register(r, Service{Repository: f}, testActor)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/records/"+value, nil))
		if value == "9007199254740993" {
			require.Equal(t, 200, w.Code)
			require.Equal(t, int64(9007199254740993), f.findID)
			require.Contains(t, w.Body.String(), `"id":"9007199254740993"`)
		} else {
			require.Equal(t, 400, w.Code)
			require.Zero(t, f.findID)
		}
	}
}
