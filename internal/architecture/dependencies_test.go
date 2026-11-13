package architecture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const module = "github.com/maxtanforge/go-frame/"

func TestConfigDecoderHasNoDeploymentDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "list", "-deps", module+"config").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect config: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "time/tzdata" || strings.HasPrefix(dep, module+"identity") || strings.HasPrefix(dep, module+"app") || strings.HasPrefix(dep, module+"telemetry/") {
			t.Errorf("generic configuration decoder imports deployment dependency %s", dep)
		}
	}
}

// Inspect production transitive dependencies. Test helpers and optional adapters
// must not accidentally force infrastructure into otherwise independent cores.
func TestCoreDependencies(t *testing.T) {
	// The whole-module graph competes with parallel test builds on cold caches.
	// Keep a bounded budget below verify's five-minute package test timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-json", "./...")
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("inspect dependencies: %v\n%s", err, &stderr)
	}
	type pkg struct {
		ImportPath string
		Standard   bool
		Deps       []string
	}
	packages := map[string]pkg{}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := decoder.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		packages[p.ImportPath] = p
	}
	// These independently usable adapters must be inspected even when none of
	// the selected core packages happens to import them.
	for _, name := range []string{"auth/token", "telemetry/otel", "app/startup", "app/config", "redis", "id", "optional"} {
		if _, ok := packages[module+name]; !ok {
			t.Errorf("public capability omitted from dependency inspection: %s", name)
		}
	}
	// Inspect every discovered public package, including future top-level packages.
	for name, p := range packages {
		if !strings.HasPrefix(name, module) || excludedPackage(strings.TrimPrefix(name, module)) {
			continue
		}
		for _, dep := range p.Deps {
			if dep == strings.TrimSuffix(module, "/") || strings.HasPrefix(dep, module+"examples/") || strings.HasPrefix(dep, module+"cmd/") || strings.HasPrefix(dep, module+"scripts/") || strings.HasPrefix(dep, module+"internal/dev") || strings.HasPrefix(dep, module+"internal/consumer") {
				t.Errorf("public package %s imports assembly or tooling dependency %s", name, dep)
			}
		}
	}
	for _, name := range []string{"examples/business/internal/category", "examples/business/internal/profile", "examples/business/internal/devicesync"} {
		p, ok := packages[module+name]
		if !ok {
			t.Fatalf("business boundary omitted: %s", name)
		}
		for _, dep := range p.Deps {
			if !packages[dep].Standard {
				t.Errorf("business use case %s imports infrastructure %s", name, dep)
			}
		}
	}
	// GF is a design reference only, never a runtime or transitive dependency.
	for name := range packages {
		if strings.HasPrefix(name, "github.com/gogf/gf") || strings.HasPrefix(name, "gorm.io/") {
			t.Errorf("unexpected framework/ORM dependency %s", name)
		}
	}
	for _, name := range []string{"cache", "health", "auth/principal", "telemetry/operation", "telemetry/metric", "telemetry/metric/otel", "validation", "optional", "http/request", "database/audit", "database", "database/query", "pagination", "http/server", "config", "config/reload", "app/command", "app", "concurrency"} {
		t.Run(name, func(t *testing.T) {
			p, ok := packages[strings.TrimSuffix(module+name, "/")]
			if !ok {
				t.Fatal("package missing from dependency graph")
			}
			for _, dep := range p.Deps {
				if packages[dep].Standard {
					continue
				}
				// Cores may share the dependency-free typed-nil guard, and the
				// two public sorters may share the sort-whitelist algorithm
				// without importing an adapter.
				if dep == module+"internal/isnil" || dep == module+"internal/sortspec" {
					continue
				}
				if name == "http/request" && (dep == module+"id" || dep == module+"validation" || dep == module+"http/httperror") {
					continue
				}
				// Reload consumes generic configuration diagnostics only.
				if name == "config/reload" && dep == module+"config" {
					continue
				}
				if name == "database/query" && dep == module+"pagination" {
					continue
				}
				// SQL and Ent share only the stdlib-only transaction cleanup policy.
				if name == "database" && dep == module+"database/internal/txcleanup" {
					continue
				}
				// The optional otel export binding reuses the existing otel
				// dependency family; the metric contract package stays stdlib-only.
				// OTel attribute hashing pulls in xxhash transitively.
				if name == "telemetry/metric/otel" && (dep == module+"telemetry/metric" || strings.HasPrefix(dep, "go.opentelemetry.io/") || dep == "github.com/cespare/xxhash/v2") {
					continue
				}
				t.Errorf("unexpected production dependency %s", strings.TrimPrefix(dep, module))
			}
		})
	}
	for name, forbidden := range map[string][]string{
		// The module root documents the library; capability packages own APIs.
		"":                         {module + "http/", module + "app", module + "app/resource", module + "auth/http", module + "database/mysql", module + "database/postgres", module + "database/ent", "entgo.io/", "github.com/go-sql-driver/", "github.com/jackc/", "go.opentelemetry.io/"},
		"app":                      {module + "app/config", module + "app/startup", module + "app/resource", module + "database", module + "redis", module + "http/", module + "telemetry/", "github.com/", "go.opentelemetry.io/"},
		"app/config":               {module + "app/startup", module + "database", module + "redis", module + "http/", module + "telemetry/", "github.com/", "go.opentelemetry.io/"},
		"redis":                    {module + "database", module + "app", module + "http/", "database/sql", "entgo.io/", "github.com/go-sql-driver/", "github.com/jackc/"},
		"schedule":                 {module + "database", module + "http/", module + "lock/", module + "auth/", "github.com/redis/", "entgo.io/"},
		"lock/redis":               {module + "database", module + "http/", module + "schedule", module + "auth/", "entgo.io/"},
		"http/binding":             {module + "database", "entgo.io/", "github.com/go-chi/", "go.opentelemetry.io/"},
		"database/ent":             {module + "http/", module + "auth/http", module + "auth/token", "go.opentelemetry.io/", "github.com/go-sql-driver/", "github.com/jackc/", "entgo.io/ent/entc/"},
		"validation/tags":          {module + "database", module + "http/", "entgo.io/", "go.opentelemetry.io/"},
		"cache/memory":             {module + "database/", "github.com/redis/", module + "http/"},
		"health":                   {"net/http", module + "http/", module + "telemetry/"},
		"auth/http":                {module + "auth/token", "github.com/go-jose/", module + "database", module + "redis"},
		"database/audit/principal": {module + "auth/http", module + "auth/token", module + "http/respond", "github.com/"},
		"database/mysql":           {"github.com/jackc/", module + "database/postgres"},
		"database/postgres":        {"github.com/go-sql-driver/mysql", module + "database/mysql"},
		"app/config/redis":         {"github.com/go-sql-driver/", "github.com/jackc/", "go.opentelemetry.io/"},
		"app/resource":             {module + "health", module + "database", module + "redis", "github.com/", "go.opentelemetry.io/"},
		"telemetry/logger":         {"go.opentelemetry.io/", module + "telemetry/otel"},
		"http/client":              {"go.opentelemetry.io/", module + "telemetry/otel"},
	} {
		t.Run(name, func(t *testing.T) {
			p, ok := packages[strings.TrimSuffix(module+name, "/")]
			if !ok {
				t.Fatal("package missing from dependency graph")
			}
			for _, dep := range p.Deps {
				for _, prefix := range forbidden {
					// The GitHub namespace also contains this module itself.
					if prefix == "github.com/" && (dep == strings.TrimSuffix(module, "/") || strings.HasPrefix(dep, module)) {
						continue
					}
					if strings.HasPrefix(dep, prefix) {
						t.Errorf("forbidden dependency %s", dep)
					}
				}
			}
		})
	}
}

func excludedPackage(name string) bool {
	for _, prefix := range []string{"internal/", "examples/", "cmd/", "scripts/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Framework test files must not import examples: example code is a consumer of
// the library, and test-time imports invert the documented examples -> library
// verification direction. go list -deps cannot see test imports, so the check
// parses _test.go files directly.
func TestFrameworkTestsDoNotImportExamples(t *testing.T) {
	skipped := map[string]bool{".git": true, ".claude": true, "docs": true, "examples": true, "scripts": true, "templates": true}
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == "../.." {
				return nil
			}
			if skipped[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), module+"examples/") {
				t.Errorf("%s imports %s; framework tests must not depend on example code", filepath.ToSlash(path), imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
