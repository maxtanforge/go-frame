// Command verify-test supplies the shared test stage of the verification scripts.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type goPackage struct {
	Dir, ImportPath                    string
	Imports, TestImports, XTestImports []string
	Incomplete                         bool
}

func main() {
	changed := flag.Bool("changed", false, "smoke test packages affected by changes relative to HEAD")
	coverage := flag.Bool("coverage", false, "write coverage.out and print total statement coverage")
	timing := flag.Bool("timing", false, "print the five slowest tests and packages")
	race := flag.Bool("race", false, "enable the race detector")
	flag.Parse()
	if flag.NArg() != 0 || (*changed && *coverage) {
		fmt.Fprintln(os.Stderr, "unexpected arguments or incompatible --changed and --coverage")
		os.Exit(2)
	}
	packages := []string{"./..."}
	if *changed {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		var err error
		packages, err = changedScope(ctx, *race)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Changed smoke scope uncertain (%v); running full root tests.\n", err)
			packages = []string{"./..."}
		}
		if len(packages) == 0 {
			fmt.Println("No changed files relative to HEAD; no scoped tests. Full root vet/build still run.")
			return
		}
		fmt.Printf("Changed smoke test scope: %s\n", strings.Join(packages, " "))
	}
	args := []string{"test", "-count=1", "-timeout=5m"}
	if *race {
		args = append(args, "-race")
	}
	if *coverage {
		args = append(args, "-coverprofile=coverage.out")
	}
	if *timing {
		args = append(args, "-json")
	}
	if err := runTests(args, packages, *timing); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *coverage {
		out, err := exec.Command("go", "tool", "cover", "-func=coverage.out").CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "coverage summary: %v\n%s", err, out)
			os.Exit(1)
		}
		for line := range strings.SplitSeq(string(out), "\n") {
			if strings.HasPrefix(line, "total:") {
				fmt.Println(line)
			}
		}
		fmt.Println("Coverage profile: coverage.out")
	}
}

func changedFiles(ctx context.Context, repo string) ([]string, error) {
	var paths []string
	// Disabling rename detection includes both old and new paths. Diffing HEAD
	// includes staged and unstaged changes; ls-files adds untracked sources.
	for _, args := range [][]string{
		{"diff", "--name-only", "-z", "--no-renames", "HEAD", "--"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w", args[0], err)
		}
		for _, path := range strings.Split(string(out), "\x00") {
			if path != "" {
				paths = append(paths, path)
			}
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

func changedScope(ctx context.Context, race bool) ([]string, error) {
	repo, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	paths, err := changedFiles(ctx, repo)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	for _, path := range paths {
		if filepath.Ext(path) != ".go" {
			return nil, fmt.Errorf("non-Go change %q", path)
		}
	}
	listArgs := []string{"list", "-json"}
	// -race changes build constraints and therefore production/test imports.
	// Inspect the same graph that the subsequent go test invocation will use.
	if race {
		listArgs = append(listArgs, "-race")
	}
	listArgs = append(listArgs, "./...")
	out, err := exec.CommandContext(ctx, "go", listArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	var packages []goPackage
	for {
		var pkg goPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list JSON: %w", err)
		}
		if pkg.Incomplete {
			return nil, fmt.Errorf("incomplete package %q", pkg.ImportPath)
		}
		packages = append(packages, pkg)
	}
	return selectPackages(repo, paths, packages)
}

func selectPackages(repo string, paths []string, packages []goPackage) ([]string, error) {
	byDir := make(map[string]string)
	reverse := make(map[string][]string)
	for _, pkg := range packages {
		byDir[filepath.Clean(pkg.Dir)] = pkg.ImportPath
		for _, imports := range [][]string{pkg.Imports, pkg.TestImports, pkg.XTestImports} {
			for _, dependency := range imports {
				reverse[dependency] = append(reverse[dependency], pkg.ImportPath)
			}
		}
	}
	selected := make(map[string]bool)
	var queue []string
	for _, path := range paths {
		if filepath.Ext(path) != ".go" {
			return nil, fmt.Errorf("non-Go change %q", path)
		}
		pkg := byDir[filepath.Dir(filepath.Join(repo, filepath.FromSlash(path)))]
		if pkg == "" {
			return nil, fmt.Errorf("cannot resolve changed package for %q", path)
		}
		queue = append(queue, pkg)
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if !selected[pkg] {
			selected[pkg] = true
			queue = append(queue, reverse[pkg]...)
		}
	}
	var result []string
	for pkg := range selected {
		result = append(result, pkg)
	}
	slices.Sort(result)
	return result, nil
}

func runTests(args, packages []string, timing bool) error {
	cmd := exec.Command("go", append(args, packages...)...)
	cmd.Stderr = os.Stderr
	if !timing {
		cmd.Stdout = os.Stdout
		return cmd.Run()
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	reportErr := reportTiming(out, os.Stdout)
	// Drain after a decoding/output error so a blocked writer cannot hide the
	// test process result. Always wait and preserve a failed test exit status.
	_, drainErr := io.Copy(io.Discard, out)
	return errors.Join(reportErr, drainErr, cmd.Wait())
}

func reportTiming(reader io.Reader, writer io.Writer) error {
	type duration struct {
		name    string
		seconds float64
	}
	var tests, packages []duration
	decoder := json.NewDecoder(reader)
	for {
		var event struct {
			Action, Package, Test, Output string
			Elapsed                       float64
		}
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("test timing JSON: %w", err)
		}
		if _, err := io.WriteString(writer, event.Output); err != nil {
			return err
		}
		if event.Action != "pass" && event.Action != "fail" && event.Action != "skip" {
			continue
		}
		if event.Test == "" {
			packages = append(packages, duration{event.Package, event.Elapsed})
		} else {
			tests = append(tests, duration{event.Package + "/" + event.Test, event.Elapsed})
		}
	}
	for _, group := range []struct {
		label string
		items []duration
	}{{"tests (including subtests)", tests}, {"packages", packages}} {
		slices.SortStableFunc(group.items, func(a, b duration) int {
			if a.seconds > b.seconds {
				return -1
			}
			if a.seconds < b.seconds {
				return 1
			}
			return strings.Compare(a.name, b.name)
		})
		if _, err := fmt.Fprintf(writer, "Top 5 slow %s:\n", group.label); err != nil {
			return err
		}
		for _, item := range group.items[:min(5, len(group.items))] {
			if _, err := fmt.Fprintf(writer, "  %.3fs %s\n", item.seconds, item.name); err != nil {
				return err
			}
		}
	}
	return nil
}
