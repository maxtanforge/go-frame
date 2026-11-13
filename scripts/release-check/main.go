// Command release-check validates local release version declarations and,
// optionally, checks that the VERSION tag resolves to HEAD.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	tag := flag.Bool("tag", false, "also require the VERSION tag to point to HEAD")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := run(ctx, *tag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Local release consistency passed; remote consumer verification remains a separate release gate.")
}

func run(ctx context.Context, tag bool) error {
	// Reuse the version test as the single source of truth for release files.
	cmd := exec.CommandContext(ctx, "go", "test", "./internal/consumer", "-run", "^TestReleaseVersionConsistency$", "-count=1", "-timeout=3m")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("release version consistency: %w", err)
	}
	if !tag {
		return nil
	}
	data, err := os.ReadFile("VERSION")
	if err != nil {
		return err
	}
	return checkTag(ctx, ".", strings.TrimSpace(string(data)))
}

func checkTag(ctx context.Context, repo, version string) error {
	resolve := func(ref string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", ref+"^{commit}")
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	tag, err := resolve("refs/tags/" + version)
	if err != nil {
		return err
	}
	head, err := resolve("HEAD")
	if err != nil {
		return err
	}
	if tag != head {
		return fmt.Errorf("VERSION tag %s points to %s, not HEAD %s", version, tag, head)
	}
	return nil
}
