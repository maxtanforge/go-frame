package reload

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"

	configcore "github.com/maxtanforge/go-frame/config"
)

func TestCallbackFieldDiagnosticPreservedWithoutWrapper(t *testing.T) {
	for _, stage := range []string{"validation", "preparation"} {
		t.Run(stage, func(t *testing.T) {
			_, fieldErr := configcore.Decode(struct {
				Count int `env:"COUNT"`
			}{}, configcore.Source{Name: "environment", Values: map[string]string{"count": "raw-secret"}})
			o := options()
			fail := false
			callback := func(config) error {
				if fail {
					return fmt.Errorf("wrapper-secret: %w", fieldErr)
				}
				return nil
			}
			if stage == "validation" {
				o.Validate = callback
			} else {
				o.Prepare = func(_, c config) (func(), error) { return nil, callback(c) }
			}
			m, err := New(config{map[string]int{"n": 1}}, o)
			if err != nil {
				t.Fatal(err)
			}
			fail = true
			snapshot, _, err := m.Update(context.Background(), config{map[string]int{"n": 2}})
			var detail *configcore.FieldError
			if !errors.As(err, &detail) {
				t.Fatalf("missing field diagnostic: %v", err)
			}
			if detail.Path != "count" || detail.Env != "COUNT" || detail.Rule != configcore.RuleType {
				t.Fatalf("incorrect diagnostic: %+v", detail)
			}
			if strings.Contains(err.Error(), "secret") || snapshot.Version != 1 {
				t.Fatalf("unsafe rejection: %v, version=%d", err, snapshot.Version)
			}
			var original *configcore.FieldError
			errors.As(fieldErr, &original)
			original.Path = "mutated"
			if detail.Path != "count" {
				t.Fatal("diagnostic was not copied")
			}
		})
	}
}

type config struct{ Values map[string]int }

func TestFieldDiagnosticRetainsRestartSentinel(t *testing.T) {
	_, diagnostic := configcore.Decode(struct{ Count int }{}, configcore.Source{Name: "environment", Values: map[string]string{"count": "secret"}})
	err := safeError("preparation", errors.Join(diagnostic, ErrRestartRequired))
	var field *configcore.FieldError
	if !errors.Is(err, ErrRestartRequired) || !errors.As(err, &field) {
		t.Fatalf("lost safe diagnostics: %v", err)
	}
}

func TestCallbackInvalidFieldDiagnosticIsHidden(t *testing.T) {
	var nilField *configcore.FieldError
	for _, callbackErr := range []error{nilField, &configcore.FieldError{Path: "secret", Rule: configcore.Rule("secret")}} {
		err := safeError("validation", callbackErr)
		var field *configcore.FieldError
		if errors.As(err, &field) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe field diagnostic: %v", err)
		}
	}
}

func copyConfig(v config) config {
	c := config{Values: map[string]int{}}
	for k, x := range v.Values {
		c.Values[k] = x
	}
	return c
}
func options() Options[config] {
	return Options[config]{Clone: copyConfig, Validate: func(config) error { return nil }}
}
func TestSnapshotIsolation(t *testing.T) {
	in := config{map[string]int{"n": 1}}
	o := options()
	o.Validate = func(v config) error { v.Values["n"] = 99; return nil }
	o.Prepare = func(a, b config) (func(), error) { a.Values["n"] = 88; b.Values["n"] = 77; return nil, nil }
	m, err := New(in, o)
	if err != nil {
		t.Fatal(err)
	}
	in.Values["n"] = 2
	s := m.Snapshot()
	if s.Version != 1 || s.Value.Values["n"] != 1 {
		t.Fatal(s)
	}
	s.Value.Values["n"] = 3
	c := config{map[string]int{"n": 4}}
	s, _, err = m.Update(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Values["n"] = 5
	s.Value.Values["n"] = 6
	if got := m.Snapshot(); got.Version != 2 || got.Value.Values["n"] != 4 {
		t.Fatal(got)
	}
}
func TestRejectedUpdatesRetainVersionAndHideErrors(t *testing.T) {
	for _, stage := range []string{"validation", "preparation"} {
		t.Run(stage, func(t *testing.T) {
			o := options()
			fail := false
			callback := func(config) error {
				if fail {
					return fmt.Errorf("secret: %w", ErrRestartRequired)
				}
				return nil
			}
			if stage == "validation" {
				o.Validate = callback
			} else {
				o.Prepare = func(_, c config) (func(), error) { return nil, callback(c) }
			}
			m, err := New(config{map[string]int{"n": 1}}, o)
			if err != nil {
				t.Fatal(err)
			}
			fail = true
			s, _, err := m.Update(context.Background(), config{map[string]int{"n": 2}})
			if err == nil || !errors.Is(err, ErrRestartRequired) || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), stage) {
				t.Fatal(err)
			}
			if s.Version != 1 || s.Value.Values["n"] != 1 || m.Snapshot().Version != 1 {
				t.Fatal(s)
			}
		})
	}
}
func TestCancellationAndOverflowPreventCommit(t *testing.T) {
	o := options()
	commits := 0
	ctx, cancel := context.WithCancel(context.Background())
	o.Prepare = func(_, _ config) (func(), error) { cancel(); return func() { commits++ }, nil }
	m, _ := New(config{}, o)
	if _, _, err := m.Update(ctx, config{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if commits != 0 || m.Snapshot().Version != 1 {
		t.Fatal(commits)
	}
	m.current.Version = math.MaxUint64
	if _, _, err := m.Update(context.Background(), config{}); err == nil {
		t.Fatal("overflow accepted")
	}
	if commits != 0 {
		t.Fatal(commits)
	}
}
func TestWaitingUpdateCanCancel(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	o := options()
	o.Prepare = func(_, _ config) (func(), error) { close(entered); <-release; return nil, nil }
	m, _ := New(config{}, o)
	done := make(chan struct{})
	go func() { defer close(done); _, _, _ = m.Update(context.Background(), config{}) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := m.Update(ctx, config{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	<-done
}
func TestConcurrentReadersAndWriters(t *testing.T) {
	m, _ := New(config{map[string]int{"n": 0}}, options())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s, _, err := m.Update(context.Background(), config{map[string]int{"n": j}})
				if err != nil {
					t.Error(err)
				}
				s.Value.Values["n"] = -1
				_ = m.Snapshot()
			}
		}()
	}
	wg.Wait()
	if m.Snapshot().Version != 401 {
		t.Fatal(m.Snapshot())
	}
}
func TestInvalidManagerInputs(t *testing.T) {
	if _, err := New(config{}, Options[config]{}); err == nil {
		t.Fatal("missing callbacks accepted")
	}
	var m Manager[config]
	if m.Snapshot().Version != 0 {
		t.Fatal("zero snapshot")
	}
	if _, _, err := m.Update(context.Background(), config{}); err == nil {
		t.Fatal("zero update")
	}
	valid, _ := New(config{}, options())
	if _, _, err := valid.Update(nil, config{}); err == nil { //nolint:staticcheck // 负向测试:显式验证 nil context 被拒绝
		t.Fatal("nil context")
	}
}

func TestInitialValidationAndUnknownErrorsAreSanitized(t *testing.T) {
	secret := errors.New("password=confidential")
	o := options()
	o.Validate = func(config) error { return secret }
	if _, err := New(config{}, o); err == nil || strings.Contains(err.Error(), "confidential") || errors.Is(err, secret) {
		t.Fatalf("unsafe initial validation error: %v", err)
	}
	o = options()
	o.Prepare = func(config, config) (func(), error) { return nil, secret }
	m, _ := New(config{}, o)
	if _, _, err := m.Update(context.Background(), config{}); err == nil || strings.Contains(err.Error(), "confidential") || errors.Is(err, secret) {
		t.Fatalf("unsafe preparation error: %v", err)
	}
}

func TestCommitCancellationStillPublishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := options()
	o.Prepare = func(config, config) (func(), error) { return cancel, nil }
	m, _ := New(config{Values: map[string]int{"n": 1}}, o)
	result, _, err := m.Update(ctx, config{Values: map[string]int{"n": 2}})
	if err != nil || result.Version != 2 || result.Value.Values["n"] != 2 {
		t.Fatalf("commit did not publish: %v %v", result, err)
	}
}

func diffPaths(a, b config) []string {
	paths := []string{}
	for k, x := range a.Values {
		if y, ok := b.Values[k]; !ok || x != y {
			paths = append(paths, k)
		}
	}
	for k := range b.Values {
		if _, ok := a.Values[k]; !ok {
			paths = append(paths, k)
		}
	}
	sort.Strings(paths)
	return paths
}

func TestUpdateReportsChangedPaths(t *testing.T) {
	o := options()
	o.Diff = diffPaths
	m, err := New(config{map[string]int{"level": 1, "labels": 2}}, o)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, paths, err := m.Update(context.Background(), config{map[string]int{"level": 9, "labels": 2, "message": 3}})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 {
		t.Fatal(snapshot.Version)
	}
	if strings.Join(paths, ",") != "level,message" {
		t.Fatal(paths)
	}
}

func TestUpdateWithoutDiffOmitsPaths(t *testing.T) {
	m, err := New(config{map[string]int{"n": 1}}, options())
	if err != nil {
		t.Fatal(err)
	}
	_, paths, err := m.Update(context.Background(), config{map[string]int{"n": 2}})
	if err != nil || len(paths) != 0 {
		t.Fatalf("paths=%v err=%v", paths, err)
	}
}

func TestUpdateFailureOmitsPaths(t *testing.T) {
	o := options()
	o.Diff = diffPaths
	o.Validate = func(v config) error {
		if v.Values["n"] == 2 {
			return errors.New("bad")
		}
		return nil
	}
	m, err := New(config{map[string]int{"n": 1}}, o)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, paths, err := m.Update(context.Background(), config{map[string]int{"n": 2}})
	if err == nil || paths != nil {
		t.Fatalf("paths=%v err=%v", paths, err)
	}
	if snapshot.Version != 1 {
		t.Fatal(snapshot.Version)
	}
}
