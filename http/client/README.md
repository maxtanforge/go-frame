# Outbound HTTP

`httpclient.New(httpclient.Options{})` creates an independently owned transport.
Reuse one client per configuration and call `CloseIdleConnections` during shutdown.
`Do` accepts standard requests and leaves response body cleanup to its caller.

Zero options select these defaults:

| Option | Default |
| --- | --- |
| Timeout | 30 seconds, including response body reads |
| DialTimeout | 5 seconds |
| TLSHandshakeTimeout | 5 seconds |
| ResponseHeaderTimeout | 10 seconds |
| IdleConnTimeout | 90 seconds |
| MaxConnsPerHost | 100 |
| MaxIdleConns | 100 |
| MaxIdleConnsPerHost | 10 |

Negative values are rejected. For streaming, create a separate client with
`DisableTimeout: true` and manage request context cancellation. Combining that
flag with a nonzero `Timeout` is invalid. Dial, TLS, and header timeouts remain
active. Proxy environment variables follow standard `http` behavior.

Redirects are returned without following. The package adds no application-level
retries. The standard transport may transparently retry requests it considers
replayable (for example, some failures on reused connections); this is not an
exactly-once execution guarantee. Design mutation endpoints around their own
idempotency requirements. A generic retry helper stays out of the library:
whether a failed call may repeat is a business decision about idempotency and
side effects, and belongs to the caller.

`RootCAs` pins TLS trust anchors for private PKI environments; nil keeps system
roots. Load the pool only from operator-controlled certificate files.

`OnRequest` observes every completed exchange, including the JSON helpers: one
synchronous `RequestEvent{Method, Host, Status, Duration, Err}` per `Do`.
Duration covers the exchange up to response headers; body reads owned by the
caller are not included. Status is zero when the exchange never reached a
response. The callback must be fast and concurrency-safe; `Err` is this
package's sanitized error. Keep metric labels bounded — Host and Method are
reasonable, full URLs and query strings are not.

`Do` clones the request and headers and propagates an existing valid framework
`x-trace-id` from its context. Explicit headers, including empty headers, win.
Correlation IDs must contain 1–128 ASCII letters, digits, dots, underscores, or
hyphens. This is correlation only, not authentication or W3C trace propagation.
The request body keeps normal `http` consumption and close semantics.

```go
client, err := httpclient.New(httpclient.Options{})
if err != nil {
    return err
}
defer client.CloseIdleConnections()

var result struct { Name string `json:"name"` }
err = httpclient.DoJSON(ctx, client, http.MethodGet, endpoint, nil, &result)
```

`DoJSON` accepts any `Doer`, enabling small consumer interfaces and test fakes.
Nil input sends no body; nonnil input is JSON encoded. It sets JSON Accept and,
when sending a body, Content-Type. Successful responses are limited to 1 MiB;
one optional `JSONOptions{MaxResponseBytes: n}` supplies a positive override.
Negative limits or multiple option values are rejected. With nonnil output,
exactly one JSON value is required; trailing data and empty bodies fail. Use nil
output for 204 responses or bounded discard. Non-2xx responses return
`*StatusError` with `StatusCode`; up to 8 KiB of the error body is drained
before closing so small error envelopes keep the connection reusable.

The helper closes every received body, including failure paths, and reports
close errors. Returned error strings exclude request URLs, credentials, and
payloads. Causes remain available through `errors.Is`/`errors.As`, including
context cancellation; unwrapped errors can contain sensitive data, so log the
public error rather than its cause.

Added in v0.2.2: `*OperationError` exposes `Stage` and the
original `Err`. Use `errors.As` to classify the failed operation without
matching error text; `errors.Is` still identifies cancellation and other
original causes. Its public text remains sanitized, including when an error
is passed from the owned `Client` through a JSON helper.

| Stage | Failed operation |
| --- | --- |
| `StageEncode` | Encoding the JSON request body |
| `StageRequest` | Constructing the request before sending |
| `StageTransport` | Executing `Do`, up to response headers |
| `StageRead` | Reading a successful response body |
| `StageDecode` | Decoding the JSON response |
| `StageClose` | Closing a received response body |

`errors.Is(err, httpclient.ErrResponseTooLarge)` identifies the response byte
limit independently of decoding, including bounded discard. This sentinel is
returned directly. Non-2xx responses continue to use `*StatusError`; validation
errors such as a nil request or invalid options do not receive a transport
stage. A stage describes where failure occurred, not whether a call may be
retried. In particular, body reads may fail with context cancellation after
response headers have arrived.

```go
var operation *httpclient.OperationError
if errors.As(err, &operation) {
    // Stage is bounded metadata; Err may contain private upstream details.
    logger.ErrorContext(ctx, "upstream call failed",
        "stage", operation.Stage, "error", err)
}
```

If cleanup also fails, the helper joins the primary error and a `StageClose`
error. `errors.Is` can find either original cause; `errors.As` returns the first
matching error in the chain. `OnRequest` still observes only the exchange up to
headers. Observe the surrounding business operation to include encoding, body
reads, decoding, and cleanup in its outcome and duration.

For authentication, custom headers or other request settings, prepare a standard
request and execute it with the same bounded JSON response handling:

```go
req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
if err != nil {
    return err
}
req.Header.Set("Authorization", "Bearer " + token)
req.Header.Set("Accept", "application/vnd.example+json")
err = httpclient.DoJSONRequest(client, req, &result)
```

`DoJSONRequest` adds no headers and preserves the request context. It clones
request metadata before invoking the `Doer`; the body is shared and retains
standard consumption and close semantics. Nil requests are rejected. Response
limits, decoding, status errors, privacy, and response-body cleanup are shared
with `DoJSON`.

Both JSON helpers reject nil and typed-nil Doers with an error, including nil
`*http.Client`, nil owned clients and nil function adapters. A missing dependency
does not cause a dereference panic during request execution.
