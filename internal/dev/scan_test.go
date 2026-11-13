package dev

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanContentRenameAndExclusions(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "aaa")
	a, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	write("main.go", "bbb")
	b, _ := scan(root)
	if a == b {
		t.Fatal("same-size content change missed")
	}
	write(".frame-dev/generated.go", "ignored")
	write("config.yaml", "ignored")
	c, _ := scan(root)
	if b != c {
		t.Fatal("excluded files changed hash")
	}
	if err := os.Rename(filepath.Join(root, "main.go"), filepath.Join(root, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	d, _ := scan(root)
	if c == d {
		t.Fatal("rename missed")
	}
	if err := os.Remove(filepath.Join(root, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	e, _ := scan(root)
	if d == e {
		t.Fatal("delete missed")
	}
}

func TestScanBoundedBytes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.go"), make([]byte, maxSourceBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := scan(root); err == nil {
		t.Fatal("expected source budget error")
	}
}
