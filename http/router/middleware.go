package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/internal/isnil"
)

// Phase selects a fixed execution boundary in an authenticated router.
type Phase uint8

const (
	// Matched runs with route metadata before authentication. No trusted actor is available yet.
	Matched Phase = iota + 1
	// AuthenticatedPhase runs after successful authentication/check, or a public exemption.
	AuthenticatedPhase
	// BeforeHandler runs around the registered handler, including binding and response output.
	BeforeHandler
	// Response wraps all matched-route execution, including authentication and
	// early rejection. Enter runs before Matched; deferred exit runs after it.
	Response
)

// Middleware declares a standard HTTP wrapper and its same-phase ordering.
// Names are unique in each effective route chain. Before/After reference names
// in the same phase; missing names and cycles are startup errors. Wrap runs
// during construction; a panic rejects startup and retains an error cause.
type Middleware struct {
	Name          string
	Phase         Phase
	Wrap          func(http.Handler) http.Handler
	Before, After []string
}

// Group applies middleware to explicitly declared routes. Paths remain explicit:
// grouping never rewrites paths, public policies or operation metadata.
type Group struct {
	Name       string
	Middleware []Middleware
	Routes     []Route
}

type policyKey struct{}

type targetKey struct{}
type requestTarget struct{ method, path, rawPath, routePath string }

func protectDispatch(next http.Handler) http.Handler {
	return protectTarget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, _ := r.Context().Value(targetKey{}).(requestTarget)
		routePath := ""
		if rc := chi.RouteContext(r.Context()); rc != nil {
			routePath = rc.RoutePath
		}
		if routePath != target.routePath {
			respond.WriteError(w, r, errors.New("router: middleware changed the matched routing path"))
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// Matched wrappers may enrich a request, but may not retarget a compiled policy.
// A missing target also fails closed if a wrapper discarded the request context.
func protectTarget(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, ok := r.Context().Value(targetKey{}).(requestTarget)
		if !ok || r.URL == nil || target.method != r.Method || target.path != r.URL.Path || target.rawPath != r.URL.RawPath {
			respond.WriteError(w, r, errors.New("router: middleware changed the matched request target"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func protectDeclared(next http.Handler, method, path string) http.Handler {
	return protectTarget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy, ok := r.Context().Value(policyKey{}).(RoutePolicy)
		if !ok || policy.Method != method || policy.Path != path {
			respond.WriteError(w, r, errors.New("router: dispatched route differs from the matched policy"))
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// Policy reads an independent snapshot of the matched route declaration.
// It is available in every declared phase; false means no route was matched.
func Policy(ctx context.Context) (RoutePolicy, bool) {
	if ctx == nil {
		return RoutePolicy{}, false
	}
	p, ok := ctx.Value(policyKey{}).(RoutePolicy)
	p.Operation = p.Operation.Clone()
	return p, ok
}

func cloneMiddleware(entries []Middleware) []Middleware {
	cloned := append([]Middleware(nil), entries...)
	for i := range cloned {
		cloned[i].Before = append([]string(nil), cloned[i].Before...)
		cloned[i].After = append([]string(nil), cloned[i].After...)
	}
	return cloned
}

func validMiddlewareName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c)) {
			return false
		}
	}
	return true
}

func orderMiddleware(entries []Middleware) (map[Phase][]Middleware, error) {
	if len(entries) > 64 {
		return nil, errors.New("router: too many middleware declarations")
	}
	indices := make(map[string]int, len(entries))
	for i, entry := range entries {
		if !validMiddlewareName(entry.Name) || entry.Wrap == nil || entry.Phase < Matched || entry.Phase > Response || len(entry.Before)+len(entry.After) > 64 {
			return nil, errors.New("router: invalid middleware declaration")
		}
		if _, exists := indices[entry.Name]; exists {
			return nil, fmt.Errorf("router: duplicate middleware %q", entry.Name)
		}
		indices[entry.Name] = i
	}
	edges := make([]map[int]bool, len(entries))
	indegree := make([]int, len(entries))
	add := func(from, to int) error {
		if entries[from].Phase != entries[to].Phase {
			return errors.New("router: middleware ordering must reference the same phase")
		}
		if edges[from] == nil {
			edges[from] = map[int]bool{}
		}
		if !edges[from][to] {
			edges[from][to] = true
			indegree[to]++
		}
		return nil
	}
	for i, entry := range entries {
		for _, name := range entry.Before {
			j, ok := indices[name]
			if !ok {
				return nil, fmt.Errorf("router: missing middleware %q", name)
			}
			if err := add(i, j); err != nil {
				return nil, err
			}
		}
		for _, name := range entry.After {
			j, ok := indices[name]
			if !ok {
				return nil, fmt.Errorf("router: missing middleware %q", name)
			}
			if err := add(j, i); err != nil {
				return nil, err
			}
		}
	}
	ordered := map[Phase][]Middleware{}
	done := make([]bool, len(entries))
	for range entries {
		selected := -1
		for i := range entries {
			if !done[i] && indegree[i] == 0 {
				selected = i
				break
			}
		}
		if selected < 0 {
			return nil, errors.New("router: middleware ordering cycle")
		}
		done[selected] = true
		entry := entries[selected]
		ordered[entry.Phase] = append(ordered[entry.Phase], entry)
		for next := range edges[selected] {
			indegree[next]--
		}
	}
	return ordered, nil
}

func wrapMiddleware(handler http.Handler, entries []Middleware) (wrapped http.Handler, err error) {
	var name string
	defer func() {
		if cause := recover(); cause != nil {
			wrapped = nil
			if causeErr, ok := cause.(error); ok {
				err = fmt.Errorf("router: middleware %q panicked during construction: %w", name, causeErr)
			} else {
				err = fmt.Errorf("router: middleware %q panicked during construction", name)
			}
		}
	}()
	for i := len(entries) - 1; i >= 0; i-- {
		name = entries[i].Name
		handler = entries[i].Wrap(handler)
		if isnil.Check(handler) {
			return nil, fmt.Errorf("router: middleware %q returned nil handler", entries[i].Name)
		}
	}
	return handler, nil
}
