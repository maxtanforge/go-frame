package httpclient

import (
	"net/http"
	"testing"
)

type decoratedTransport struct{ http.RoundTripper }

func TestTransportDecorator(t *testing.T) {
	var base *http.Transport
	c, err := New(Options{MaxConnsPerHost: 7, WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
		base = rt.(*http.Transport)
		return &decoratedTransport{rt}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if c.transport != base || base.MaxConnsPerHost != 7 || c.client.Transport == base {
		t.Fatal("private transport not retained or decorated")
	}
	if _, err := New(Options{WrapTransport: func(http.RoundTripper) http.RoundTripper { return nil }}); err == nil {
		t.Fatal("nil decorator accepted")
	}
	if _, err := New(Options{WrapTransport: func(http.RoundTripper) http.RoundTripper { var p *decoratedTransport; return p }}); err == nil {
		t.Fatal("typed nil decorator accepted")
	}
}
