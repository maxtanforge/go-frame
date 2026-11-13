package transactionexample

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/http/router"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

func categoryParams(r *http.Request) map[string]string {
	return map[string]string{"category": chi.URLParam(r, "category")}
}

// Routes declares policy beside each typed adapter and reports construction errors once.
func Routes(service Service, list func(context.Context, ListQuery) ([]Product, error)) ([]router.Route, error) {
	return router.Collect("products",
		router.Route{Method: "POST", Path: "/products", Operation: operationhttp.Metadata{Action: "product.create"}}.Bind(CreateHandler(service)),
		router.Route{Method: "GET", Path: "/categories/{category}/products", Operation: operationhttp.Metadata{Action: "product.list"}}.Bind(ListHandler(list, categoryParams)),
	)
}
