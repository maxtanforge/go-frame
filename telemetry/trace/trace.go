// Package trace provides request correlation IDs, response headers and context propagation.
// 日志(RequestLog)与响应包络(respond)共用同一 traceId,是链路追踪的单一事实源。
//
// The default generator is a process-shared Snowflake with node 0. Deployments
// with multiple instances must inject generators with distinct node IDs.
// These are correlation IDs, not W3C trace contexts or trusted identity.
package trace

import (
	"context"
	"net/http"
	"strconv"

	"github.com/maxtanforge/go-frame/id"
	"github.com/maxtanforge/go-frame/internal/isnil"
)

// HeaderTraceID 上游注入与响应回写的头名。
const HeaderTraceID = "x-trace-id"

// Node 0 is always within NewSnowflake's validated range. Sharing the default
// keeps independent middleware instances in this process on one sequence.
var defaultSnowflake, _ = id.NewSnowflake(0)

type idGenerator interface{ Next() (int64, error) }

type config struct {
	sf      idGenerator
	onError func(error)
}

// Option Trace 中间件配置。
type Option func(*config)

// WithGenerator selects an explicit ID generator (for example *id.Snowflake).
// Nil (including typed nil) selects the shared default Snowflake. Generation
// failures are reported to WithGenerateError and reject the request with 500.
func WithGenerator(g idGenerator) Option {
	if isnil.Check(g) {
		g = nil
	}
	return func(c *config) { c.sf = g }
}

// WithSnowflake selects an explicitly coordinated Snowflake generator.
// nil selects the shared default Snowflake.
func WithSnowflake(sf *id.Snowflake) Option {
	return WithGenerator(sf)
}

// WithGenerateError registers an observer invoked when the configured
// generator fails. Observers run on the request
// path; keep them cheap. A panicking observer is recovered by the router's
// outer Recoverer like any handler panic, producing a generic 500 before
// business handling. Standalone Middleware users must supply their own recovery.
func WithGenerateError(fn func(error)) Option {
	return func(c *config) { c.onError = fn }
}

// Middleware accepts upstream IDs of 1–128 ASCII letters, digits, '.', '_' or '-';
// absent or invalid IDs are replaced with a generated ID.
// 回写响应头并注入 ctx。
func Middleware(opts ...Option) func(http.Handler) http.Handler {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tid := r.Header.Get(HeaderTraceID)
			if !ValidID(tid) {
				var err error
				tid, err = generate(c.sf, c.onError)
				if err != nil {
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
			}
			w.Header().Set(HeaderTraceID, tid)
			next.ServeHTTP(w, r.WithContext(IntoContext(r.Context(), tid)))
		})
	}
}

// generate uses the configured generator or the shared default Snowflake.
// Generator failures are reported without switching nodes or ID algorithms.
func generate(g idGenerator, onError func(error)) (string, error) {
	if g == nil {
		g = defaultSnowflake
	}
	n, err := g.Next()
	if err != nil {
		if onError != nil {
			onError(err)
		}
		return "", err
	}
	return strconv.FormatInt(n, 10), nil
}

// ValidID checks the shared inbound/outbound correlation ID format: 1–128 ASCII
// letters, digits, dots, underscores or hyphens. Validity does not establish trust.
func ValidID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

type ctxKey struct{}

// IntoContext 把 traceId 挂进 ctx。
func IntoContext(ctx context.Context, tid string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tid)
}

// FromContext 取 traceId;无则空串。
func FromContext(ctx context.Context) string {
	if tid, ok := ctx.Value(ctxKey{}).(string); ok {
		return tid
	}
	return ""
}
