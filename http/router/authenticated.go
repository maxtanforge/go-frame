package router

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/internal/requestmeta"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

// Route declares one route and its authentication/operation metadata together.
// Public permits literal paths only. Handler remains ordinary net/http.
type Route struct {
	Method, Path string
	Handler      http.Handler
	Public       bool
	Operation    operationhttp.Metadata
	Middleware   []Middleware
}

// RoutePolicy is one catalog row: the declared endpoint with its resolved
// public flag and operation metadata.
type RoutePolicy struct {
	Method    string                 `json:"method"`
	Path      string                 `json:"path"`
	Public    bool                   `json:"public"`
	Operation operationhttp.Metadata `json:"operation"`
}

// AuthenticatedOptions declares routes, the verifier and optional policies.
type AuthenticatedOptions struct {
	Router   Options
	Verifier auth.Verifier
	Check    func(context.Context, principal.Principal) error
	Routes   []Route
	// Middleware supplies named application wrappers for the fixed route phases.
	Middleware []Middleware
	// Groups add explicitly pathed routes with group-level middleware.
	Groups []Group
	// PublicEndpoints is for explicitly exposing built-in health or other
	// registered routes. Duplicates with Route.Public are rejected.
	PublicEndpoints []auth.Endpoint
	Operations      *operationhttp.Recorder
}

// Authenticated wraps chi without replacing it. Add routes only before serving.
// New method/path pairs added afterwards remain protected and are not in the
// startup catalog. Do not overwrite declared routes or mutate routes while serving.
type Authenticated struct {
	chi.Router
	policies []RoutePolicy
}

// Policies returns an independent snapshot of the startup catalog, sorted by
// path then method. Routes added later are not included.
func (r *Authenticated) Policies() []RoutePolicy {
	policies := append([]RoutePolicy(nil), r.policies...)
	for i := range policies {
		policies[i].Operation = policies[i].Operation.Clone()
	}
	return policies
}

// NewAuthenticated compiles a fixed public policy; empty means all protected.
// This is an optional composition entry point, not a dependency of core auth.
// It includes New's trace, logging, recovery, deadlines and health endpoints.
// Configure those through Router; do not wrap it in another framework router.
func NewAuthenticated(lg *slog.Logger, o AuthenticatedOptions) (result *Authenticated, err error) {
	if lg == nil {
		lg = slog.Default()
	}
	// chi rejects invalid pattern declarations with panic. Convert construction
	// mistakes to a safe startup error, never an accidentally unprotected router.
	defer func() {
		if cause := recover(); cause != nil {
			result = nil
			if causeErr, ok := cause.(error); ok {
				err = causeErr
			} else {
				err = errors.New("router: invalid route declaration")
			}
		}
	}()
	if len(o.Routes) > 4096 || len(o.Groups) > 4096 {
		return nil, errors.New("router: too many routes")
	}
	o.Routes = append([]Route(nil), o.Routes...)
	groupNames := map[string]bool{}
	for _, group := range o.Groups {
		if !validMiddlewareName(group.Name) || groupNames[group.Name] || len(o.Routes)+len(group.Routes) > 4096 {
			return nil, errors.New("router: invalid route group")
		}
		groupNames[group.Name] = true
		for _, route := range group.Routes {
			route.Middleware = append(cloneMiddleware(group.Middleware), cloneMiddleware(route.Middleware)...)
			o.Routes = append(o.Routes, route)
		}
	}
	application, err := orderMiddleware(cloneMiddleware(o.Middleware))
	if err != nil {
		return nil, err
	}
	plans := make(map[auth.Endpoint]map[Phase][]Middleware, len(o.Routes))
	catalog := map[auth.Endpoint]operationhttp.Metadata{}
	if !o.Router.DisableHealthMount {
		for _, p := range []string{"/healthz", "/readyz"} {
			for _, m := range []string{"GET", "HEAD"} {
				catalog[auth.Endpoint{Method: m, Path: p}] = operationhttp.Metadata{Route: p}
			}
		}
	}
	public := append([]auth.Endpoint(nil), o.PublicEndpoints...)
	for _, route := range o.Routes {
		if err := validateRoute(route); err != nil {
			return nil, routeError(route, err)
		}
		e := auth.Endpoint{Method: route.Method, Path: route.Path}
		if _, exists := catalog[e]; exists {
			return nil, routeError(route, errors.New("duplicate route declaration"))
		}
		m := route.Operation.Clone()
		m.Route = route.Path
		catalog[e] = m
		plan, err := orderMiddleware(append(cloneMiddleware(o.Middleware), cloneMiddleware(route.Middleware)...))
		if err != nil {
			return nil, routeError(route, err)
		}
		plans[e] = plan
		if route.Public {
			public = append(public, e)
		}
	}
	seenPublic := make(map[auth.Endpoint]bool, len(public))
	for _, e := range public {
		if err := validatePublicEndpoint(e); err != nil {
			return nil, routeError(Route{Method: e.Method, Path: e.Path}, err)
		}
		if seenPublic[e] {
			return nil, routeError(Route{Method: e.Method, Path: e.Path}, errors.New("duplicate public endpoint"))
		}
		seenPublic[e] = true
		if _, exists := catalog[e]; !exists {
			return nil, routeError(Route{Method: e.Method, Path: e.Path}, errors.New("public endpoint is not registered"))
		}
	}
	guard, err := auth.New(o.Verifier, auth.Options{PublicEndpoints: public, Check: o.Check})
	if err != nil {
		return nil, err
	}
	isPublic := map[auth.Endpoint]bool{}
	for _, e := range guard.PublicEndpoints() {
		isPublic[e] = true
	}
	policies := make([]RoutePolicy, 0, len(catalog))
	for e, m := range catalog {
		policies = append(policies, RoutePolicy{e.Method, e.Path, isPublic[e], m})
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Path != policies[j].Path {
			return policies[i].Path < policies[j].Path
		}
		return policies[i].Method < policies[j].Method
	})
	var routes chi.Router
	gate := func(next http.Handler) http.Handler {
		next = protectDispatch(next)
		fallback := next
		for _, phase := range []Phase{BeforeHandler, AuthenticatedPhase} {
			fallback, err = wrapMiddleware(fallback, application[phase])
			if err != nil {
				panic(err)
			}
		}
		fallback = guard.Require(fallback)
		fallback, err = wrapMiddleware(fallback, application[Matched])
		if err != nil {
			panic(err)
		}
		fallback = respond.Recoverer(fallback)
		fallback, err = wrapMiddleware(fallback, application[Response])
		if err != nil {
			panic(err)
		}
		secured := guard.Require(next)
		prepared := make(map[auth.Endpoint]http.Handler, len(catalog))
		build := func(endpoint auth.Endpoint, m operationhttp.Metadata) (http.Handler, error) {
			plan, declared := plans[endpoint]
			if !declared {
				plan = application
			}
			handler := next
			if !declared {
				handler, err = wrapMiddleware(handler, plan[BeforeHandler])
				if err != nil {
					return nil, err
				}
			}
			handler, err = wrapMiddleware(handler, plan[AuthenticatedPhase])
			if err != nil {
				return nil, err
			}
			handler = guard.EndpointMiddleware(endpoint)(handler)
			handler, err = wrapMiddleware(handler, plan[Matched])
			if err != nil {
				return nil, err
			}
			handler = respond.Recoverer(handler)
			handler, err = wrapMiddleware(handler, plan[Response])
			if err != nil {
				return nil, err
			}
			policy := RoutePolicy{Method: endpoint.Method, Path: endpoint.Path, Public: isPublic[endpoint], Operation: m.Clone()}
			handler = operationhttp.With(m)(handler)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), policyKey{}, policy)))
			}), nil
		}
		for endpoint, m := range catalog {
			handler, err := build(endpoint, m)
			if err != nil {
				panic(routeError(Route{Method: endpoint.Method, Path: endpoint.Path}, err))
			}
			prepared[endpoint] = handler
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := requestTarget{method: r.Method, path: r.URL.Path, rawPath: r.URL.RawPath}
			if rc := chi.RouteContext(r.Context()); rc != nil {
				target.routePath = rc.RoutePath
			}
			r = r.WithContext(context.WithValue(r.Context(), targetKey{}, target))
			probe := chi.NewRouteContext()
			routePath := r.URL.RawPath
			if routePath == "" {
				routePath = r.URL.Path
			}
			// chi changes RoutePath at each mount while preserving the full URL.
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePath != "" {
				routePath = rc.RoutePath
			}
			if routes.Match(probe, r.Method, routePath) {
				requestmeta.PublishParams(r.Context(), probe.URLParams.Keys, probe.URLParams.Values)
				endpoint := auth.Endpoint{Method: r.Method, Path: probe.RoutePattern()}
				if h, ok := prepared[endpoint]; ok {
					h.ServeHTTP(w, r)
					return
				}
				m := operationhttp.Metadata{Route: endpoint.Path}
				policy := RoutePolicy{Method: endpoint.Method, Path: endpoint.Path, Operation: m}
				r = r.WithContext(context.WithValue(r.Context(), policyKey{}, policy))
				operationhttp.With(m)(fallback).ServeHTTP(w, r)
				return
			}
			secured.ServeHTTP(w, r)
		})
	}
	opts := o.Router
	opts.Middleware = nil
	if o.Operations != nil {
		opts.Middleware = append(opts.Middleware, o.Operations.Middleware)
	}
	opts.Middleware = append(opts.Middleware, gate)
	for _, wrapper := range o.Router.Middleware {
		if wrapper != nil {
			opts.Middleware = append(opts.Middleware, func(next http.Handler) http.Handler {
				return wrapper(protectDispatch(next))
			})
		}
	}
	routes = New(lg, opts)
	for _, route := range o.Routes {
		route.Handler = protectDeclared(route.Handler, route.Method, route.Path)
		route.Handler, err = wrapMiddleware(route.Handler, plans[auth.Endpoint{Method: route.Method, Path: route.Path}][BeforeHandler])
		if err != nil {
			return nil, routeError(route, err)
		}
		if err := mountRoute(routes, route); err != nil {
			return nil, err
		}
	}
	// chi builds its middleware chain when the first endpoint is mounted.
	// Gate failures panic so they reach the constructor's recovery boundary, or
	// abort a later first registration if this router initially has no endpoints.
	if err := validateMountedRoutes(routes, o.Routes); err != nil {
		return nil, err
	}
	if !o.Router.DisableHealthMount {
		var protected []string
		for _, path := range []string{"/healthz", "/readyz"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				if !isPublic[auth.Endpoint{Method: method, Path: path}] {
					protected = append(protected, method+" "+path)
				}
			}
		}
		if len(protected) > 0 {
			lg.Warn("built-in health endpoints require authentication", "endpoints", protected, "configuration", "declare PublicEndpoints for unauthenticated probes or set DisableHealthMount")
		}
	}
	return &Authenticated{Router: routes, policies: policies}, nil
}
