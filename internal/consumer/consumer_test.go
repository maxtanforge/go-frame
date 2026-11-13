package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/health"
	healthhttp "github.com/maxtanforge/go-frame/http/health"
)

func mustWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if e := os.WriteFile(path, data, mode); e != nil {
		t.Fatal(e)
	}
}

func TestPrepareServiceTemplate(t *testing.T) {
	source := t.TempDir()
	manifest := []byte("module example.com/service\n\ngo 1.27.1\n\nrequire github.com/maxtanforge/go-frame v0.0.0\n")
	mustWrite(t, filepath.Join(source, "go.mod"), manifest, 0600)
	code := []byte("package service\nimport _ \"example.com/service/internal/greeting\"\n")
	mustWrite(t, filepath.Join(source, "main.go"), code, 0600)
	destination := filepath.Join(t.TempDir(), "service")
	if err := prepareService(source, destination, "v0.1.4"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "go.mod"))
	if err != nil || !strings.Contains(string(got), framework+" v0.1.4") || !strings.Contains(string(got), "module example.com/service") {
		t.Fatalf("invalid service module: %s, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(destination, "main.go"))
	if err != nil || string(got) != string(code) {
		t.Fatal("service imports changed")
	}
	got, err = os.ReadFile(filepath.Join(source, "go.mod"))
	if err != nil || string(got) != string(manifest) {
		t.Fatal("source template changed")
	}
	if prepareService(source, destination, "v0.2.0") == nil {
		t.Fatal("existing service directory accepted")
	}
}
func TestOptions(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "go.mod"), []byte("module github.com/maxtanforge/go-frame\n"), 0600)
	good := Options{Repo: repo, Mode: "workspace", Budget: time.Minute}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Options){func(o *Options) { o.Mode = "bad" }, func(o *Options) { o.Budget = 0 }, func(o *Options) { o.Repo = "missing" }, func(o *Options) { o.Engine = "sqlite" }, func(o *Options) { o.Mode = "remote"; o.Version = "main"; o.Network = true }, func(o *Options) { o.Mode = "remote"; o.Version = "b1f6ba15" }} {
		o := good
		change(&o)
		if o.Validate() == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	good.Mode = "remote"
	good.Version = "b1f6ba15"
	good.Network = true
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestEnvironment(t *testing.T) {
	got := environment([]string{"PATH=ok", "FRAME_TEST_MYSQL_DSN=secret", "DATABASE_URL=secret", "GOWORK=secret", "GOFLAGS=-mod=vendor", "LOG_OUTPUT=secret", "GOPROXY=https://example.org", "GONOPROXY=secret", "GOSUMDB=secret"}, Options{}, "")
	joined := strings.Join(got, "\n")
	for _, bad := range []string{"secret", "-mod=vendor"} {
		if strings.Contains(joined, bad) {
			t.Fatal(joined)
		}
	}
	if !strings.Contains(joined, "GOPROXY=off") || !strings.Contains(joined, "GOWORK=off") {
		t.Fatal(joined)
	}
}
func TestFixture(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "fixture")
	original := []byte("package fixture\nimport \"github.com/maxtanforge/go-frame/examples/ent/ent\"\nvar literal = \"github.com/maxtanforge/go-frame/examples/ent/ent\"\n// github.com/maxtanforge/go-frame/examples/ent/ent\n")
	mustWrite(t, filepath.Join(src, "main.go"), original, 0600)
	mustWrite(t, filepath.Join(src, "schema.sql"), []byte("DDL"), 0600)
	if err := copyFixture(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "main.go"))
	if !strings.Contains(string(got), "\"example.org/consumer/ent/ent\"") || !strings.Contains(string(got), "// github.com/maxtanforge/go-frame") || !strings.Contains(string(got), `var literal = "github.com/maxtanforge/go-frame/examples/ent/ent"`) {
		t.Fatal(string(got))
	}
	ddl, _ := os.ReadFile(filepath.Join(dst, "schema.sql"))
	if string(ddl) != "DDL" {
		t.Fatal(string(ddl))
	}
	mustWrite(t, filepath.Join(src, ".env"), []byte("secret"), 0600)
	if copyFixture(src, filepath.Join(t.TempDir(), "fixture")) == nil {
		t.Fatal("accepted env file")
	}
}
func TestOwnedCleanup(t *testing.T) {
	root, err := newOwned()
	if err != nil {
		t.Fatal(err)
	}
	if err = root.remove(); err != nil {
		t.Fatal(err)
	}
	arbitrary := owned{path: t.TempDir(), token: "wrong"}
	if arbitrary.remove() == nil {
		t.Fatal("removed unowned directory")
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__consumer_child" {
		os.Exit(ChildMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}
func TestProcessHelper(_ *testing.T) {
	switch os.Getenv("CONSUMER_HELPER") {
	case "json":
		_, _ = fmt.Fprintln(os.Stdout, `{"OK":true}`)
		_, _ = fmt.Fprintln(os.Stderr, "warning")
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Hour)
		os.Exit(0)
	case "output":
		fmt.Print(strings.Repeat("secret", 300000))
		os.Exit(7)
	case "tree":
		executable, e := os.Executable()
		if e != nil {
			os.Exit(3)
		}
		command := exec.Command(executable, "-test.run=^TestProcessHelper$")
		command.Env = append(withoutEnv(os.Environ(), "CONSUMER_HELPER"), "CONSUMER_HELPER=sleep")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if command.Start() != nil {
			os.Exit(3)
		}
		_, _ = fmt.Printf("descendant=%d\n", command.Process.Pid)
		os.Exit(0)
	case "eof":
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}
func TestProcessBudgetAndBoundedOutput(t *testing.T) {
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	env := append(os.Environ(), "CONSUMER_HELPER=output")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, e := run(ctx, t.TempDir(), env, executable, "-test.run=^TestProcessHelper$")
	if e == nil || len(output) != outputLimit {
		t.Fatalf("output=%d err=%v", len(output), e)
	}
	env = append(os.Environ(), "CONSUMER_HELPER=sleep")
	short, stop := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer stop()
	started := time.Now()
	_, e = run(short, t.TempDir(), env, executable, "-test.run=^TestProcessHelper$")
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(started) > 5*time.Second {
		t.Fatalf("budget failure: %v", e)
	}
}
func TestProcessEOF(t *testing.T) {
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, _, e := start(ctx, t.TempDir(), append(os.Environ(), "CONSUMER_HELPER=eof"), executable, "-test.run=^TestProcessHelper$")
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-p.done:
		t.Fatal("EOF delivered before explicit close")
	case <-time.After(100 * time.Millisecond):
	}
	if e = p.stdin.Close(); e != nil {
		t.Fatal(e)
	}
	if e = p.wait(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestIntegrationEvidence(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   bool
	}{{"pass", true}, {"skip", false}, {"fail", false}} {
		b := []byte(`{"Action":"` + tc.action + `","Test":"TestIntegrationEnt/mysql"}` + "\n")
		if integrationPassed(b, "mysql") != tc.want {
			t.Fatal(tc)
		}
	}
	if integrationPassed([]byte(`{"Action":"pass","Test":"TestIntegrationEnt"}`), "mysql") {
		t.Fatal("parent pass is not engine evidence")
	}
}
func TestSafeReport(t *testing.T) {
	t.Setenv("FRAME_TEST_MYSQL_DSN", "supersecret")
	report, e := Run(context.Background(), Options{Repo: "supersecret", Mode: "remote", Version: "supersecret", Budget: time.Minute}, io.Discard)
	if e == nil {
		t.Fatal("invalid options accepted")
	}
	// User-supplied version can be echoed only after concrete validation.

	b, e := json.Marshal(report)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "supersecret") {
		t.Fatal(string(b))
	}
}

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHealthResponseContract(t *testing.T) {
	for _, tc := range []struct {
		path, status string
		want         bool
	}{{"/readyz", "ready", true}, {"/healthz", "ok", true}, {"/readyz", "ok", false}} {
		client := &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"status":"` + tc.status + `"}}`)), Header: make(http.Header)}, nil
		})}
		e := checkHTTP(context.Background(), client, "http://127.0.0.1:1", "GET", tc.path, "", 200, "", false)
		if (e == nil) != tc.want {
			t.Fatalf("%+v: %v", tc, e)
		}
	}
}

func TestOwnedRootOutsideRepository(t *testing.T) {
	repo := t.TempDir()
	if outsideRepository(repo, repo) || outsideRepository(filepath.Join(repo, "child"), repo) {
		t.Fatal("accepted repository directory")
	}
	if !outsideRepository(filepath.Join(filepath.Dir(repo), "other"), repo) {
		t.Fatal("rejected sibling")
	}
}

func TestPartialCleanupRetainsOwnership(t *testing.T) {
	root, e := newOwned()
	if e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(root.path, "child"), []byte("owned"), 0600)
	if e = root.removeWith(func(string) error { return errors.New("blocked") }); e == nil {
		t.Fatal("cleanup accepted failure")
	}
	marker, e := os.ReadFile(filepath.Join(root.path, ".owner"))
	if e != nil || string(marker) != root.token {
		t.Fatal("lost ownership")
	}
	if e = root.remove(); e != nil {
		t.Fatal(e)
	}
}

func TestWindowsCleanupRetriesTransientErrors(t *testing.T) {
	for _, code := range []syscall.Errno{5, 32} {
		calls := 0
		err := retryWindowsCleanup(func() error {
			calls++
			if calls < 3 {
				return &os.PathError{Op: "remove", Path: "not-public", Err: code}
			}
			return nil
		})
		if err != nil || calls != 3 {
			t.Fatalf("errno %d: calls=%d err=%v", code, calls, err)
		}
	}
	calls := 0
	err := retryWindowsCleanup(func() error { calls++; return errors.New("permanent") })
	if err == nil || calls != 1 {
		t.Fatal("permanent error retried or ignored")
	}
}
func TestFixtureManifestPins(t *testing.T) {
	source := []byte("module " + framework + "\ngo 1.27.1\nrequire entgo.io/ent v0.14.6\nreplace entgo.io/ent => ../local\n")
	if _, e := fixtureManifest(source, "v0.1.4", true); e == nil {
		t.Fatal("remote accepted replacement")
	}
	manifest, e := fixtureManifest(source, "v0.1.4", false)
	if e != nil {
		t.Fatal(e)
	}
	graph := []byte(`{"Path":"github.com/rogpeppe/go-internal","Version":"v1.14.1"}`)
	manifest, e = pinSourceGraph(manifest, graph)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"module " + fixtureModule, "entgo.io/ent v0.14.6", framework + " v0.1.4", "github.com/rogpeppe/go-internal v1.14.1"} {
		if !strings.Contains(string(manifest), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(manifest), "replace") {
		t.Fatal("source replacement retained")
	}
	if _, e = pinSourceGraph(manifest, []byte(`{"Path":"dep","Version":"v1.0.0","Replace":{"Path":"../local"}}`)); e == nil {
		t.Fatal("graph replacement accepted")
	}
	if _, e = pinSourceGraph(manifest, []byte(`{"Path":`)); e == nil {
		t.Fatal("truncated graph accepted")
	}
}

func TestReadinessEarlyExitAndBudget(t *testing.T) {
	done := make(chan struct{})
	close(done)
	called := false
	if e := awaitReady(context.Background(), done, func() error { called = true; return nil }); e == nil || called {
		t.Fatal("accepted early exit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if e := awaitReady(ctx, make(chan struct{}), func() error { return errors.New("not ready") }); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
func TestRequestDeadline(t *testing.T) {
	client := &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if e := checkHTTP(ctx, client, "http://127.0.0.1:1", "GET", "/readyz", "", 200, "", false); e == nil {
		t.Fatal("request timeout accepted")
	}
}
func TestStopFailure(t *testing.T) {
	done := make(chan struct{})
	close(done)
	failure := errors.New("stop failed")
	p := &child{done: done, clean: func() error { return failure }}
	if e := p.wait(context.Background()); !errors.Is(e, failure) {
		t.Fatalf("ignored stop failure: %v", e)
	}
}
func TestRemoteCommitEvidence(t *testing.T) {
	for _, hash := range []string{"", "b1f6ba15", strings.Repeat("x", 40), "v1.0.0"} {
		if validCommit(hash) {
			t.Fatal("accepted missing/invalid commit")
		}
	}
	if !validCommit("b1f6ba15dae236c245db0c1648ca20b37967bf35") {
		t.Fatal("rejected exact commit")
	}
}

func TestProcessTreeReaped(t *testing.T) {
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, _ := run(ctx, t.TempDir(), append(withoutEnv(os.Environ(), "CONSUMER_HELPER"), "CONSUMER_HELPER=tree"), executable, "-test.run=^TestProcessHelper$")
	var pid int
	if _, e = fmt.Sscanf(string(output), "descendant=%d", &pid); e != nil || pid <= 0 {
		t.Fatal("helper did not start descendant")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !descendantGone(pid) {
		if time.Now().After(deadline) {
			t.Fatal("owned descendant survived")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestActualHealthEnvelope(t *testing.T) {
	checker, e := health.New(health.Options{})
	if e != nil {
		t.Fatal(e)
	}
	handler := healthhttp.New(checker)
	client := &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})}
	for _, path := range []string{"/healthz", "/readyz"} {
		if e = checkHTTP(context.Background(), client, "http://127.0.0.1:1", "GET", path, "", 200, "", false); e != nil {
			t.Fatal(e)
		}
	}
}

func TestMetadataSeparatesStderr(t *testing.T) {
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, e := run(ctx, t.TempDir(), append(withoutEnv(os.Environ(), "CONSUMER_HELPER"), "CONSUMER_HELPER=json"), executable, "-test.run=^TestProcessHelper$")
	if e != nil {
		t.Fatal(e)
	}
	var value struct{ OK bool }
	if json.Unmarshal(output, &value) != nil || !value.OK {
		t.Fatal("stderr corrupted JSON")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("private writer error") }
func TestProgressFailure(t *testing.T) {
	report := Report{}
	r := runner{ctx: context.Background(), out: failedWriter{}, report: &report}
	if e := r.step("check", func() error { return nil }); e == nil {
		t.Fatal("ignored writer failure")
	}
	if len(report.Steps) != 1 || report.Steps[0].Status != "failed" || report.Steps[0].Reason != "progress_write_failed" {
		t.Fatal(report)
	}
}

func TestCaptureOverflowExplicit(t *testing.T) {
	c := &capture{}
	_, _ = c.Write([]byte(strings.Repeat("x", outputLimit)))
	if c.exceeded() {
		t.Fatal("exact bound is not overflow")
	}
	_, _ = c.Write([]byte("x"))
	if !c.exceeded() || len(c.bytes()) != outputLimit {
		t.Fatal("overflow not recorded")
	}
}
