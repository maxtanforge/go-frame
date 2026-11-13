package greetinghttp

import (
	"net/http"

	"example.com/service/internal/greeting"
	"github.com/maxtanforge/go-frame/http/router"
)

// Routes collects the feature declarations before bootstrap mounts them.
func Routes(service greeting.Service) ([]router.Route, error) {
	return router.Collect("greeting", router.Route{Method: http.MethodPost, Path: "/greetings"}.Bind(New(service)))
}
