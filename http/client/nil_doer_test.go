package httpclient

import (
	"context"
	"net/http"
	"testing"
)

func TestJSONRejectsTypedNilDoers(t *testing.T) {
	var standard *http.Client
	var owned *Client
	var function doerFunc
	for name, client := range map[string]Doer{"standard": standard, "owned": owned, "function": function} {
		for _, prepared := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/convenience", true: "/prepared"}[prepared], func(t *testing.T) {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("nil Doer panicked: %v", p)
					}
				}()
				var err error
				if prepared {
					req, _ := http.NewRequest("GET", "http://example.invalid", nil)
					err = DoJSONRequest(client, req, nil)
				} else {
					err = DoJSON(context.Background(), client, "GET", "http://example.invalid", nil, nil)
				}
				if err == nil {
					t.Fatal("nil Doer accepted")
				}
			})
		}
	}
}
