package consumer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseVersionConsistency(t *testing.T) {
	repo := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(repo, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(data))
	if !strings.HasPrefix(version, "v") || !concrete.MatchString(version) {
		t.Fatalf("invalid VERSION %q", version)
	}
	for _, tc := range []struct{ file, pattern string }{
		{"templates/service/go.mod", `(?m)^\s*(?:require\s+)?github\.com/maxtanforge/go-frame\s+(\S+)`},
		{"README.md", "当前版本为 `([^`]+)`"},
		{"docs/verification.md", "当前版本为 `([^`]+)`"},
	} {
		body, readErr := os.ReadFile(filepath.Join(repo, tc.file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		match := regexp.MustCompile(tc.pattern).FindStringSubmatch(string(body))
		if len(match) != 2 || match[1] != version {
			t.Errorf("%s must match VERSION %s", tc.file, version)
		}
	}
}
