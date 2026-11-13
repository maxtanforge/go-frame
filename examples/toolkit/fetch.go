package toolkit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	httpclient "github.com/maxtanforge/go-frame/http/client"
)

// NewFetcher builds the owned outbound client; call CloseIdleConnections on
// shutdown. The client adds no application-level retries; onRequest observes
// every completed exchange (see httpclient.RequestEvent).
func NewFetcher(onRequest func(httpclient.RequestEvent)) (*httpclient.Client, error) {
	return httpclient.New(httpclient.Options{
		Timeout:         10 * time.Second,
		MaxConnsPerHost: 20,
		OnRequest:       onRequest,
	})
}

// GetJSON calls a JSON endpoint. Non-2xx statuses surface as StatusError and
// are re-mapped to a public-safe message without the upstream body.
func GetJSON[T any](ctx context.Context, client *httpclient.Client, url string, out *T) error {
	err := httpclient.DoJSON(ctx, client, http.MethodGet, url, nil, out)
	var status *httpclient.StatusError
	if errors.As(err, &status) {
		return fmt.Errorf("upstream returned status %d", status.StatusCode)
	}
	return err
}
