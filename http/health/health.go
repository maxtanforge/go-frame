// Package healthhttp exposes readiness and liveness over HTTP.
package healthhttp

import (
	"net/http"
	"strings"

	"github.com/maxtanforge/go-frame/health"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/telemetry/logger"
)

// New borrows a checker. A missing checker is a configuration error, never a healthy response.
func New(c *health.Checker) http.Handler {
	return newHandler(c, "")
}

// NewLiveness borrows a checker and serves liveness regardless of URL path.
// Bind it to the desired GET/HEAD route; a nil checker still returns 503.
func NewLiveness(c *health.Checker) http.Handler { return newHandler(c, "/healthz") }

// NewReadiness borrows a checker and serves readiness regardless of URL path.
// Bind it to the desired GET/HEAD route; a nil checker still returns 503.
func NewReadiness(c *health.Checker) http.Handler { return newHandler(c, "/readyz") }

func newHandler(c *health.Checker, endpoint string) http.Handler {
	return respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if c == nil {
			return nil, &httperror.Error{Status: 503, Message: "health checker unavailable"}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			return nil, &httperror.Error{Status: 405, Message: "method not allowed"}
		}
		path := endpoint
		if path == "" {
			path = r.URL.Path
		}
		switch path {
		case "/healthz":
			return map[string]string{"status": "ok"}, nil
		case "/readyz":
			report := c.Readiness(r.Context())
			var failed []string
			for _, f := range report.Failures {
				if f.Optional {
					logger.FromContext(r.Context()).WarnContext(r.Context(), "optional readiness check failed", "component", f.Name, "error", f.Err)
				} else {
					failed = append(failed, f.Name)
					logger.FromContext(r.Context()).ErrorContext(r.Context(), "readiness check failed", "component", f.Name, "error", f.Err)
				}
			}
			if report.Status == "draining" {
				return nil, &httperror.Error{Status: 503, Message: "service is draining"}
			}
			if report.Status == "unavailable" {
				return nil, &httperror.Error{Status: 503, Message: "not ready: " + strings.Join(failed, ", ")}
			}
			return report, nil
		default:
			return nil, &httperror.Error{Status: 404, Message: "not found"}
		}
	})
}
