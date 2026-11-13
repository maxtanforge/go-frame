// Package greeting shows a feature module with an explicitly injected service.
package greeting

import (
	"context"
	"strings"
)

// Greeter is owned by this feature, not by a global dependency container.
type Greeter interface {
	Greet(context.Context, string) (Greeting, error)
}

// Greeting is the public DTO. Its JSON behavior is standard encoding/json.
type Greeting struct {
	Message string `json:"message"`
}

// Service implements this example's business rule without HTTP operations.
type Service struct{ Prefix string }

// Greet demonstrates propagating cancellation into the business layer.
func (s Service) Greet(ctx context.Context, name string) (Greeting, error) {
	if err := ctx.Err(); err != nil {
		return Greeting{}, err
	}
	return Greeting{Message: s.Prefix + ", " + strings.TrimSpace(name)}, nil
}
