package request_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/http/request"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

type brokenSchema struct {
	Name string `json:"name" query:"name" path:"name"`
}

func (brokenSchema) Validate() error { return fmt.Errorf("private: %w", validation.ErrSchema) }

func TestValidationSchemaRemainsInternal(t *testing.T) {
	for _, decode := range []func() error{
		func() error { _, err := request.DecodeQuery("name=x", brokenSchema{}); return err },
		func() error { _, err := request.DecodePath[brokenSchema](map[string]string{"name": "x"}); return err },
		func() error {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"x"}`))
			r.Header.Set("Content-Type", "application/json")
			_, err := request.DecodeJSON[brokenSchema](httptest.NewRecorder(), r)
			return err
		},
	} {
		err := decode()
		require.ErrorIs(t, err, validation.ErrSchema)
		require.NotErrorIs(t, err, validation.ErrInvalid)
		w := httptest.NewRecorder()
		respond.WriteError(w, httptest.NewRequest(http.MethodGet, "/", nil), err)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.NotContains(t, w.Body.String(), "private")
	}
}
