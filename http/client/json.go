package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/maxtanforge/go-frame/internal/isnil"
)

// drainLimit bounds how much of a non-2xx error body is read before Close, so
// ordinary JSON error envelopes keep the connection reusable without ever
// reading an unbounded response.
const drainLimit = 8 << 10

// Doer is the consumer-owned seam for outbound HTTP calls.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// JSONOptions bounds successful response reads. Zero selects 1 MiB. Negative
// limits are invalid; positive values override the default.
type JSONOptions struct{ MaxResponseBytes int64 }

// StatusError reports a non-2xx status without retaining its response body.
type StatusError struct{ StatusCode int }

func (e *StatusError) Error() string { return fmt.Sprintf("httpclient: HTTP status %d", e.StatusCode) }

// DoJSON sends JSON and owns the response body on all paths. At most one options
// value is accepted. Nil input means no request body. Nil output discards a
// bounded success body; otherwise exactly one JSON value is required (an empty
// 204 body is a decode error). Public error text excludes payloads and URLs.
func DoJSON(ctx context.Context, client Doer, method, url string, input, output any, options ...JSONOptions) (err error) {
	var requestBody io.Reader
	if input != nil {
		data, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return safeError(StageEncode, marshalErr)
		}
		requestBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, requestBody)
	if err != nil {
		return safeError(StageRequest, err)
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return DoJSONRequest(client, req, output, options...)
}

// DoJSONRequest executes a prepared request and owns every received response body.
// It preserves explicit headers and context and clones metadata before calling Do.
// The request body retains net/http consumption and close semantics. JSON limits,
// decoding, and public error privacy are identical to DoJSON.
func DoJSONRequest(client Doer, req *http.Request, output any, options ...JSONOptions) (err error) {
	if len(options) > 1 {
		return errors.New("httpclient: multiple JSON options")
	}
	limit := int64(1 << 20)
	if len(options) == 1 {
		if options[0].MaxResponseBytes < 0 {
			return errors.New("httpclient: negative response limit")
		}
		if options[0].MaxResponseBytes > 0 {
			limit = options[0].MaxResponseBytes
		}
	}
	if isnil.Check(client) {
		return errors.New("httpclient: nil Doer")
	}
	if req == nil {
		return errors.New("httpclient: nil request")
	}
	req = req.Clone(req.Context())
	resp, doErr := client.Do(req)
	if resp != nil && resp.Body != nil {
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				err = errors.Join(err, safeError(StageClose, closeErr))
			}
		}()
	}
	if doErr != nil {
		return safeError(StageTransport, doErr)
	}
	if resp == nil {
		return errors.New("httpclient: missing response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Bounded drain lets small error bodies keep the keep-alive connection
		// reusable; oversized bodies still discard the connection. A nil body
		// (legal for custom Doers) skips draining. Drain read failures need no
		// handling: Close runs either way and joins real errors.
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimit))
		}
		return &StatusError{StatusCode: resp.StatusCode}
	}
	if resp.Body == nil {
		return errors.New("httpclient: missing response body")
	}
	// Read the configured maximum, then probe one extra byte. This avoids limit+1
	// integer overflow and stops without draining an unbounded response.
	var data []byte
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, limit))
	} else {
		data, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	}
	if err != nil {
		return safeError(StageRead, err)
	}
	var extra [1]byte
	n, readErr := io.ReadFull(resp.Body, extra[:])
	if n > 0 {
		return ErrResponseTooLarge
	}
	if readErr != nil && readErr != io.EOF {
		return safeError(StageRead, readErr)
	}
	if output == nil {
		return nil
	}
	if err = json.Unmarshal(data, output); err != nil {
		return safeError(StageDecode, err)
	}
	return nil
}
