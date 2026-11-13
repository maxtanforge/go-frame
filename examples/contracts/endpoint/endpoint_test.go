package endpoint_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/maxtanforge/go-frame/examples/contracts/endpoint"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

type path struct{ ID string }
type input struct{ Name string }
type output struct{ Name string }

func TestConstructionFailuresRemainAtomic(t *testing.T) {
	route := router.Route{Method: "GET", Path: "/items/{id}"}
	for name, declaration := range map[string]router.Declaration{
		"path":  endpoint.Path[path, output](route, nil),
		"query": endpoint.PathWithQuery[path, input, output](route, nil),
		"body":  endpoint.JSONWithPath[path, input, output](route, nil),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := router.Collect("catalog", declaration)
			require.Nil(t, got)
			require.ErrorIs(t, err, validation.ErrSchema)
			require.ErrorContains(t, err, "catalog")
			require.ErrorContains(t, err, "GET")
			require.ErrorContains(t, err, "/items/{id}")
		})
	}
}

func TestExistingHandlerRemainsRejected(t *testing.T) {
	route := router.Route{Method: "GET", Path: "/items/{id}", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	_, err := router.Collect("catalog", endpoint.Path(route, func(context.Context, *path) (*output, error) {
		return &output{}, nil
	}))
	require.ErrorContains(t, err, "handler already set")
}
