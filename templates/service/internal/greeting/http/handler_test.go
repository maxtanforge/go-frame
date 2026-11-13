package greetinghttp

import (
	"example.com/service/internal/greeting"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGreetingModelHasNoTransportTags(t *testing.T) {
	model := reflect.TypeFor[greeting.Greeting]()
	for field := range model.Fields() {
		if field.Tag.Get("json") != "" {
			t.Fatalf("business model %s contains HTTP JSON metadata", field.Name)
		}
	}
}

func TestHandler(t *testing.T) {
	h, err := New(greeting.Service{Prefix: "Welcome"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body     string
		status   int
		contains string
	}{
		{`{"name":" Ada "}`, 200, `"message":"Welcome, Ada"`},
		{`{"name":" "}`, 400, `"success":false`},
		{`{"name":"Ada","admin":true}`, 400, `"success":false`},
		{`{`, 400, `"success":false`},
	} {
		req := httptest.NewRequest("POST", "/greetings", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != tc.status || !strings.Contains(out.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %s", tc.body, out.Code, out.Body.String())
		}
	}
}
