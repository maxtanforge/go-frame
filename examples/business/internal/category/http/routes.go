package categoryhttp

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/router"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

func detailOptions() binding.Options {
	return binding.Options{MapError: httpError, PathParams: func(r *http.Request) map[string]string { return map[string]string{"id": chi.URLParam(r, "id")} }}
}

func parentOptions() binding.Options {
	return binding.Options{MapError: httpError, PathParams: func(r *http.Request) map[string]string { return map[string]string{"bookId": chi.URLParam(r, "bookId")} }}
}

func detailRoute() router.Route {
	return router.Route{Method: http.MethodGet, Path: "/categories/{id}", Operation: operationhttp.Metadata{Action: "category.detail", ResourceType: "category"}}
}
func patchRoute() router.Route {
	return router.Route{Method: http.MethodPatch, Path: "/categories/{id}", Operation: operationhttp.Metadata{Action: "category.patch", ResourceType: "category"}}
}

// Routes prepares detail and PATCH declarations. Mount with router.NewAuthenticated.
func Routes(service category.Service) ([]router.Route, error) {
	if service.Repository == nil {
		return nil, errors.New("category repository required")
	}
	h := Handler{service: service}
	return router.Collect("categories", detailRoute().Bind(binding.Path(h.Detail, detailOptions())), patchRoute().Bind(binding.JSONWithPath(h.Patch, detailOptions())))
}

// CRUDRoutes prepares all five protected declarations in one atomic collection.
// Creation requires an operation recorder; listing uses Catalog's fixed ID order.
func CRUDRoutes(catalog *category.Catalog) ([]router.Route, error) {
	if catalog == nil {
		return nil, errors.New("category catalog required")
	}
	h := Handler{service: catalog.Service(), catalog: catalog}
	create, err := CreateHandler(catalog.Create)
	return router.Collect("categories",
		detailRoute().Bind(binding.Path(h.Detail, detailOptions())),
		patchRoute().Bind(binding.JSONWithPath(h.Patch, detailOptions())),
		router.Route{Method: http.MethodPost, Path: "/books/{bookId}/categories", Operation: operationhttp.Metadata{Action: "category.create", ResourceType: "book"}}.Bind(create, err),
		router.Route{Method: http.MethodGet, Path: "/books/{bookId}/categories", Operation: operationhttp.Metadata{Action: "category.list", ResourceType: "book"}}.Bind(binding.PathWithQuery(h.List, parentOptions())),
		router.Route{Method: http.MethodDelete, Path: "/categories/{id}", Operation: operationhttp.Metadata{Action: "category.delete", ResourceType: "category"}}.Bind(binding.Path(h.Delete, detailOptions())),
	)
}
