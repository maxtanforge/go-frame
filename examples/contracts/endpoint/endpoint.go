// Package endpoint compares typed registration helpers in examples only.
// It is not a framework API; decoding and startup validation remain in binding/router.
package endpoint

import (
	"context"

	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/router"
)

// Path binds a typed path callback without replacing the route's policy.
func Path[P, R any](route router.Route, handler func(context.Context, *P) (*R, error), options ...binding.Options) router.Declaration {
	return route.Bind(binding.Path(handler, options...))
}

// PathWithQuery keeps path and query inputs separate.
func PathWithQuery[P, Q, R any](route router.Route, handler func(context.Context, *P, *Q) (*R, error), options ...binding.Options) router.Declaration {
	return route.Bind(binding.PathWithQuery(handler, options...))
}

// JSONWithPath keeps path and JSON presence semantics separate.
func JSONWithPath[P, B, R any](route router.Route, handler func(context.Context, *P, *B) (*R, error), options ...binding.Options) router.Declaration {
	return route.Bind(binding.JSONWithPath(handler, options...))
}
