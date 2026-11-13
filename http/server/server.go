// Package server provides composable chi routing and configurable HTTP servers.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

// Server HTTP 服务器封装。
type Server struct {
	srv *http.Server
}

// NoWriteTimeout explicitly disables the write timeout for long-lived response
// streams (SSE, large downloads). WriteTimeout covers the whole request
// lifetime after headers, including handler time; the zero-value default is
// 60s, which suits ordinary JSON services. NoWriteTimeout is the only
// accepted negative value.
const NoWriteTimeout = -time.Second

// Options configures server resource limits. Zero uses 5s read-header, 30s read,
// 60s write, 60s idle and 1 MiB headers. Use NoWriteTimeout for response
// streams that must outlive the write deadline; ordinary JSON request deadlines
// are configured separately by the handler (for example, router.Options.Timeout).
type Options struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	// TLSConfig enables TLS for both Run and Serve. A certificate source is
	// required. Construction uses tls.Config.Clone, a shallow clone: callers
	// must keep referenced certificates, slices, maps and callback state safe
	// and immutable except where the standard library explicitly permits it.
	TLSConfig   *tls.Config
	ErrorLog    *log.Logger
	BaseContext func(net.Listener) context.Context
	ConnContext func(context.Context, net.Conn) context.Context
	ConnState   func(net.Conn, http.ConnState)
}

// New addr 形如 ":3000"。
func New(addr string, handler http.Handler) *Server {
	return newServer(addr, handler, Options{})
}

// NewWithOptions validates limits and constructs an HTTP server without listening.
func NewWithOptions(addr string, handler http.Handler, opts Options) (*Server, error) {
	if opts.ReadHeaderTimeout < 0 || opts.ReadTimeout < 0 || opts.IdleTimeout < 0 || opts.MaxHeaderBytes < 0 {
		return nil, fmt.Errorf("server: resource limits cannot be negative")
	}
	if opts.WriteTimeout < 0 && opts.WriteTimeout != NoWriteTimeout {
		return nil, fmt.Errorf("server: WriteTimeout cannot be negative; use NoWriteTimeout for streams")
	}
	if cfg := opts.TLSConfig; cfg != nil && len(cfg.Certificates) == 0 && cfg.GetCertificate == nil && cfg.GetConfigForClient == nil {
		return nil, errors.New("server: TLS requires a certificate source")
	}
	return newServer(addr, handler, opts), nil
}

func newServer(addr string, handler http.Handler, opts Options) *Server {
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	if opts.ReadHeaderTimeout == 0 {
		opts.ReadHeaderTimeout = 5 * time.Second
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 30 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 60 * time.Second
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 60 * time.Second
	}
	if opts.WriteTimeout == NoWriteTimeout {
		opts.WriteTimeout = 0
	}
	if opts.MaxHeaderBytes == 0 {
		opts.MaxHeaderBytes = 1 << 20
	}
	var tlsConfig *tls.Config
	if opts.TLSConfig != nil {
		tlsConfig = opts.TLSConfig.Clone()
	}
	return &Server{srv: &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: opts.ReadHeaderTimeout,
		ReadTimeout:       opts.ReadTimeout,
		WriteTimeout:      opts.WriteTimeout,
		IdleTimeout:       opts.IdleTimeout,
		MaxHeaderBytes:    opts.MaxHeaderBytes,
		TLSConfig:         tlsConfig,
		ErrorLog:          opts.ErrorLog,
		BaseContext:       opts.BaseContext,
		ConnContext:       opts.ConnContext,
		ConnState:         opts.ConnState,
	}}
}

// Serve serves an existing listener, useful for tests or custom listener setup.
func (s *Server) Serve(listener net.Listener) error {
	var err error
	if s.srv.TLSConfig != nil {
		err = s.srv.ServeTLS(listener, "", "")
	} else {
		err = s.srv.Serve(listener)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Close force-closes active connections. Hijacked connections are owned by the
// handler and must have their own lifecycle hook.
func (s *Server) Close() error { return s.srv.Close() }

// Run 阻塞启动;http.ErrServerClosed 是正常退出信号。
func (s *Server) Run() error {
	var err error
	if s.srv.TLSConfig != nil {
		err = s.srv.ListenAndServeTLS("", "")
	} else {
		err = s.srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown 优雅退出,排空在途请求。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
