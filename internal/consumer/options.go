// Package consumer verifies independent consumers without changing the repository.
package consumer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const framework = "github.com/maxtanforge/go-frame"
const fixtureModule = "example.org/consumer/ent"

type Options struct {
	Repo, Mode, Version, Engine string
	Network, Process, Race      bool
	Budget                      time.Duration
}

var concrete = regexp.MustCompile(`^(?:[a-fA-F0-9]{7,40}|v[01]\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

func (o Options) Validate() error {
	if o.Mode != "workspace" && o.Mode != "remote" {
		return errors.New("mode must be workspace or remote")
	}
	if o.Budget <= 0 || o.Budget > 10*time.Minute {
		return errors.New("budget must be positive and at most 10m")
	}
	if o.Repo == "" {
		return errors.New("repo is required")
	}
	b, err := os.ReadFile(filepath.Join(o.Repo, "go.mod"))
	if err != nil || !strings.Contains(string(b), "module "+framework) {
		return errors.New("repo must contain the framework module")
	}
	if o.Mode == "remote" && (!o.Network || !concrete.MatchString(o.Version)) {
		return errors.New("remote requires network opt-in and a concrete version or commit")
	}
	if o.Mode == "workspace" && o.Version != "" {
		return errors.New("version is only valid in remote mode")
	}
	if o.Engine != "" && o.Engine != "mysql" && o.Engine != "postgres" {
		return errors.New("engine must be mysql or postgres")
	}
	return nil
}
func environment(base []string, o Options, cache string) []string {
	var result []string
	for _, entry := range base {
		k, _, _ := strings.Cut(entry, "=")
		key := strings.ToUpper(k)
		// Retain toolchain/auth settings but exclude runtime configuration and live tests.
		if strings.HasPrefix(key, "FRAME_TEST_") || strings.HasPrefix(key, "FRAME_BUSINESS_TEST_") || strings.HasPrefix(key, "BUSINESS_AUTH_") || strings.HasPrefix(key, "ENT_AUTH_") || strings.HasPrefix(key, "DATABASE_") || strings.HasPrefix(key, "REDIS_") || strings.HasPrefix(key, "LOG_") || strings.HasPrefix(key, "HTTP_") || strings.HasPrefix(key, "APP_") || strings.HasPrefix(key, "OTEL_") || key == "LISTEN_HOST" || key == "GREETING_PREFIX" || key == "GOWORK" || key == "GOFLAGS" || key == "GOTOOLCHAIN" || key == "GIT_TERMINAL_PROMPT" || key == "GCM_INTERACTIVE" || key == "GIT_ASKPASS" || key == "SSH_ASKPASS" || (!o.Network && (key == "GOPROXY" || key == "GONOPROXY" || key == "GOSUMDB")) || (cache != "" && key == "GOMODCACHE") {
			continue
		}
		result = append(result, entry)
	}
	result = append(result, "GOWORK=off", "GOFLAGS=-p=1", "GOTOOLCHAIN=local", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS=")
	if !o.Network {
		result = append(result, "GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off")
	}
	if cache != "" {
		result = append(result, "GOMODCACHE="+cache)
	}
	return result
}

type owned struct{ path, token string }

func newOwned() (owned, error) {
	p, e := os.MkdirTemp("", "frame-consumer-")
	if e != nil {
		return owned{}, e
	}
	b := make([]byte, 16)
	if _, e = rand.Read(b); e != nil {
		_ = os.RemoveAll(p)
		return owned{}, e
	}
	o := owned{p, hex.EncodeToString(b)}
	e = os.WriteFile(filepath.Join(p, ".owner"), []byte(o.token), 0600)
	if e != nil {
		_ = os.RemoveAll(p)
		return owned{}, e
	}
	return o, nil
}
func (o owned) remove() error { return o.removeWith(os.RemoveAll) }
func (o owned) removeWith(removeTree func(string) error) error {
	absolute, e := filepath.Abs(o.path)
	temp, te := filepath.Abs(os.TempDir())
	if te != nil {
		return errors.New("invalid temporary parent")
	}
	if e != nil || filepath.Dir(absolute) != filepath.Clean(temp) || absolute != filepath.Clean(o.path) || !strings.HasPrefix(filepath.Base(absolute), "frame-consumer-") {
		return errors.New("invalid owned root")
	}
	info, e := os.Lstat(absolute)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid owned root")
	}
	b, e := os.ReadFile(filepath.Join(absolute, ".owner"))
	if e != nil || o.token == "" || string(b) != o.token {
		return errors.New("ownership check failed")
	}
	// Downloaded Go modules are read-only. Make only files in this verified root removable.
	if e = filepath.WalkDir(absolute, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink == 0 {
			return os.Chmod(p, 0700)
		}
		return nil
	}); e != nil {
		return e
	}
	entries, e := os.ReadDir(absolute)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() == ".owner" {
			continue
		}
		target := filepath.Join(absolute, entry.Name())
		var err error
		if runtime.GOOS == "windows" {
			err = retryWindowsCleanup(func() error { return removeTree(target) })
		} else {
			err = removeTree(target)
		}
		if err != nil {
			return err
		}
	}
	if e = os.Remove(filepath.Join(absolute, ".owner")); e != nil {
		return e
	}
	if e = os.Remove(absolute); e != nil {
		current, statErr := os.Lstat(absolute)
		if statErr == nil && os.SameFile(info, current) {
			return errors.Join(e, os.WriteFile(filepath.Join(absolute, ".owner"), []byte(o.token), 0600))
		}
		return e
	}
	return nil
}

// Like the Go tool's robust removal, retry only known transient Windows errors.
// A permanent failure still retains the owned root and fails acceptance.
func retryWindowsCleanup(remove func() error) error {
	var deadline time.Time
	delay := 25 * time.Millisecond
	for {
		err := remove()
		var code syscall.Errno
		if err == nil || !errors.As(err, &code) || (code != 5 && code != 32) {
			return err
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(2 * time.Second)
		}
		if time.Now().Add(delay).After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(delay*2, 200*time.Millisecond)
	}
}
func copyFixture(src, dst string) error {
	return copyFixtureModule(src, dst, framework+"/examples/ent", fixtureModule)
}

func copyBusinessFixture(src, dst string) error {
	return copyFixtureModule(src, dst, framework+"/examples/business", "example.org/consumer/business")
}

func copyFixtureModule(src, dst, old, module string) error {
	if _, e := os.Lstat(dst); !os.IsNotExist(e) {
		return errors.New("fixture destination exists")
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("fixture symlink rejected")
		}
		name := strings.ToLower(d.Name())
		if name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".env") {
			return errors.New("fixture environment file rejected")
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("fixture special file rejected")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if strings.HasSuffix(name, ".go") {
			fset := token.NewFileSet()
			parsed, e := parser.ParseFile(fset, p, b, 0)
			if e != nil {
				return errors.New("invalid fixture Go source")
			}
			for i := len(parsed.Imports) - 1; i >= 0; i-- {
				imp := parsed.Imports[i]
				value, e := strconv.Unquote(imp.Path.Value)
				if e != nil {
					return errors.New("invalid import")
				}
				if value == old || strings.HasPrefix(value, old+"/") {
					replacement := strconv.Quote(module + strings.TrimPrefix(value, old))
					start, end := fset.Position(imp.Path.Pos()).Offset, fset.Position(imp.Path.End()).Offset
					b = append(append(append([]byte(nil), b[:start]...), replacement...), b[end:]...)
				}
			}
		}
		return os.WriteFile(target, b, 0600)
	})
}

func outsideRepository(path, repo string) bool {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return false
	}
	repository, e := filepath.Abs(repo)
	if e != nil {
		return false
	}
	relative, e := filepath.Rel(repository, absolute)
	if e != nil {
		return filepath.VolumeName(repository) != filepath.VolumeName(absolute)
	}
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validCommit(hash string) bool { return len(hash) == 40 && concrete.MatchString(hash) }
