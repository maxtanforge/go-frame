package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireOperationStage(t *testing.T, err error, want Stage) {
	t.Helper()
	var op *OperationError
	if !errors.As(err, &op) || op == nil || op.Stage != want {
		t.Fatalf("error %T does not expose OperationError stage %q: %v", err, want, err)
	}
}

type failedJSONMarshaler struct{ err error }

func (m failedJSONMarshaler) MarshalJSON() ([]byte, error) { return nil, m.err }

func TestOperationStagesAndOriginalCauses(t *testing.T) {
	secret := errors.New("private token and payload")
	never := doerFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected exchange")
		return nil, nil
	})
	t.Run("encode", func(t *testing.T) {
		err := DoJSON(context.Background(), never, "POST", "https://example.test", failedJSONMarshaler{secret}, nil)
		requireOperationStage(t, err, StageEncode)
		var marshaler *json.MarshalerError
		if !errors.Is(err, secret) || !errors.As(err, &marshaler) || err.Error() != "httpclient: encode JSON failed" {
			t.Fatalf("encoding cause or safe text lost: %v", err)
		}
	})
	t.Run("request construction", func(t *testing.T) {
		err := DoJSON(context.Background(), never, "GET", ":private-token", nil, nil)
		requireOperationStage(t, err, StageRequest)
		var original *url.Error
		if !errors.As(err, &original) || err.Error() != "httpclient: create request failed" {
			t.Fatalf("request cause or safe text lost: %v", err)
		}
	})
	t.Run("custom transport", func(t *testing.T) {
		err := DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, secret
		}), "GET", "https://user:private@example.test/?token=private", nil, nil)
		requireOperationStage(t, err, StageTransport)
		if !errors.Is(err, secret) || err.Error() != "httpclient: request failed" {
			t.Fatalf("transport cause or safe text lost: %v", err)
		}
	})
	for _, tc := range []struct {
		name, text string
		stage      Stage
		reader     io.Reader
		closeErr   error
	}{
		{"read", "read response failed", StageRead, failingReader{secret}, nil},
		{"decode", "decode JSON failed", StageDecode, strings.NewReader("private payload"), nil},
		{"close", "close response failed", StageClose, strings.NewReader("{}"), secret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &body{Reader: tc.reader, closeErr: tc.closeErr}
			var out any
			err := DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: b}, nil
			}), "GET", "https://example.test", nil, &out)
			requireOperationStage(t, err, tc.stage)
			if !b.closed || err.Error() != "httpclient: "+tc.text {
				t.Fatalf("cleanup or safe text lost: %v", err)
			}
			if tc.stage == StageDecode {
				var syntax *json.SyntaxError
				if !errors.As(err, &syntax) {
					t.Fatalf("decode cause lost: %v", err)
				}
			} else if !errors.Is(err, secret) {
				t.Fatalf("cause lost: %v", err)
			}
		})
	}
}

func TestOperationReadAndCloseFailuresRemainDistinct(t *testing.T) {
	readErr, closeErr := errors.New("private read"), errors.New("private close")
	b := &body{Reader: failingReader{readErr}, closeErr: closeErr}
	err := DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: b}, nil
	}), "GET", "https://example.test", nil, nil)
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("expected both failures: %v", err)
	}
	requireOperationStage(t, joined.Unwrap()[0], StageRead)
	requireOperationStage(t, joined.Unwrap()[1], StageClose)
	if !errors.Is(err, readErr) || !errors.Is(err, closeErr) || strings.Contains(err.Error(), "private") || !b.closed {
		t.Fatalf("joined causes, privacy or cleanup lost: %v", err)
	}
}

func TestOperationTransportCancellationHasOneStage(t *testing.T) {
	var observed error
	c, err := New(Options{OnRequest: func(e RequestEvent) { observed = e.Err }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = DoJSON(ctx, c, "GET", "https://user:private@example.test/?token=private", nil, nil)
	requireOperationStage(t, err, StageTransport)
	requireOperationStage(t, observed, StageTransport)
	var op *OperationError
	require.ErrorAs(t, err, &op)
	var original *url.Error
	if !errors.Is(err, context.Canceled) || !errors.As(err, &original) || err.Error() != "httpclient: request failed" {
		t.Fatalf("transport cancellation or privacy lost: %v", err)
	}
	if errors.Unwrap(op) != original { //nolint:errorlint // Assert immediate pointer identity to detect a redundant wrapper; errors.Is would hide it.
		t.Fatalf("redundant stage wrapper: %T", errors.Unwrap(op))
	}
}

func TestResponseLimitHasStableIdentity(t *testing.T) {
	call := func(output any) error {
		return DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("1234"))}, nil
		}), "GET", "https://example.test", nil, output, JSONOptions{MaxResponseBytes: 3})
	}
	var out any
	first, second := call(nil), call(&out)
	if !errors.Is(first, ErrResponseTooLarge) || !errors.Is(second, ErrResponseTooLarge) || first.Error() != "httpclient: response exceeds byte limit" {
		t.Fatalf("response limit has no stable errors.Is identity: %v, %v", first, second)
	}
}

func TestOperationStatusErrorPreservesContract(t *testing.T) {
	closeErr := errors.New("private close")
	err := DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: &body{Reader: strings.NewReader("private payload"), closeErr: closeErr}}, nil
	}), "GET", "https://example.test", nil, nil)
	var status *StatusError
	if !errors.As(err, &status) || status.StatusCode != 403 || !errors.Is(err, closeErr) || strings.Contains(err.Error(), "private") {
		t.Fatalf("status contract or close cause lost: %v", err)
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("expected status and close: %v", err)
	}
	requireOperationStage(t, joined.Unwrap()[1], StageClose)
}

func TestOperationTransportTypedNilError(t *testing.T) {
	var cause *OperationError
	b := &body{Reader: strings.NewReader("")}
	var err error
	require.NotPanics(t, func() {
		err = DoJSON(context.Background(), doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: b}, cause
		}), "GET", "https://example.test", nil, nil)
	})
	requireOperationStage(t, err, StageTransport)
	require.Equal(t, "httpclient: request failed", err.Error())
	require.True(t, b.closed)
	require.ErrorIs(t, err, cause)
	require.NotPanics(t, func() {
		require.NotErrorIs(t, err, context.Canceled)
		var transport *url.Error
		require.False(t, errors.As(err, &transport))
	})
}

func TestNilOperationErrorMethods(t *testing.T) {
	var err *OperationError
	require.NotPanics(t, func() {
		require.Equal(t, "httpclient: operation failed", err.Error())
	})
	require.NotPanics(t, func() {
		require.Nil(t, err.Unwrap())
		require.NotErrorIs(t, err, context.Canceled)
		var transport *url.Error
		require.False(t, errors.As(err, &transport))
	})
}
