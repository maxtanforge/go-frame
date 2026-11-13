package router_test

import (
	"context"
	"net/http"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/stretchr/testify/require"
)

func TestStartupRejectsEquivalentRoutePatterns(t *testing.T) {
	for _, paths := range [][2]string{
		{"/items/{id}", "/items/{name}"},
		{"/items/{id:[0-9]+}", "/items/{name:[0-9]+}"},
		{"/items/{id}/events/{event}", "/items/{name}/events/{entry}"},
	} {
		t.Run(paths[0], func(t *testing.T) {
			h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			routes := []router.Route{
				{Method: http.MethodGet, Path: paths[0], Handler: h},
				{Method: http.MethodGet, Path: paths[1], Handler: h},
			}
			t.Run("collect", func(t *testing.T) {
				got, err := router.Collect("inventory",
					router.Route{Method: http.MethodGet, Path: paths[0]}.Bind(h, nil),
					router.Route{Method: http.MethodGet, Path: paths[1]}.Bind(h, nil),
				)
				require.Error(t, err)
				require.Nil(t, got)
				require.ErrorContains(t, err, "inventory")
				require.ErrorContains(t, err, paths[0])
			})
			t.Run("authenticated", func(t *testing.T) {
				got, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
					Routes: routes,
					Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
						return principal.Principal{}, principal.ErrUnauthenticated
					}),
				})
				require.Error(t, err)
				require.Nil(t, got)
				require.ErrorContains(t, err, paths[0])
			})
		})
	}
}

func TestStartupAllowsDistinctRoutePatterns(t *testing.T) {
	for _, endpoints := range [][2]auth.Endpoint{
		{{Method: "GET", Path: "/items/{id}"}, {Method: "POST", Path: "/items/{name}"}},
		{{Method: "GET", Path: "/items/{id}"}, {Method: "HEAD", Path: "/items/{name}"}},
		{{Method: "GET", Path: "/items/{id:[0-9]+}"}, {Method: "GET", Path: "/items/{name:[a-z]+}"}},
		{{Method: "GET", Path: "/items/{id}"}, {Method: "GET", Path: "/items/latest"}},
		{{Method: "GET", Path: "/items/*"}, {Method: "GET", Path: "/events/*"}},
	} {
		t.Run(endpoints[1].Method+endpoints[1].Path, func(t *testing.T) {
			h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			routes, err := router.Collect("inventory",
				router.Route{Method: endpoints[0].Method, Path: endpoints[0].Path}.Bind(h, nil),
				router.Route{Method: endpoints[1].Method, Path: endpoints[1].Path}.Bind(h, nil),
			)
			require.NoError(t, err)
			require.Len(t, routes, 2)
			got, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
				Router: router.Options{DisableHealthMount: true}, Routes: routes,
				Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
					return principal.Principal{}, principal.ErrUnauthenticated
				}),
			})
			require.NoError(t, err)
			require.Len(t, got.Policies(), 2)
		})
	}
}

func TestStartupRejectsEquivalentPatternsAcrossModules(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	first, err := router.Collect("inventory", router.Route{Method: "GET", Path: "/items/{id}"}.Bind(h, nil))
	require.NoError(t, err)
	second, err := router.Collect("reporting", router.Route{Method: "GET", Path: "/items/{name}"}.Bind(h, nil))
	require.NoError(t, err)
	got, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{
		Routes: append(first, second...),
		Verifier: auth.VerifyFunc(func(context.Context, string) (principal.Principal, error) {
			return principal.Principal{}, principal.ErrUnauthenticated
		}),
	})
	require.ErrorContains(t, err, "/items/{id}")
	require.Nil(t, got)
}
