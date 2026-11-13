package auth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/telemetry/logger"
)

func verifier(_ context.Context, s string) (principal.Principal, error) {
	if s != "good" {
		return principal.Principal{}, principal.ErrUnauthenticated
	}
	return principal.Principal{ID: "42", Issuer: "test"}, nil
}
func TestDefaultProtectionAndPublicPolicy(t *testing.T) {
	a, err := auth.New(auth.VerifyFunc(verifier), auth.Options{PublicEndpoints: []auth.Endpoint{{Method: "POST", Path: "/login"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, token string
		status              int
		actor               bool
	}{{"POST", "/login", "bad", 200, false}, {"GET", "/login", "", 401, false}, {"POST", "/login/", "", 401, false}, {"POST", "/%6cogin", "", 401, false}, {"POST", "/login?x=y", "", 200, false}, {"GET", "/new", "", 401, false}, {"GET", "/new", "good", 200, true}, {"GET", "/new", "bad", 401, false}} {
		t.Run(tc.method+tc.path+tc.token, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, ok := principal.FromContext(r.Context())
				if ok != tc.actor {
					t.Error("wrong identity")
				}
				w.WriteHeader(200)
			})).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}
func TestOptionalAndDuplicateHeaders(t *testing.T) {
	a, _ := auth.New(auth.VerifyFunc(verifier), auth.Options{})
	h := a.Optional(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, headers := range [][]string{nil, {""}, {"Bearer bad"}, {"Bearer good", "Bearer good"}, {"Bearer good"}} {
		r := httptest.NewRequest("GET", "/", nil)
		for _, v := range headers {
			r.Header.Add("Authorization", v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if headers == nil || len(headers) == 1 && headers[0] == "Bearer good" {
			want = 204
		}
		if w.Code != want {
			t.Fatalf("headers %d: %d", len(headers), w.Code)
		}
	}
}
func TestErrorsAndCheck(t *testing.T) {
	for _, cause := range []error{principal.ErrUnauthenticated, errors.New("private backend"), context.DeadlineExceeded} {
		a, _ := auth.New(auth.VerifyFunc(verifier), auth.Options{Check: func(context.Context, principal.Principal) error { return cause }})
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer good")
		w := httptest.NewRecorder()
		a.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler reached") })).ServeHTTP(w, r)
		want := 500
		if errors.Is(cause, principal.ErrUnauthenticated) {
			want = 401
		}
		if w.Code != want || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}
func TestPolicyValidation(t *testing.T) {
	for _, e := range []auth.Endpoint{{"GET", "/a/*"}, {"get", "/a"}, {"GET", "/a//b"}, {"GET", "/a/../b"}, {"GET", "/a%20b"}, {"GET", "/a?x"}, {"GET", "/a/{id}"}} {
		if _, err := auth.New(auth.VerifyFunc(verifier), auth.Options{PublicEndpoints: []auth.Endpoint{e}}); err == nil {
			t.Fatal("invalid endpoint", e)
		}
	}
	if _, err := auth.New(nil, auth.Options{}); err == nil {
		t.Fatal("nil verifier")
	}
}
func TestIdentityReachesOuterAccessAndBusinessLog(t *testing.T) {
	var logs strings.Builder
	lg := slog.New(slog.NewJSONHandler(&logs, nil))
	a, _ := auth.New(auth.VerifyFunc(verifier), auth.Options{})
	h := middleware.AccessLog(lg, middleware.AccessOptions{})(a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.FromContext(r.Context()).Info("business")
		_, _ = io.WriteString(w, "ok")
	})))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer good")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if strings.Count(logs.String(), `"actor_id":"42"`) != 2 {
		t.Fatal(logs.String())
	}
}

func TestConcurrentRequestIdentitiesAreIsolated(t *testing.T) {
	a, _ := auth.New(auth.VerifyFunc(func(_ context.Context, raw string) (principal.Principal, error) {
		return principal.Principal{ID: raw, Issuer: "test"}, nil
	}), auth.Options{})
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := principal.Require(r.Context())
		if err != nil || p.ID != strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("identity crossed requests")
		}
		w.WriteHeader(204)
	}))
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Authorization", fmt.Sprintf("Bearer user-%d", i))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 204 {
				t.Error(w.Code)
			}
		}(i)
	}
	group.Wait()
}
