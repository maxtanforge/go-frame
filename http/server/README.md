# HTTP server

`http/server` uses only the Go standard library and accepts any `http.Handler`.
Use [`http/router`](../router/README.md) when you want the framework's chi routing,
response envelope, health endpoints and request middleware.

`New(addr, handler)` retains the default server settings. `NewWithOptions`
validates resource limits and accepts application-owned TLS and server hooks.
Neither constructor opens a listener. A nil handler serves 404 responses.

Zero limits select a 5-second read-header timeout, 30-second read timeout,
60-second write timeout, 60-second idle timeout and 1 MiB header limit. The
write timeout covers the whole request lifetime after headers, including
handler time; use `server.NoWriteTimeout` (the only accepted negative value)
for long-lived response streams such as SSE. Other negative limits are rejected.

```go
srv, err := server.NewWithOptions("127.0.0.1:8443", handler, server.Options{
    TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}},
    ErrorLog: logger,
    BaseContext: func(net.Listener) context.Context { return appContext },
})
```

Supplying `TLSConfig` enables TLS for both `Run()` and `Serve(listener)`.
Pass a plain TCP listener to `Serve`; the server performs the TLS wrapping.
The configuration must supply `Certificates`, `GetCertificate`, or
`GetConfigForClient`. Certificate validity and callback results are checked by
the standard TLS handshake. Without `TLSConfig`, the server uses plain HTTP.

Construction calls `tls.Config.Clone`, which is a shallow clone. Treat shared
certificate data, slices, maps, and callback state as immutable or synchronize
them as allowed by the standard library. Do not mutate a configuration while
it is being cloned. Certificate rotation can use the standard TLS callbacks.

`ErrorLog`, `BaseContext`, `ConnContext`, and `ConnState` have the same semantics
as their `http.Server` counterparts. Context callbacks must return nonnil
contexts. Hooks should return promptly, and shared hook state must support
concurrent access. Nil hooks and a nil logger retain standard library behavior.

`Shutdown(ctx)` drains active requests; `Close()` force-closes connections.
Both entry points treat `http.ErrServerClosed` as a normal exit. Hijacked
connections remain the handler's lifecycle responsibility.
