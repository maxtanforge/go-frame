// Package auth adapts credential verification to HTTP with default-required routes.
package auth

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/internal/isnil"
	"github.com/maxtanforge/go-frame/internal/requestmeta"
	"github.com/maxtanforge/go-frame/telemetry/logger"
)

// Verifier identifies invalid credentials with principal.ErrUnauthenticated.
// Infrastructure errors remain ordinary errors; methods must honor context.
type Verifier interface {
	Verify(context.Context, string) (principal.Principal, error)
}

// VerifyFunc adapts a plain function to Verifier.
type VerifyFunc func(context.Context, string) (principal.Principal, error)

// Verify implements Verifier by delegating to the function.
func (f VerifyFunc) Verify(ctx context.Context, raw string) (principal.Principal, error) {
	return f(ctx, raw)
}

// Endpoint identifies one method/path pair in the public catalog.
type Endpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Options configures the explicit public-endpoint allowlist and post-verification check.
type Options struct {
	PublicEndpoints []Endpoint
	// Check runs after verification and publishing the verified identity. Return
	// principal.ErrUnauthenticated for rejection, other errors for backend failure.
	Check func(context.Context, principal.Principal) error
}

// Auth guards routes: verification is required unless the endpoint is explicitly public.
type Auth struct {
	verifier  Verifier
	public    map[Endpoint]bool
	endpoints []Endpoint
	check     func(context.Context, principal.Principal) error
}

// New snapshots the allowlist; empty means every endpoint requires verification.
func New(v Verifier, o Options) (*Auth, error) {
	if isnil.Check(v) {
		return nil, errors.New("auth: verifier is required")
	}
	if len(o.PublicEndpoints) > 1024 {
		return nil, errors.New("auth: too many public endpoints")
	}
	a := &Auth{verifier: v, check: o.Check, public: make(map[Endpoint]bool), endpoints: append([]Endpoint(nil), o.PublicEndpoints...)}
	for _, e := range o.PublicEndpoints {
		if !validEndpoint(e) || a.public[e] {
			return nil, errors.New("auth: invalid or duplicate public endpoint")
		}
		a.public[e] = true
	}
	return a, nil
}

// PublicEndpoints returns a copy of the exact public policy for inspection.
func (a *Auth) PublicEndpoints() []Endpoint { return append([]Endpoint(nil), a.endpoints...) }
func validEndpoint(e Endpoint) bool {
	switch e.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
	default:
		return false
	}
	return canonical(e.Path)
}
func canonical(s string) bool {
	if len(s) == 0 || len(s) > 2048 || s[0] != '/' || strings.ContainsAny(s, "%?#\\*{}") || strings.Contains(s, "//") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	clean := path.Clean(s)
	return s == clean || s != "/" && strings.HasSuffix(s, "/") && strings.TrimSuffix(s, "/") == clean
}

// Middleware protects every request except exact declared public endpoints.
// Public routes ignore credentials; they do not acquire an authenticated actor.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath == "" && canonical(r.URL.Path) && a.public[Endpoint{r.Method, r.URL.Path}] {
			next.ServeHTTP(w, r)
			return
		}
		a.authenticate(w, r, next, false)
	})
}

// EndpointMiddleware binds policy to a trusted, router-resolved endpoint instead
// of the full URL path. Use only after matching the actual route; never derive
// endpoint from client headers or apply a public endpoint to a whole router.
// The original request URL is preserved and must still be canonical.
func (a *Auth) EndpointMiddleware(endpoint Endpoint) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == endpoint.Method && r.URL.RawPath == "" && canonical(r.URL.Path) && a.public[endpoint] {
				next.ServeHTTP(w, r)
				return
			}
			a.authenticate(w, r, next, false)
		})
	}
}

// Require wraps a handler with unconditional verification; the authenticated
// principal and operation context are injected before it runs.
func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.authenticate(w, r, next, false) })
}

// Optional permits missing credentials, but rejects present invalid credentials.
func (a *Auth) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.authenticate(w, r, next, true) })
}
func (a *Auth) authenticate(w http.ResponseWriter, r *http.Request, next http.Handler, optional bool) {
	ctx := requestmeta.Ensure(r.Context())
	r = r.WithContext(ctx)
	if err := ctx.Err(); err != nil {
		respond.WriteError(w, r, err)
		return
	}
	values := r.Header.Values("Authorization")
	if optional && len(values) == 0 {
		next.ServeHTTP(w, r)
		return
	}
	if len(values) != 1 || len(values[0]) > 16391 {
		reject(w, r, principal.ErrUnauthenticated)
		return
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" || strings.ContainsAny(parts[1], "\t\r\n,") {
		reject(w, r, principal.ErrUnauthenticated)
		return
	}
	p, err := a.verifier.Verify(ctx, parts[1])
	if err == nil {
		err = p.Validate()
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		reject(w, r, err)
		return
	}
	if !requestmeta.Publish(ctx, p) {
		respond.WriteError(w, r, errors.New("auth: identity already published"))
		return
	}
	ctx = principal.IntoContext(ctx, p)
	ctx = logger.IntoContext(ctx, logger.FromContext(ctx).With("actor_id", p.ID, "actor_name", p.Name, "actor_issuer", p.Issuer))
	r = r.WithContext(ctx)
	if a.check != nil {
		if err := a.check(ctx, p); err != nil {
			reject(w, r, err)
			return
		}
	}
	if err := ctx.Err(); err != nil {
		reject(w, r, err)
		return
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}
func reject(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, principal.ErrUnauthenticated) && !errors.Is(err, context.DeadlineExceeded) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		respond.WriteError(w, r, &httperror.Error{Status: 401, Message: "Unauthorized"})
		return
	}
	respond.WriteError(w, r, err)
}
