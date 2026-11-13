// Package greeting owns business rules without HTTP or infrastructure dependencies.
package greeting

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrName = errors.New("name must contain 1–80 characters")

type Greeting struct {
	Message string
}
type Service struct{ Prefix string }

func (s Service) Greet(ctx context.Context, name string) (Greeting, error) {
	if err := ctx.Err(); err != nil {
		return Greeting{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return Greeting{}, ErrName
	}
	return Greeting{Message: s.Prefix + ", " + name}, nil
}
