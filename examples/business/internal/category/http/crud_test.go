package categoryhttp

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	auth "github.com/maxtanforge/go-frame/auth/http"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
)

func TestProductionHTTPDoesNotDependOnContractExperiment(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(importPath, "/examples/contracts/") {
				t.Errorf("production %s imports experimental contract %s", path, importPath)
			}
		}
	}
}

type crudMemory struct {
	captureRepository
	calls        int
	offset, size int64
}

func (r *crudMemory) Create(_ context.Context, m category.Model, check func(string) error) (category.Model, error) {
	r.calls++
	if err := check("alice"); err != nil {
		return category.Model{}, err
	}
	r.model = m
	return m, nil
}
func (r *crudMemory) List(_ context.Context, _ category.ID, offset, size int64, check func(string) error) ([]category.Model, int64, error) {
	r.calls++
	r.offset, r.size = offset, size
	if err := check("alice"); err != nil {
		return nil, 0, err
	}
	if !r.model.ID.Valid() {
		return nil, 0, nil
	}
	return []category.Model{r.model}, 1, nil
}
func (r *crudMemory) Delete(_ context.Context, _ category.ID, check func(category.Model) error) error {
	r.calls++
	if err := check(r.model); err != nil {
		return err
	}
	r.model = category.Model{}
	return nil
}
func TestCRUDThroughAuthenticatedRouter(t *testing.T) {
	repo := &crudMemory{}
	catalog, err := category.NewCatalog(repo, func() (category.ID, error) { return category.NewID(42) })
	if err != nil {
		t.Fatal(err)
	}
	routes, err := CRUDRoutes(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 5 {
		t.Fatal(len(routes))
	}
	var events []operation.Event
	recorder, err := operationhttp.New(operation.SinkFunc(func(_ context.Context, e operation.Event) error { events = append(events, e); return nil }), operationhttp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := router.NewAuthenticated(nil, router.AuthenticatedOptions{Router: router.Options{DisableHealthMount: true}, Routes: routes, Operations: recorder, Verifier: auth.VerifyFunc(func(_ context.Context, token string) (principal.Principal, error) {
		return principal.Principal{ID: token, Issuer: "test"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	expectedPolicies := map[string]string{
		"GET /categories/{id}":            "category.detail/category",
		"PATCH /categories/{id}":          "category.patch/category",
		"DELETE /categories/{id}":         "category.delete/category",
		"POST /books/{bookId}/categories": "category.create/book",
		"GET /books/{bookId}/categories":  "category.list/book",
	}
	for _, p := range r.Policies() {
		key := p.Method + " " + p.Path
		want, declared := expectedPolicies[key]
		if p.Public || !declared || p.Operation.Action+"/"+p.Operation.ResourceType != want {
			t.Fatalf("%+v", p)
		}
		delete(expectedPolicies, key)
	}
	if len(expectedPolicies) != 0 {
		t.Fatalf("missing policies: %v", expectedPolicies)
	}
	call := func(method, path, body, actor string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if actor != "" {
			req.Header.Set("Authorization", "Bearer "+actor)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"owner"`) || strings.Contains(w.Body.String(), `"system"`) || strings.Contains(w.Body.String(), `"parent_id"`) {
			t.Fatal(w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		data, _ := out["data"].(map[string]any)
		return data
	}
	for _, request := range []struct{ method, path, body string }{
		{"POST", "/books/7/categories", `{"name":"new"}`},
		{"GET", "/books/7/categories", ""},
		{"GET", "/categories/42", ""},
		{"PATCH", "/categories/42", `{"name":"new"}`},
		{"DELETE", "/categories/42", ""},
	} {
		call(request.method, request.path, request.body, "", 401)
	}
	if repo.calls != 0 || repo.updates != 0 {
		t.Fatal("unauthenticated request reached repository")
	}
	for _, tc := range []struct{ method, path, body string }{{"POST", "/books/01/categories", `{"name":"new"}`}, {"POST", "/books/7/categories", `{"name":"new","owner":"bob"}`}, {"POST", "/books/7/categories", `{"name":"new","system":true}`}, {"GET", "/books/7/categories?current=0", ""}, {"GET", "/books/7/categories?size=101", ""}, {"GET", "/books/7/categories?current=9223372036854775807", ""}, {"DELETE", "/categories/01", ""}} {
		before := repo.calls
		call(tc.method, tc.path, tc.body, "alice", 400)
		if repo.calls != before {
			t.Fatal("invalid request reached repository")
		}
	}
	call("POST", "/books/7/categories", `{"name":"new"}`, "bob", 403)
	data := call("POST", "/books/7/categories", `{"name":"new"}`, "alice", 200)
	if data["id"] != "42" || repo.model.Owner != "alice" {
		t.Fatal(data)
	}
	e := events[len(events)-1]
	if e.ResourceType != "category" || e.ResourceID != "42" {
		t.Fatalf("%+v", e)
	}
	call("GET", "/categories/42", "", "alice", 200)
	call("PATCH", "/categories/42", `{"note":"kept"}`, "alice", 200)
	data = call("GET", "/books/7/categories", "", "alice", 200)
	if len(data) != 5 || data["current"] != float64(1) || data["size"] != float64(20) || data["total"] != float64(1) || data["pages"] != float64(1) || repo.offset != 0 || repo.size != 20 {
		t.Fatal(data)
	}
	call("GET", "/books/7/categories?current=2&size=1", "", "alice", 200)
	if repo.offset != 1 || repo.size != 1 {
		t.Fatal("paging lost")
	}
	call("GET", "/books/7/categories", "", "bob", 403)
	repo.model.System = true
	call("DELETE", "/categories/42", "", "alice", 409)
	call("DELETE", "/categories/42", "", "bob", 403)
	repo.model.System = false
	data = call("DELETE", "/categories/42", "", "alice", 200)
	if data["deleted"] != true {
		t.Fatal(data)
	}
	data = call("GET", "/books/7/categories", "", "alice", 200)
	if len(data["records"].([]any)) != 0 {
		t.Fatal(data)
	}
	if _, err := CRUDRoutes(nil); err == nil {
		t.Fatal("nil catalog accepted")
	}
}
