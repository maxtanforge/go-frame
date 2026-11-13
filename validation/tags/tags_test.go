package tags_test

import (
	"testing"

	"github.com/maxtanforge/go-frame/validation"
	"github.com/maxtanforge/go-frame/validation/tags"
	"github.com/stretchr/testify/require"
)

func TestValidationRules(t *testing.T) {
	type input struct {
		Name  string `json:"name" v:"required,min=3"`
		Email string `json:"email" v:"email"`
	}
	engine := tags.New()
	var all *validation.Errors
	err := engine.Validate(input{Email: "private"})
	require.ErrorAs(t, err, &all)
	require.Len(t, all.Issues(), 3)
	require.Equal(t, validation.Path{"name"}, all.Issues()[0].Path)
	require.NotContains(t, err.Error(), "private")
	require.NoError(t, engine.Validate(input{Name: "abc", Email: "a@example.com"}))
}
func TestSchemaErrorsDoNotEscapeOrDiscloseValues(t *testing.T) {
	for _, v := range []any{nil, 123, struct {
		Name string `v:"not_a_rule"`
	}{"private"}, struct {
		Name string `v:"min=private"`
	}{"private"}, struct {
		Name string `validate:"required"`
	}{"private"}, struct {
		Items map[string]string `v:"dive"`
	}{map[string]string{"private": "secret"}}} {
		require.NotPanics(t, func() {
			err := tags.New().Validate(v)
			require.ErrorIs(t, err, validation.ErrSchema)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}
func TestAnonymousAndEmbeddedStructFieldNames(t *testing.T) {
	type Address struct {
		City string `v:"required"`
	}
	type Input struct{ Address }
	var all *validation.Errors
	require.ErrorAs(t, tags.New().Validate(Input{}), &all)
	require.Equal(t, validation.Path{"city"}, all.Issues()[0].Path)
	type Nested struct {
		Address `json:"address"`
	}
	require.ErrorAs(t, tags.New().Validate(Nested{}), &all)
	require.Equal(t, validation.Path{"address", "city"}, all.Issues()[0].Path)
}
