// Package greetinghttp adapts the greeting business service to HTTP.
package greetinghttp

import (
	"context"
	"net/http"

	"example.com/service/internal/greeting"
	"github.com/maxtanforge/go-frame/http/binding"
)

// Handler maps validated request DTOs to the greeting service and response VO.
type Handler struct {
	service greeting.Service
}

func (h Handler) Greet(ctx context.Context, input *Input) (*GreetingVO, error) {
	result, err := h.service.Greet(ctx, input.Name)
	if err != nil {
		return nil, err
	}
	return &GreetingVO{Message: result.Message}, nil
}

// New prepares the JSON handler once during startup.
func New(service greeting.Service) (http.Handler, error) {
	h := Handler{service: service}
	return binding.JSON(h.Greet, binding.Options{MapError: httpError})
}
