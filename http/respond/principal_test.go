package respond_test

import (
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/respond"
)

func TestMissingPrincipalIsUnauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	respond.WriteError(w, httptest.NewRequest("GET", "/", nil), principal.ErrUnauthenticated)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
