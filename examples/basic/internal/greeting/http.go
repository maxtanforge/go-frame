package greeting

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
)

type createRequest struct {
	Name string `json:"name"`
}

func (r createRequest) Validate() error {
	if strings.TrimSpace(r.Name) == "" || utf8.RuneCountInString(r.Name) > 80 {
		return errors.New("name must contain 1–80 characters")
	}
	return nil
}

// Register wires feature routes. Consumers can call this inside a protected chi
// group to apply their authentication middleware without changing the feature.
// This minimal example uses DecodeJSON; see templates/service and
// examples/business for typed binding, request DTOs and response view objects.
func Register(r chi.Router, service Greeter) {
	r.Post("/greetings", respond.Adapter(func(w http.ResponseWriter, r *http.Request) (any, error) {
		input, err := request.DecodeJSON[createRequest](w, r)
		if err != nil {
			return nil, err
		}
		return service.Greet(r.Context(), input.Name)
	}))
}
