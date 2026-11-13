package router_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/http/router"
)

func TestMiddlewareConstructionPanicReportsNameAndCause(t *testing.T) {
	cause := errors.New("cannot construct middleware")
	for _, scope := range []string{"application", "route"} {
		for _, phase := range []router.Phase{router.Matched, router.AuthenticatedPhase, router.BeforeHandler, router.Response} {
			for _, tc := range []struct {
				name      string
				payload   any
				wantCause error
			}{{"error", cause, cause}, {"value", "private panic detail", nil}} {
				t.Run(scope+"/"+phaseName(phase)+"/"+tc.name, func(t *testing.T) {
					entry := router.Middleware{Name: "broken-wrapper", Phase: phase, Wrap: func(http.Handler) http.Handler {
						panic(tc.payload)
					}}
					route := router.Route{Method: "GET", Path: "/items", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
						t.Fatal("handler ran during construction")
					})}
					options := router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Verifier: auth.VerifyFunc(actorVerifier)}
					if scope == "application" {
						options.Middleware = []router.Middleware{entry}
					} else {
						route.Middleware = []router.Middleware{entry}
					}
					options.Routes = []router.Route{route}
					routes, err := router.NewAuthenticated(quietLogger(), options)
					if routes != nil || err == nil {
						t.Fatalf("construction must return no router on panic: router=%v err=%v", routes, err)
					}
					if !strings.Contains(err.Error(), entry.Name) {
						t.Fatalf("construction error lacks middleware name: %v", err)
					}
					if tc.wantCause != nil && !errors.Is(err, tc.wantCause) {
						t.Fatalf("construction error lost panic cause: %v", err)
					}
					if strings.Contains(err.Error(), "private panic detail") {
						t.Fatalf("non-error panic payload leaked: %v", err)
					}
					if scope == "route" && (!strings.Contains(err.Error(), "GET") || !strings.Contains(err.Error(), "/items")) {
						t.Fatalf("construction error lacks route context: %v", err)
					}
				})
			}
		}
	}
}

func phaseName(phase router.Phase) string {
	switch phase {
	case router.Matched:
		return "matched"
	case router.AuthenticatedPhase:
		return "authenticated"
	case router.BeforeHandler:
		return "before-handler"
	default:
		return "response"
	}
}

func TestLateRouteRegistrationReportsMiddlewareConstructionFailure(t *testing.T) {
	cause := errors.New("cannot construct late middleware")
	for _, phase := range []router.Phase{router.Matched, router.AuthenticatedPhase, router.BeforeHandler, router.Response} {
		t.Run(phaseName(phase), func(t *testing.T) {
			routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{
				Router:   router.Options{DisableHealthMount: true},
				Verifier: auth.VerifyFunc(actorVerifier),
				Middleware: []router.Middleware{{Name: "late-wrapper", Phase: phase, Wrap: func(http.Handler) http.Handler {
					panic(cause)
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var caught any
			func() {
				defer func() { caught = recover() }()
				routes.Get("/items", func(http.ResponseWriter, *http.Request) { t.Fatal("handler ran during registration") })
			}()
			constructionErr, ok := caught.(error)
			if !ok || !errors.Is(constructionErr, cause) || !strings.Contains(constructionErr.Error(), "late-wrapper") {
				t.Fatalf("registration did not report the middleware construction failure: %v", caught)
			}
		})
	}
}

func TestLateRouteRegistrationKeepsApplicationMiddlewareAndAuthentication(t *testing.T) {
	var constructed, wrapped, handled int
	routes, err := router.NewAuthenticated(quietLogger(), router.AuthenticatedOptions{
		Router:   router.Options{DisableHealthMount: true},
		Verifier: auth.VerifyFunc(actorVerifier),
		Middleware: []router.Middleware{{Name: "observe", Phase: router.Response, Wrap: func(next http.Handler) http.Handler {
			constructed++
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wrapped++
				next.ServeHTTP(w, r)
			})
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	routes.Get("/items", func(w http.ResponseWriter, _ *http.Request) { handled++; w.WriteHeader(http.StatusNoContent) })
	for _, credential := range []string{"", "Bearer valid"} {
		request := httptest.NewRequest("GET", "/items", nil)
		if credential != "" {
			request.Header.Set("Authorization", credential)
		}
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if credential != "" {
			want = http.StatusNoContent
		}
		if response.Code != want {
			t.Fatalf("credential %q: status=%d want=%d", credential, response.Code, want)
		}
	}
	if constructed == 0 || wrapped != 2 || handled != 1 {
		t.Fatalf("constructed=%d wrapped=%d handled=%d", constructed, wrapped, handled)
	}
}
