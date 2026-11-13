package binding

import (
	"context"
	"errors"
)

// Handler is a typed business callback invoked after decoding and validation.
type Handler[Req, Res any] func(context.Context, *Req) (*Res, error)

// Interceptor wraps typed input/result handling without reading HTTP bodies.
// Interceptors run in declaration order and return in reverse order. They must
// be concurrency-safe and use the received context for business work.
type Interceptor[Req, Res any] func(Handler[Req, Res]) Handler[Req, Res]

// Intercept builds a typed callback once at registration. Pass its result to
// JSON/Query/Path/Form; decoder failures never invoke these interceptors.
func Intercept[Req, Res any](handler Handler[Req, Res], entries ...Interceptor[Req, Res]) (Handler[Req, Res], error) {
	if handler == nil || len(entries) > 64 {
		return nil, errors.New("binding: invalid interceptor declaration")
	}
	for _, entry := range entries {
		if entry == nil {
			return nil, errors.New("binding: nil interceptor")
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		handler = entries[i](handler)
		if handler == nil {
			return nil, errors.New("binding: interceptor returned nil handler")
		}
	}
	return handler, nil
}

// PairHandler receives independent path and body/query DTOs after validation.
type PairHandler[Path, Input, Res any] func(context.Context, *Path, *Input) (*Res, error)

// PairInterceptor wraps a two-source typed callback.
type PairInterceptor[Path, Input, Res any] func(PairHandler[Path, Input, Res]) PairHandler[Path, Input, Res]

// InterceptPair prepares a callback for JSONWithPath or PathWithQuery.
func InterceptPair[Path, Input, Res any](handler PairHandler[Path, Input, Res], entries ...PairInterceptor[Path, Input, Res]) (PairHandler[Path, Input, Res], error) {
	if handler == nil || len(entries) > 64 {
		return nil, errors.New("binding: invalid interceptor declaration")
	}
	for _, entry := range entries {
		if entry == nil {
			return nil, errors.New("binding: nil interceptor")
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		handler = entries[i](handler)
		if handler == nil {
			return nil, errors.New("binding: interceptor returned nil handler")
		}
	}
	return handler, nil
}
