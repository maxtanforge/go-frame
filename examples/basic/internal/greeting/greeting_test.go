package greeting

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/router"
)

func TestFeatureThroughFramework(t *testing.T) {
	r := router.New(nil, router.Options{})
	Register(r, Service{Prefix: "Hello"})
	for _, tc := range []struct {
		body     string
		status   int
		contains string
	}{
		{`{"name":"Ada"}`, 200, `"message":"Hello, Ada"`},
		{`{"name":" "}`, 400, `"success":false`},
		{`{"name":"Ada","admin":true}`, 400, `"success":false`},
	} {
		req := httptest.NewRequest("POST", "/greetings", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("x-trace-id") == "" {
			t.Fatal("missing trace")
		}
	}
}
