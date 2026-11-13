package httpclient

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestDoJSONRequestPreservesMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.test/path?secret=value", strings.NewReader(`{}`))
	req.SetBasicAuth("user", "secret")
	req.Header["x-trace-id"] = []string{""}
	req.Header.Set("Accept", "application/custom+json")
	req.Host = "custom.test"
	before := req.Clone(ctx)
	var out map[string]int
	err := DoJSONRequest(doerFunc(func(sent *http.Request) (*http.Response, error) {
		if sent.Context() != ctx || !reflect.DeepEqual(sent.Header, before.Header) || sent.Host != before.Host || sent.Body != req.Body {
			t.Fatal("request metadata changed")
		}
		sent.Header.Set("Authorization", "changed")
		sent.URL.Path = "/changed"
		return &http.Response{StatusCode: 200, Body: &body{Reader: strings.NewReader(`{"n":2}`)}}, nil
	}), req, &out)
	if err != nil || out["n"] != 2 {
		t.Fatal(out, err)
	}
	if !reflect.DeepEqual(req.Header, before.Header) || req.URL.Path != before.URL.Path {
		t.Fatal("caller metadata mutated")
	}
}

func TestDoJSONRequestFailuresAndClosure(t *testing.T) {
	secret := errors.New("private secret")
	for _, tc := range []struct {
		name, content   string
		status          int
		doErr, closeErr error
		limit           int64
		want            error
	}{
		{"limit", "123456", 200, nil, nil, 3, nil},
		{"decode", "private secret", 200, nil, nil, 0, nil},
		{"status", "private secret", 403, nil, nil, 0, nil},
		{"transport", "", 200, secret, nil, 0, secret},
		{"close", "{}", 200, nil, secret, 0, secret},
		{"cancel", "", 200, context.Canceled, nil, 0, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &body{Reader: strings.NewReader(tc.content), closeErr: tc.closeErr}
			req, _ := http.NewRequest("GET", "http://example.test/?secret", nil)
			var out any
			err := DoJSONRequest(doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: b}, tc.doErr
			}), req, &out, JSONOptions{MaxResponseBytes: tc.limit})
			if err == nil || !b.closed || strings.Contains(err.Error(), "secret") {
				t.Fatal(err, b.closed)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if tc.status == 403 {
				var status *StatusError
				if !errors.As(err, &status) || status.StatusCode != 403 {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDoJSONRequestValidationAndCancellation(t *testing.T) {
	never := doerFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected Do"); return nil, nil })
	req, _ := http.NewRequest("GET", "http://example.test", nil)
	if DoJSONRequest(never, nil, nil) == nil || DoJSONRequest(nil, req, nil) == nil {
		t.Fatal("nil accepted")
	}
	for _, opts := range [][]JSONOptions{{{MaxResponseBytes: -1}}, {{}, {}}} {
		if DoJSONRequest(never, req, nil, opts...) == nil {
			t.Fatal("invalid options accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := DoJSONRequest(http.DefaultClient, req.WithContext(ctx), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
