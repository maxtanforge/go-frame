// Package dotenv parses explicit development-only environment files without
// changing the process environment. It performs no expansion or shell execution.
package dotenv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	config "github.com/maxtanforge/go-frame/config"
)

// Parse accepts literal KEY=VALUE lines, blank lines and whole-line comments.
// Optional matching single/double quotes are stripped; escapes, interpolation,
// inline comments and export syntax are not interpreted. Duplicate keys fail.
func Parse(r io.Reader) (config.Source, error) {
	if r == nil {
		return config.Source{}, errors.New("config dotenv: reader is required")
	}
	b, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return config.Source{}, errors.New("config dotenv: cannot read input")
	}
	if len(b) > 1<<20 {
		return config.Source{}, errors.New("config dotenv: document exceeds 1 MiB")
	}
	values := make(map[string]string)
	s := bufio.NewScanner(strings.NewReader(string(b)))
	s.Buffer(make([]byte, 4096), (64<<10)+256)
	line := 0
	for s.Scan() {
		line++
		raw := strings.TrimSpace(s.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		fail := func(reason string) (config.Source, error) {
			return config.Source{}, fmt.Errorf("config dotenv: line %d: %s", line, reason)
		}
		if !ok || !validKey(key) {
			return fail("expected KEY=VALUE")
		}
		if _, exists := values[key]; exists {
			return fail("duplicate key")
		}
		if strings.ContainsRune(value, 0) {
			return fail("NUL is not supported")
		}
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
			if len(value) < 2 || value[len(value)-1] != value[0] {
				return fail("unmatched value quotes")
			}
			value = value[1 : len(value)-1]
		}
		if len(value) > 64<<10 {
			return fail("value exceeds 64 KiB")
		}
		values[key] = value
		if len(values) > 4096 {
			return fail("too many keys")
		}
	}
	if s.Err() != nil {
		return config.Source{}, errors.New("config dotenv: line exceeds size limit")
	}
	return config.Source{Name: "dotenv", Lookup: func(key string) (string, bool) { v, ok := values[key]; return v, ok }}, nil
}

func validKey(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
