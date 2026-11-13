package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/internal/isnil"
)

// Declaration holds a route and its handler-construction result until Collect.
// Its fields are private so construction errors cannot be accidentally discarded.
type Declaration struct {
	route Route
	err   error
}

// Bind attaches a handler-construction result to a copy of the route metadata.
// A binding factory's (http.Handler, error) result can be its sole argument.
func (r Route) Bind(handler http.Handler, err error) Declaration {
	if r.Handler != nil {
		err = errors.Join(errors.New("handler already set"), err)
	}
	r.Handler = handler
	return Declaration{route: r, err: err}
}

// Collect validates declarations atomically and preserves construction errors.
// Errors identify the module, method and path; no routes are returned on failure.
// Modules must be nonempty, at most 128 bytes, without surrounding whitespace
// or control characters. Health-route conflicts are checked at startup.
func Collect(module string, entries ...Declaration) ([]Route, error) {
	if module == "" || len(module) > 128 || strings.TrimSpace(module) != module || strings.ContainsFunc(module, unicode.IsControl) {
		return nil, errors.New("router: invalid module identifier")
	}
	if len(entries) > 4096 {
		return nil, fmt.Errorf("router: module %q: too many routes", module)
	}
	routes := make([]Route, 0, len(entries))
	seen := make(map[auth.Endpoint]bool, len(entries))
	patterns := chi.NewRouter()
	for _, entry := range entries {
		route := entry.route
		err := entry.err
		if err == nil {
			err = validateRoute(route)
		}
		endpoint := auth.Endpoint{Method: route.Method, Path: route.Path}
		if err == nil && seen[endpoint] {
			err = errors.New("router: duplicate route declaration")
		}
		if err == nil {
			err = mountRoute(patterns, route)
		}
		if err != nil {
			return nil, fmt.Errorf("router: module %q: %s %q: %w", module, route.Method, route.Path, err)
		}
		seen[endpoint] = true
		routes = append(routes, route)
	}
	if err := validateMountedRoutes(patterns, routes); err != nil {
		return nil, fmt.Errorf("router: module %q: %w", module, err)
	}
	return routes, nil
}

// Registration trees here contain only flat Method registrations. Compare chi's
// compiled endpoints with the declarations so parameter aliases cannot silently
// replace handlers. Different methods and distinct matchers remain valid.
func validateMountedRoutes(compiled chi.Routes, declarations []Route) error {
	mounted := make(map[auth.Endpoint]bool, len(declarations))
	for _, route := range compiled.Routes() {
		for method := range route.Handlers {
			mounted[auth.Endpoint{Method: method, Path: route.Pattern}] = true
		}
	}
	for _, route := range declarations {
		if !mounted[auth.Endpoint{Method: route.Method, Path: route.Path}] {
			return routeError(route, errors.New("route pattern overwritten by an equivalent declaration"))
		}
	}
	return nil
}

func routeError(route Route, err error) error {
	return fmt.Errorf("router: %s %q: %w", route.Method, route.Path, err)
}

func validateRoute(route Route) error {
	switch route.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
	default:
		return errors.New("invalid method")
	}
	if isnil.Check(route.Handler) {
		return errors.New("handler required")
	}
	if len(route.Path) == 0 || len(route.Path) > 2048 || route.Path[0] != '/' || strings.ContainsAny(route.Path, "?#%\\") {
		return errors.New("invalid route path")
	}
	if err := route.Operation.Validate(); err != nil {
		return fmt.Errorf("invalid route metadata: %w", err)
	}
	m := route.Operation
	m.Route = route.Path
	if err := m.Validate(); err != nil {
		return fmt.Errorf("invalid route metadata: %w", err)
	}
	if route.Public {
		if err := validatePublicEndpoint(auth.Endpoint{Method: route.Method, Path: route.Path}); err != nil {
			return err
		}
	}
	return nil
}

func validatePublicEndpoint(endpoint auth.Endpoint) error {
	// Reuse auth's canonical literal-path validation; never broaden its policy.
	_, err := auth.New(auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
		return principal.Principal{}, principal.ErrUnauthenticated
	}), auth.Options{PublicEndpoints: []auth.Endpoint{endpoint}})
	return err
}

func mountRoute(routes chi.Router, route Route) (err error) {
	defer func() {
		if cause := recover(); cause != nil {
			if causeErr, ok := cause.(error); ok {
				err = routeError(route, fmt.Errorf("invalid route declaration: %w", causeErr))
			} else {
				err = routeError(route, fmt.Errorf("invalid route declaration: %v", cause))
			}
		}
	}()
	routes.Method(route.Method, route.Path, route.Handler)
	return nil
}
