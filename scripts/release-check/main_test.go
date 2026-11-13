package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCheckTagRequiresVersionTagAtHEAD(t *testing.T) {
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
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("v1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial")
	ctx := context.Background()
	if err := checkTag(ctx, repo, "v1.2.3"); err == nil {
		t.Fatal("missing tag must fail")
	}
	git("tag", "v1.2.3")
	if err := checkTag(ctx, repo, "v1.2.3"); err != nil {
		t.Fatalf("matching lightweight tag: %v", err)
	}
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "tag", "-am", "release", "v1.2.4")
	if err := checkTag(ctx, repo, "v1.2.4"); err != nil {
		t.Fatalf("matching annotated tag: %v", err)
	}
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "next")
	if err := checkTag(ctx, repo, "v1.2.3"); err == nil {
		t.Fatal("tag pointing to an older commit must fail")
	}
}
