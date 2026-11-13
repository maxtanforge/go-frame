package httpclient

import "errors"

// Stage identifies the operation that failed, independently of its cause.
type Stage string

// StageEncode, StageRequest, StageTransport, StageRead, StageDecode and StageClose
// identify the supported outbound failure stages.
const (
	StageEncode    Stage = "encode"    // JSON request encoding.
	StageRequest   Stage = "request"   // Request construction before sending.
	StageTransport Stage = "transport" // Do exchange, up to response headers.
	StageRead      Stage = "read"      // Reading a successful response body.
	StageDecode    Stage = "decode"    // JSON response decoding.
	StageClose     Stage = "close"     // Closing a received response body.
)

// ErrResponseTooLarge reports a successful response exceeding JSONOptions'
// byte limit, including when the response is discarded without decoding.
var ErrResponseTooLarge = errors.New("httpclient: response exceeds byte limit")

// OperationError classifies an outbound failure while keeping its public text
// free of URLs and payloads. Err remains available through errors.Is/As and may
// contain sensitive data. HTTP status failures use StatusError instead.
type OperationError struct {
	Stage Stage
	Err   error
}

func (e *OperationError) Error() string {
	if e == nil {
		return "httpclient: operation failed"
	}
	switch e.Stage {
	case StageEncode:
		return "httpclient: encode JSON failed"
	case StageRequest:
		return "httpclient: create request failed"
	case StageTransport:
		return "httpclient: request failed"
	case StageRead:
		return "httpclient: read response failed"
	case StageDecode:
		return "httpclient: decode JSON failed"
	case StageClose:
		return "httpclient: close response failed"
	default:
		return "httpclient: operation failed"
	}
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func safeError(stage Stage, cause error) error {
	// DoJSONRequest may receive an already classified error from Client.Do.
	// Only reuse the immediate matching stage: deeper errors may describe a
	// different operation and wrappers around them may have unsafe public text.
	if existing, ok := cause.(*OperationError); ok && existing != nil && existing.Stage == stage { //nolint:errorlint // Reuse only the immediate error; nested stages must retain their outer operation and wrapper.
		return existing
	}
	return &OperationError{Stage: stage, Err: cause}
}
