package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectPackagesIncludesTransitiveAndTestDependents(t *testing.T) {
	root := t.TempDir()
	packages := []goPackage{
		{Dir: filepath.Join(root, "base"), ImportPath: "example/base"},
		{Dir: filepath.Join(root, "service"), ImportPath: "example/service", Imports: []string{"example/base"}},
		{Dir: filepath.Join(root, "tests"), ImportPath: "example/tests", TestImports: []string{"example/service"}},
		{Dir: filepath.Join(root, "external"), ImportPath: "example/external", XTestImports: []string{"example/base"}},
		{Dir: filepath.Join(root, "other"), ImportPath: "example/other"},
	}
	got, err := selectPackages(root, []string{"base/deleted.go", "base/new.go"}, packages)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example/base", "example/external", "example/service", "example/tests"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	for _, paths := range [][]string{{"README.md"}, {"gone/deleted.go"}, {"templates/service/main.go"}} {
		if _, err := selectPackages(root, paths, packages); err == nil {
			t.Errorf("%v should require full verification", paths)
		}
	}
	got, err = selectPackages(root, nil, packages)
	if err != nil || len(got) != 0 {
		t.Fatalf("unchanged scope = %v, %v", got, err)
	}
}

func TestChangedFilesIncludesBothRenameSidesAndAllWorktreeStates(t *testing.T) {
	repo := t.TempDir()
	gitConfig := []string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "core.hooksPath=" + t.TempDir()}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append(gitConfig, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	for _, name := range []string{"renamed.go", "staged.go", "unstaged.go", "deleted.go"} {
		write(name, "package example\n")
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial")
	if paths, err := changedFiles(context.Background(), repo); err != nil || len(paths) != 0 {
		t.Fatalf("clean repository scope = %v, %v", paths, err)
	}
	git("mv", "renamed.go", "renamed with space.go")
	write("staged.go", "package changed\n")
	git("add", "staged.go")
	write("unstaged.go", "package changed\n")
	write("untracked.go", "package example\n")
	if err := os.Remove(filepath.Join(repo, "deleted.go")); err != nil {
		t.Fatal(err)
	}
	got, err := changedFiles(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deleted.go", "renamed with space.go", "renamed.go", "staged.go", "unstaged.go", "untracked.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
}

func TestTimingReportsSlowestFiveTestsAndPackages(t *testing.T) {
	var input strings.Builder
	input.WriteString("{\"Action\":\"output\",\"Output\":\"a test log\\n\"}\n")
	for _, event := range []string{
		`{"Action":"pass","Package":"fast","Test":"TestFast","Elapsed":0.1}`,
		`{"Action":"fail","Package":"slow","Test":"TestSlow","Elapsed":4}`,
		`{"Action":"pass","Package":"slow","Elapsed":5}`,
		`{"Action":"pass","Package":"fast","Elapsed":1}`,
	} {
		input.WriteString(event + "\n")
	}
	var output bytes.Buffer
	if err := reportTiming(strings.NewReader(input.String()), &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "a test log\n") || !strings.Contains(text, "slow/TestSlow") || strings.Index(text, "slow/TestSlow") > strings.Index(text, "fast/TestFast") {
		t.Fatalf("missing output or incorrect ranking:\n%s", text)
	}
	if strings.Index(text, "5.000s slow") > strings.Index(text, "1.000s fast") {
		t.Fatalf("incorrect package ranking:\n%s", text)
	}
	if err := reportTiming(strings.NewReader("invalid JSON"), &output); err == nil {
		t.Fatal("invalid timing output must fail")
	}
}

func TestRunTestsPreservesFailureWithTiming(t *testing.T) {
	repo := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":          "module example.test/fixture\n\ngo 1.25\n",
		"failure_test.go": "package fixture\nimport \"testing\"\nfunc TestFailure(t *testing.T) { t.Fatal(\"intentional fixture failure\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)
	t.Setenv("GOWORK", "off")
	if err := runTests([]string{"test", "-json", "-count=1", "-timeout=30s"}, []string{"."}, true); err == nil {
		t.Fatal("timing must preserve a failing go test exit")
	}
}

func TestTimingLimitsEachGroupToFive(t *testing.T) {
	var input, output bytes.Buffer
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&input, "{\"Action\":\"pass\",\"Package\":\"package%d\",\"Test\":\"Test%d\",\"Elapsed\":%d}\n", i, i, i)
		fmt.Fprintf(&input, "{\"Action\":\"pass\",\"Package\":\"package%d\",\"Elapsed\":%d}\n", i, i)
	}
	if err := reportTiming(&input, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Contains(text, "package1") || strings.Contains(text, "package2") || strings.Count(text, "s package") != 10 {
		t.Fatalf("expected five tests and five packages:\n%s", text)
	}
}

func TestChangedScopeReadsNormalAndExternalTestImports(t *testing.T) {
	repo := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":                        "module example.test/scope\n\ngo 1.25\n",
		"base/base.go":                  "package base\nconst Value = 1\n",
		"unit/unit_test.go":             "package unit\nimport \"example.test/scope/base\"\nvar _ = base.Value\n",
		"external/external.go":          "package external\n",
		"external/external_test.go":     "package external_test\nimport \"example.test/scope/base\"\nvar _ = base.Value\n",
		"dependent/dependent.go":        "package dependent\nimport _ \"example.test/scope/external\"\n",
		"unrelated/unrelated.go":        "package unrelated\n",
		"raceunit/unit.go":              "package raceunit\n",
		"raceunit/unit_test.go":         "//go:build race\n\npackage raceunit\nimport \"example.test/scope/base\"\nvar _ = base.Value\n",
		"raceexternal/external.go":      "package raceexternal\n",
		"raceexternal/external_test.go": "//go:build race\n\npackage raceexternal_test\nimport \"example.test/scope/base\"\nvar _ = base.Value\n",
		"racedependent/dependent.go":    "package racedependent\nimport _ \"example.test/scope/raceexternal\"\n",
	} {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)
	t.Setenv("GOWORK", "off")
	// Only inspect source graphs: use a supported race target without requiring
	// a host C compiler or claiming to execute the race detector.
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "amd64")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOFLAGS", "")
	gitConfig := []string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "core.hooksPath=" + t.TempDir()}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial"}} {
		if out, err := exec.Command("git", append(gitConfig, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "base", "base.go"), []byte("package base\nconst Value = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, race := range []bool{false, true} {
		t.Run(fmt.Sprintf("race=%t", race), func(t *testing.T) {
			got, err := changedScope(context.Background(), race)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"example.test/scope/base", "example.test/scope/dependent", "example.test/scope/external"}
			if race {
				want = append(want, "example.test/scope/racedependent", "example.test/scope/raceexternal", "example.test/scope/raceunit")
			}
			want = append(want, "example.test/scope/unit")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("scope from go list = %v, want %v", got, want)
			}
		})
	}
}
