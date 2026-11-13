package consumer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyBusinessFixtureRewritesOnlyImports(t *testing.T) {
	src := t.TempDir()
	code := "package business\nimport _ \"" + framework + "/examples/business/internal/category\"\nvar literal = \"" + framework + "/examples/business/internal/category\"\n"
	mustWrite(t, filepath.Join(src, "main.go"), []byte(code), 0600)
	dst := filepath.Join(t.TempDir(), "business")
	if err := copyBusinessFixture(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "import _ \"example.org/consumer/business/internal/category\"") || !strings.Contains(string(got), "var literal = \""+framework+"/examples/business/internal/category\"") {
		t.Fatal(string(got))
	}
}
