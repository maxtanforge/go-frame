package categoryhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/http/httperror"
)

type captureRepository struct {
	model   category.Model
	updates int
	failure error
	seen    context.Context
}

func (r *captureRepository) Find(ctx context.Context, _ category.ID) (category.Model, error) {
	r.seen = ctx
	return r.model, r.failure
}
func (r *captureRepository) Update(ctx context.Context, _ category.ID, change func(*category.Model) error) (category.Model, error) {
	r.seen = ctx
	r.updates++
	if r.failure != nil {
		return category.Model{}, r.failure
	}
	next := r.model
	if err := change(&next); err != nil {
		return category.Model{}, err
	}
	r.model = next
	return next, nil
}
func request(t *testing.T, method, path, body string, repo *captureRepository, actor string) *httptest.ResponseRecorder {
	t.Helper()
	routes, err := Routes(category.Service{Repository: repo})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	for _, route := range routes {
		if route.Public {
			t.Fatal("route must be protected")
		}
		r.Method(route.Method, route.Path, route.Handler)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if actor != "" {
		req = req.WithContext(principal.IntoContext(req.Context(), principal.Principal{ID: actor, Issuer: "test"}))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func fixture() *captureRepository {
	id, _ := category.NewID(1)
	return &captureRepository{model: category.Model{ID: id, Owner: "alice", Name: "old", Note: "kept", HasNote: true}}
}
func TestPatchMapping(t *testing.T) {
	for _, tc := range []struct {
		body, note string
		present    bool
	}{
		{`{"name":"new"}`, "kept", true}, {`{"note":null}`, "", false}, {`{"note":""}`, "", true}, {`{"note":"new"}`, "new", true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			repo := fixture()
			w := request(t, "PATCH", "/categories/1", tc.body, repo, "alice")
			if w.Code != 200 || repo.model.Note != tc.note || repo.model.HasNote != tc.present {
				t.Fatalf("%d %s, %+v", w.Code, w.Body.String(), repo.model)
			}
			if strings.Contains(w.Body.String(), "owner") || strings.Contains(w.Body.String(), "system") || strings.Contains(w.Body.String(), "parent") {
				t.Fatal("internal fields exposed")
			}
		})
	}
}
func TestTransportWhitelistAndCanonicalID(t *testing.T) {
	for _, tc := range []struct{ path, body string }{{"01", `{"note":"x"}`}, {"+1", `{"note":"x"}`}, {"0", `{"note":"x"}`}, {"9223372036854775808", `{"note":"x"}`}, {"1", `{"name":null}`}, {"1", `{"owner":"bob"}`}, {"1", `{"id":"2"}`}, {"1", `{"parent_id":"2"}`}} {
		repo := fixture()
		w := request(t, "PATCH", "/categories/"+tc.path, tc.body, repo, "alice")
		if w.Code != 400 || repo.updates != 0 {
			t.Fatalf("%s %s: %d %s", tc.path, tc.body, w.Code, w.Body.String())
		}
	}
}
func TestDetailAndPermission(t *testing.T) {
	repo := fixture()
	w := request(t, "GET", "/categories/1", "", repo, "alice")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"1"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		actor  string
		status int
	}{{"", 401}, {"bob", 403}} {
		w = request(t, "PATCH", "/categories/1", `{"note":null}`, repo, tc.actor)
		if w.Code != tc.status {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}
func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		cause        error
		status, code int
		message      string
	}{
		{category.ErrInvalid, 400, 40001, "invalid category input"}, {category.ErrUnauthenticated, 401, 40101, "authentication required"}, {category.ErrForbidden, 403, 40301, "category access denied"}, {category.ErrImmutable, 409, 40901, "category cannot be modified"}, {category.ErrNotFound, 404, 40401, "category not found"}, {errors.New("private database password"), 500, 50001, "internal server error"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			cause := fmt.Errorf("wrapped: %w", tc.cause)
			mapped := httpError(cause)
			var out *httperror.Error
			if !errors.As(mapped, &out) || out.Status != tc.status || out.Code != tc.code || out.Message != tc.message || !errors.Is(mapped, tc.cause) {
				t.Fatalf("%+v", out)
			}
			repo := fixture()
			repo.failure = cause
			w := request(t, "GET", "/categories/1", "", repo, "alice")
			if w.Code != tc.status || strings.Contains(w.Body.String(), "wrapped:") || strings.Contains(w.Body.String(), "password") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	if mapped := httpError(context.Canceled); !errors.Is(mapped, context.Canceled) {
		t.Fatal("cancellation transformed")
	}
}
