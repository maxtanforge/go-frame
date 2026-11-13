package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Step struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason,omitempty"`
}
type Report struct {
	RetainedTemp     string `json:"retained_temp,omitempty"`
	WorkspaceDirty   bool   `json:"workspace_dirty"`
	Mode             string `json:"mode"`
	Platform         string `json:"platform"`
	RequestedVersion string `json:"requested_version,omitempty"`
	ResolvedVersion  string `json:"resolved_version,omitempty"`
	SourceCommit     string `json:"source_commit,omitempty"`
	LocalReplace     bool   `json:"local_replace"`
	Network          bool   `json:"network"`
	Steps            []Step `json:"steps"`
	Success          bool   `json:"success"`
}
type runner struct {
	ctx    context.Context
	env    []string
	out    io.Writer
	report *Report
}

func (r *runner) step(name string, f func() error) error {
	start := time.Now()
	err := f()
	s := Step{Name: name, Status: "passed", DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		s.Status = "failed"
		s.Reason = "command_or_check_failed"
		if errors.Is(err, errDependencyNetwork) {
			s.Reason = "dependency_network_failed"
		}
		if errors.Is(err, errOutputLimit) {
			s.Reason = "output_limit_exceeded"
		}
		if errors.Is(err, errDependencyNotCached) {
			s.Reason = "dependency_not_cached"
		}
		if errors.Is(err, context.DeadlineExceeded) || r.ctx.Err() != nil {
			s.Reason = "budget_exceeded"
		}
	}
	if _, writeErr := fmt.Fprintf(r.out, "%s: %s (%dms)\n", s.Name, s.Status, s.DurationMS); writeErr != nil {
		s.Status = "failed"
		s.Reason = "progress_write_failed"
		err = errors.New("progress write failed")
	}
	r.report.Steps = append(r.report.Steps, s)
	return err
}
func (r *runner) skip(name, reason string) {
	r.report.Steps = append(r.report.Steps, Step{Name: name, Status: "skipped", Reason: reason})
}
func Run(parent context.Context, o Options, progress io.Writer) (report Report, err error) {
	report = Report{Platform: runtime.GOOS + "/" + runtime.GOARCH, LocalReplace: o.Mode == "workspace", Network: o.Network, Steps: []Step{}}
	if progress == nil {
		progress = io.Discard
	}
	if e := o.Validate(); e != nil {
		report.Steps = append(report.Steps, Step{Name: "options", Status: "failed", Reason: "invalid_options"})
		return report, errors.New("invalid options")
	}
	report.Mode = o.Mode
	report.RequestedVersion = o.Version
	o.Repo, err = filepath.Abs(o.Repo)
	if err != nil {
		return report, errors.New("invalid repo")
	}
	ctx, cancel := context.WithTimeout(parent, o.Budget)
	defer cancel()
	root, e := newOwned()
	if e != nil {
		return report, errors.New("temporary root failed")
	}
	defer func() {
		cleanupStart := time.Now()
		ce := root.remove()
		s := Step{Name: "cleanup", Status: "passed", DurationMS: time.Since(cleanupStart).Milliseconds()}
		if ce != nil {
			s.Status = "failed"
			s.Reason = "owned_cleanup_failed"
			report.RetainedTemp = root.path
			err = errors.New("cleanup failed")
			report.Success = false
		}
		report.Steps = append(report.Steps, s)
		if report.Success && ctx.Err() != nil {
			report.Success = false
			err = errors.New("budget exceeded")
			report.Steps = append(report.Steps, Step{Name: "budget", Status: "failed", Reason: "budget_exceeded"})
		}
	}()
	cache := ""
	if o.Mode == "remote" {
		cache = filepath.Join(root.path, "modcache")
	}
	env := environment(os.Environ(), o, cache)
	r := runner{ctx, env, progress, &report}
	if err = r.step("owned_root", func() error {
		if !outsideRepository(root.path, o.Repo) {
			return errors.New("temporary root inside repository")
		}
		return nil
	}); err != nil {
		return report, errors.New("temporary root inside repository")
	}
	if o.Engine != "" && os.Getenv(engineKey(o.Engine)) == "" {
		_ = r.step("integration_precheck", func() error { return errors.New("selected test DSN is required") })
		return report, errors.New("selected test DSN required")
	}
	if o.Race {
		if err = r.step("race_precheck", func() error {
			b, e := run(ctx, o.Repo, env, "go", "env", "CGO_ENABLED")
			if e != nil || strings.TrimSpace(string(b)) != "1" {
				return errors.New("race requires cgo")
			}
			return nil
		}); err != nil {
			return report, errors.New("race unavailable")
		}
	} else {
		r.skip("race", "not_selected")
	}
	if b, e := run(ctx, o.Repo, env, "git", "rev-parse", "HEAD"); o.Mode == "workspace" && e == nil {
		v := strings.TrimSpace(string(b))
		if len(v) == 40 && concrete.MatchString(v) {
			report.SourceCommit = v
		}
	}
	if o.Mode == "workspace" {
		b, e := run(ctx, o.Repo, env, "git", "status", "--porcelain")
		if e != nil {
			return report, errors.New("workspace status failed")
		}
		report.WorkspaceDirty = len(strings.TrimSpace(string(b))) > 0
	}
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	service := filepath.Join(root.path, "service")
	version := o.Version
	if o.Mode == "workspace" {
		data, readErr := os.ReadFile(filepath.Join(o.Repo, "VERSION"))
		if readErr != nil {
			return report, fmt.Errorf("read framework VERSION: %w", readErr)
		}
		version = strings.TrimSpace(string(data))
		if !strings.HasPrefix(version, "v") || !concrete.MatchString(version) {
			return report, errors.New("invalid framework VERSION")
		}
	}
	if err = r.step("service_template", func() error {
		return prepareService(filepath.Join(o.Repo, "templates", "service"), service, version)
	}); err != nil {
		return report, errors.New("service template preparation failed")
	}
	if o.Mode == "workspace" {
		if err = r.step("workspace_replace", func() error {
			_, e := run(ctx, service, env, "go", "mod", "edit", "-replace="+framework+"="+o.Repo)
			return e
		}); err != nil {
			return report, errors.New("replace failed")
		}
	}
	if err = r.step("service_tidy", func() error { _, e := run(ctx, service, env, "go", "mod", "tidy"); return e }); err != nil {
		return report, errors.New("service tidy failed")
	}
	source := o.Repo
	if err = r.step("resolve_framework", func() error {
		b, e := run(ctx, service, env, "go", "list", "-m", "-json", framework)
		if e != nil {
			return e
		}
		var m struct {
			Path, Version, Dir string
			Replace            *struct{ Dir string }
			Origin             *struct{ Hash string }
		}
		if json.Unmarshal(b, &m) != nil || m.Path != framework || m.Version == "" {
			return errors.New("invalid module metadata")
		}
		report.ResolvedVersion = m.Version
		if o.Mode == "remote" {
			if m.Replace != nil || m.Dir == "" {
				return errors.New("unexpected remote replacement")
			}
			source = m.Dir
			report.SourceCommit = ""
			b, e = run(ctx, service, env, "go", "mod", "download", "-json", framework+"@"+o.Version)
			if e != nil {
				return e
			}
			var d struct {
				Path, Version string
				Origin        *struct{ Hash string }
			}
			if json.Unmarshal(b, &d) != nil || d.Path != framework || d.Version != m.Version {
				return errors.New("invalid download metadata")
			}
			if d.Origin == nil || !validCommit(d.Origin.Hash) {
				return errors.New("remote commit metadata unavailable")
			}
			if d.Origin != nil {
				report.SourceCommit = d.Origin.Hash
			}
		}
		return nil
	}); err != nil {
		return report, errors.New("resolution failed")
	}
	for _, item := range []struct {
		name string
		args []string
	}{{"service_test", []string{"test", "./...", "-count=1"}}, {"service_vet", []string{"vet", "./..."}}, {"service_build", []string{"build", "-buildvcs=false", "-o", filepath.Join(root.path, "api"+extension), "./cmd/api"}}} {
		if err = r.step(item.name, func() error { _, e := run(ctx, service, env, "go", item.args...); return e }); err != nil {
			return report, errors.New("service checks failed")
		}
	}
	api := filepath.Join(root.path, "api"+extension)
	for _, args := range [][]string{{"version"}, {"config", "check", "--config", "config.yaml"}, {"config", "explain", "--config", "config.yaml"}} {
		if err = r.step("service_"+strings.Join(args[:min(2, len(args))], "_"), func() error { _, e := run(ctx, service, env, api, args...); return e }); err != nil {
			return report, errors.New("inspection failed")
		}
	}
	fixture := filepath.Join(root.path, "ent")
	if err = r.step("ent_fixture", func() error {
		if e := copyFixture(filepath.Join(source, "examples", "ent"), fixture); e != nil {
			return e
		}
		b, e := os.ReadFile(filepath.Join(source, "go.mod"))
		if e != nil {
			return e
		}
		manifest, e := fixtureManifest(b, report.ResolvedVersion, o.Mode == "remote")
		if e != nil {
			return e
		}
		graph, e := run(ctx, source, env, "go", "list", "-mod=readonly", "-m", "-json", "all")
		if e != nil {
			return e
		}
		manifest, e = pinSourceGraph(manifest, graph)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(fixture, "go.mod"), manifest, 0600); e != nil {
			return e
		}
		sums, e := os.ReadFile(filepath.Join(source, "go.sum"))
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(fixture, "go.sum"), sums, 0600); e != nil {
			return e
		}
		if o.Mode == "workspace" {
			_, e = run(ctx, fixture, env, "go", "mod", "edit", "-replace="+framework+"="+o.Repo)
		}
		return e
	}); err != nil {
		return report, errors.New("fixture failed")
	}
	for _, item := range []struct {
		name string
		args []string
	}{{"ent_tidy", []string{"mod", "tidy"}}, {"ent_test", []string{"test", "./...", "-count=1"}}, {"ent_vet", []string{"vet", "./..."}}, {"ent_build", []string{"build", "-buildvcs=false", "./..."}}} {
		if err = r.step(item.name, func() error { _, e := run(ctx, fixture, env, "go", item.args...); return e }); err != nil {
			return report, errors.New("ent checks failed")
		}
	}
	available, e := fixtureHasTest(fixture, "TestAuthenticatedProductComposition")
	if e != nil {
		return report, errors.New("composition discovery failed")
	}
	if !available {
		if o.Mode == "workspace" {
			_ = r.step("ent_authenticated_composition", func() error { return errors.New("composition missing") })
			return report, errors.New("composition missing")
		}
		r.skip("ent_authenticated_composition", "selected_version_test_unavailable")
	} else {
		if err = r.step("ent_authenticated_composition", func() error {
			b, e := run(ctx, fixture, env, "go", "test", ".", "-run", "^TestAuthenticatedProductComposition$", "-count=1", "-json")
			if e != nil {
				return e
			}
			if !testPassed(b, "TestAuthenticatedProductComposition") {
				return errors.New("composition not executed")
			}
			return nil
		}); err != nil {
			return report, errors.New("composition failed")
		}
	}
	if o.Engine != "" {
		if err = r.step("ent_integration_"+o.Engine, func() error {
			dbEnv := append(append([]string(nil), env...), engineKey(o.Engine)+"="+os.Getenv(engineKey(o.Engine)))
			b, e := run(ctx, fixture, dbEnv, "go", "test", ".", "-run", "^TestIntegrationEnt$/"+o.Engine+"$", "-count=1", "-json")
			if e != nil {
				return e
			}
			if !integrationPassed(b, o.Engine) {
				return errors.New("integration skipped or not executed")
			}
			return nil
		}); err != nil {
			return report, errors.New("integration failed")
		}
	} else {
		r.skip("ent_integration", "not_selected")
	}
	if o.Race {
		for _, dir := range []string{service, fixture} {
			name := "service_race"
			if dir == fixture {
				name = "ent_race"
			}
			if err = r.step(name, func() error { _, e := run(ctx, dir, env, "go", "test", "-race", "./...", "-count=1"); return e }); err != nil {
				return report, errors.New("race failed")
			}
		}
	}
	if o.Process {
		if err = r.step("process_context_shutdown", func() error { return processCheck(ctx, service, root.path, extension, env, false) }); err != nil {
			return report, errors.New("process failed")
		}
		if runtime.GOOS != "windows" {
			if err = r.step("process_sigterm_shutdown", func() error { return processCheck(ctx, service, root.path, extension, env, true) }); err != nil {
				return report, errors.New("signal shutdown failed")
			}
		} else {
			r.skip("process_sigterm_shutdown", "unsupported_windows")
		}
	} else {
		r.skip("process", "not_selected")
	}
	if err = r.checkBusiness(source, filepath.Join(root.path, "business"), o); err != nil {
		return report, err
	}
	if ctx.Err() != nil {
		return report, errors.New("budget exceeded")
	}
	report.Success = true
	return report, nil
}
func withoutEnv(env []string, key string) []string {
	var out []string
	for _, s := range env {
		k, _, _ := strings.Cut(s, "=")
		if !strings.EqualFold(k, key) {
			out = append(out, s)
		}
	}
	return out
}
func engineKey(engine string) string { return "FRAME_TEST_" + strings.ToUpper(engine) + "_DSN" }
func integrationPassed(b []byte, engine string) bool {
	return testPassed(b, "TestIntegrationEnt/"+engine)
}
func testPassed(b []byte, name string) bool {
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	passed := false
	for decoder.More() {
		var event struct{ Action, Test string }
		if decoder.Decode(&event) != nil {
			return false
		}
		if event.Test == name {
			if event.Action == "skip" || event.Action == "fail" {
				return false
			}
			if event.Action == "pass" {
				passed = true
			}
		}
	}
	return passed
}
