// Package authenticated demonstrates explicit auth, operation and audit wiring.
// It does not provide a login endpoint or decide how business credentials work.
package authenticated

import (
	"log/slog"
	"net/http"

	"github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	auditprincipal "github.com/maxtanforge/go-frame/database/audit/principal"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

// NewHandler assembles the authenticated example router with its fixed policy.
func NewHandler(lg *slog.Logger, verifier auth.Verifier, sink operation.Sink) (*router.Authenticated, error) {
	recorder, err := operationhttp.New(sink, operationhttp.Options{})
	if err != nil {
		return nil, err
	}
	return router.NewAuthenticated(lg, router.AuthenticatedOptions{Verifier: verifier, Operations: recorder,
		Middleware: []router.Middleware{{Name: "response.headers", Phase: router.Response, Wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
			})
		}}},
		PublicEndpoints: []auth.Endpoint{{Method: "GET", Path: "/healthz"}, {Method: "HEAD", Path: "/healthz"}, {Method: "GET", Path: "/readyz"}, {Method: "HEAD", Path: "/readyz"}},
		Routes: []router.Route{
			{Method: "GET", Path: "/public/info", Public: true, Operation: operationhttp.Metadata{Action: "service.info"}, Handler: respond.Adapter(func(http.ResponseWriter, *http.Request) (any, error) {
				return map[string]string{"service": "example"}, nil
			})},
			{Method: "GET", Path: "/me", Operation: operationhttp.Metadata{Action: "profile.read", Description: "Read own profile"}, Handler: respond.Adapter(func(_ http.ResponseWriter, r *http.Request) (any, error) {
				p, err := principal.Require(r.Context())
				if err != nil {
					return nil, err
				}
				actor, err := auditprincipal.FromContext(r.Context())
				if err != nil {
					return nil, err
				}
				// A write use case would receive actor explicitly and call audit.New or
				// Fields.WithUpdate inside its transaction. Do not persist audit in this GET.
				return struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					ActorID string `json:"actor_id"`
				}{p.ID, p.Name, actor.ID}, nil
			})},
		},
	})
}
