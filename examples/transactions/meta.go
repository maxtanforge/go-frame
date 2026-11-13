package transactionexample

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	"github.com/maxtanforge/go-frame/http/router"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

// MetaRoutes compares startup-only field scanning with explicit Routes.
// This prototype supports typed adapter factories, not arbitrary controller methods.
func MetaRoutes(service Service, list func(context.Context, ListQuery) ([]Product, error)) ([]router.Route, error) {
	controller := struct {
		Create func() (http.Handler, error) `method:"POST" path:"/products" action:"product.create"`
		List   func() (http.Handler, error) `method:"GET" path:"/categories/{category}/products" action:"product.list"`
	}{
		Create: func() (http.Handler, error) { return CreateHandler(service) },
		List:   func() (http.Handler, error) { return ListHandler(list, categoryParams) },
	}
	return scanRoutes("products", controller)
}

func scanRoutes(module string, controller any) ([]router.Route, error) {
	v := reflect.ValueOf(controller)
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("meta %s: controller must be a struct", module)
	}
	t := v.Type()
	declarations := make([]router.Declaration, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		method, hasMethod := field.Tag.Lookup("method")
		path, hasPath := field.Tag.Lookup("path")
		if !hasMethod && !hasPath {
			continue // Dependencies are never exposed automatically.
		}
		label := fmt.Sprintf("meta %s.%s %s %s", module, field.Name, method, path)
		if !hasMethod || !hasPath || !v.Field(i).CanInterface() {
			return nil, fmt.Errorf("%s: exported factory requires method and path", label)
		}
		public := field.Tag.Get("public")
		if public != "" && public != "false" && public != "true" {
			return nil, fmt.Errorf("%s: public must be true or false", label)
		}
		factory, ok := v.Field(i).Interface().(func() (http.Handler, error))
		if !ok || factory == nil {
			return nil, fmt.Errorf("%s: typed handler factory required", label)
		}
		declarations = append(declarations, router.Route{
			Method: method, Path: path, Public: public == "true",
			Operation: operationhttp.Metadata{Action: field.Tag.Get("action")},
		}.Bind(factory()))
	}
	return router.Collect(module, declarations...)
}
