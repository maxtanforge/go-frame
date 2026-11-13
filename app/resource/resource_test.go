package resource_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/app/resource"
)

type managed struct {
	ping     func(context.Context) error
	closeErr error
	closed   atomic.Int32
}

type valueManaged struct{}

func newManagedResource(name string, open func(context.Context) (*managed, error)) (*resource.Resource[*managed], error) {
	return resource.New(resource.Options[*managed]{Name: name, Open: open,
		Probe: func(ctx context.Context, m *managed) error { return m.Ping(ctx) },
		Close: func(_ context.Context, m *managed) error { return m.Close() },
	})
}

func TestStopBeforeStartDoesNotCloseUncreatedValueType(t *testing.T) {
	r, _ := resource.New(resource.Options[valueManaged]{Name: "value", Open: func(context.Context) (valueManaged, error) {
		t.Fatal("unexpected factory call")
		return valueManaged{}, nil
	}, Close: func(context.Context, valueManaged) error { return errors.New("value was closed") }})
	if err := r.Hook().Stop(context.Background()); err != nil {
		t.Fatalf("closed an uncreated value: %v", err)
	}
}

func TestCanceledStartupDoesNotPublishValue(t *testing.T) {
	for _, phase := range []string{"before factory", "during probe"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opened := false
			m := &managed{ping: func(context.Context) error { cancel(); return nil }}
			r, _ := newManagedResource("db", func(context.Context) (*managed, error) { opened = true; return m, nil })
			if phase == "before factory" {
				cancel()
			}
			if err := r.Hook().Start(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if phase == "before factory" && opened {
				t.Fatal("factory called with canceled startup")
			}
			if _, err := r.Value(); err == nil {
				t.Fatal("canceled startup published value")
			}
			if err := r.Hook().Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (!opened && m.closed.Load() != 0) || (opened && m.closed.Load() != 1) {
				t.Fatal("canceled startup resource not cleaned up")
			}
		})
	}
}

func (m *managed) Ping(ctx context.Context) error {
	if m.ping != nil {
		return m.ping(ctx)
	}
	return nil
}
func (m *managed) Close() error { m.closed.Add(1); return m.closeErr }

func TestLazyLifecycle(t *testing.T) {
	ctx := context.Background()
	m := &managed{}
	opens := 0
	r, err := newManagedResource("db", func(context.Context) (*managed, error) { opens++; return m, nil })
	if err != nil {
		t.Fatal(err)
	}
	if opens != 0 || r.Name() != "db" || r.Hook().Name != "db" {
		t.Fatal("construction must be lazy and names consistent")
	}
	if _, err := r.Value(); err == nil {
		t.Fatal("value before start")
	}
	if err := r.Probe(ctx); err == nil {
		t.Fatal("ready before start")
	}
	if err := r.Hook().Start(ctx); err != nil {
		t.Fatal(err)
	}
	if v, err := r.Value(); err != nil || v != m {
		t.Fatalf("value=%v err=%v", v, err)
	}
	if err := r.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Hook().Start(ctx); err == nil {
		t.Fatal("repeated start accepted")
	}
	if err := r.Hook().Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Hook().Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Value(); err == nil {
		t.Fatal("value after stop")
	}
	if err := r.Probe(ctx); err == nil {
		t.Fatal("ready after stop")
	}
	if opens != 1 || m.closed.Load() != 1 {
		t.Fatal("must open and close once")
	}
}

func TestValidationAndStopBeforeStart(t *testing.T) {
	factory := func(context.Context) (*managed, error) { t.Fatal("unexpected factory"); return nil, nil }
	if _, err := newManagedResource(" ", factory); err == nil {
		t.Fatal("blank name accepted")
	}
	if _, err := newManagedResource("db", nil); err == nil {
		t.Fatal("nil factory accepted")
	}
	r, _ := newManagedResource("db", factory)
	if err := r.Hook().Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Hook().Start(context.Background()); err == nil {
		t.Fatal("start after stop accepted")
	}
}

type unusedService struct{ t *testing.T }

func (s unusedService) Run() error                   { s.t.Error("service must not run"); return nil }
func (unusedService) Shutdown(context.Context) error { return nil }
func (unusedService) Close() error                   { return nil }

func TestAppRollsBackFailedResource(t *testing.T) {
	for _, factoryFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "probe", true: "factory"}[factoryFailure], func(t *testing.T) {
			startErr, closeErr := errors.New("start"), errors.New("close")
			m := &managed{closeErr: closeErr, ping: func(context.Context) error { return startErr }}
			r, _ := newManagedResource("db", func(context.Context) (*managed, error) {
				if factoryFailure {
					return m, startErr
				}
				return m, nil
			})
			a, err := app.New(unusedService{t}, app.Options{Hooks: []app.Hook{r.Hook()}})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Run(context.Background()); !errors.Is(err, startErr) || !errors.Is(err, closeErr) {
				t.Fatalf("lost cause: %v", err)
			}
			if _, err := r.Value(); err == nil {
				t.Fatal("failed resource published")
			}
			if err := r.Hook().Stop(context.Background()); !errors.Is(err, closeErr) {
				t.Fatalf("lost close error: %v", err)
			}
			if m.closed.Load() != 1 {
				t.Fatal("partial resource not closed exactly once")
			}
		})
	}
}

func TestNilFactoryResults(t *testing.T) {
	ctx := context.Background()
	r, _ := newManagedResource("typed", func(context.Context) (*managed, error) { return nil, nil })
	if err := r.Hook().Start(ctx); err == nil {
		t.Fatal("typed nil accepted")
	}
	if err := r.Hook().Stop(ctx); err != nil {
		t.Fatal(err)
	}
	i, _ := resource.New(resource.Options[any]{Name: "interface", Open: func(context.Context) (any, error) { return nil, nil }, Close: func(context.Context, any) error { t.Fatal("closed nil"); return nil }})
	if err := i.Hook().Start(ctx); err == nil {
		t.Fatal("nil interface accepted")
	}
	if err := i.Hook().Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStartupPublicationAndConcurrentStop(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var r *resource.Resource[*managed]
	m := &managed{ping: func(context.Context) error {
		if _, err := r.Value(); err == nil {
			t.Error("published during startup ping")
		}
		close(entered)
		<-release
		return nil
	}}
	r, _ = newManagedResource("db", func(context.Context) (*managed, error) { return m, nil })
	started := make(chan error, 1)
	go func() { started <- r.Hook().Start(ctx) }()
	<-entered
	if err := r.Probe(ctx); err == nil {
		t.Fatal("ready during startup")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := r.Hook().Stop(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	close(release)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if m.closed.Load() != 1 {
		t.Fatal("concurrent stop must close once")
	}
	if _, err := r.Value(); err == nil {
		t.Fatal("published after concurrent stop")
	}
}

// sdkClient deliberately has neither Ping nor Close methods.
type sdkClient struct{ token string }

func TestArbitraryClientOptionalProbeAndCleanupContext(t *testing.T) {
	client := &sdkClient{token: "token"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closes := 0
	closeErr := errors.New("cleanup")
	r, err := resource.New(resource.Options[*sdkClient]{Name: "sdk", Open: func(context.Context) (*sdkClient, error) { return client, nil }, Close: func(got context.Context, v *sdkClient) error {
		closes++
		if got != ctx || got.Err() != context.Canceled || v != client {
			t.Error("cleanup lost value or context")
		}
		return closeErr
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Probe(context.Background()); err == nil {
		t.Fatal("ready before start")
	}
	if err = r.Hook().Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v, err := r.Value(); err != nil || v != client {
		t.Fatalf("value=%v err=%v", v, err)
	}
	if err = r.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = r.Hook().Stop(ctx); !errors.Is(err, closeErr) {
			t.Fatal(err)
		}
	}
	if closes != 1 {
		t.Fatalf("closes=%d", closes)
	}
	if err = r.Probe(context.Background()); err == nil {
		t.Fatal("ready after stop")
	}
}

func TestCloseRequired(t *testing.T) {
	if _, err := resource.New(resource.Options[int]{Name: "value", Open: func(context.Context) (int, error) { return 1, nil }}); err == nil {
		t.Fatal("missing close accepted")
	}
}

func TestFactoryErrorRetainsAcquiredValue(t *testing.T) {
	cause := errors.New("open")
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil", true: "partial"}[partial], func(t *testing.T) {
			closes := 0
			var client *sdkClient
			if partial {
				client = &sdkClient{}
			}
			r, _ := resource.New(resource.Options[*sdkClient]{Name: "sdk", Open: func(context.Context) (*sdkClient, error) { return client, cause }, Close: func(context.Context, *sdkClient) error { closes++; return nil }})
			if err := r.Hook().Start(context.Background()); !errors.Is(err, cause) {
				t.Fatal(err)
			}
			if err := r.Hook().Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 0
			if partial {
				want = 1
			}
			if closes != want {
				t.Fatalf("closes=%d want=%d", closes, want)
			}
		})
	}
}

func TestProbeCannotPublishSuccessAfterStop(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	r, _ := resource.New(resource.Options[int]{Name: "value", Open: func(context.Context) (int, error) { return 0, nil }, Probe: func(context.Context, int) error {
		calls++
		if calls > 1 {
			close(entered)
			<-release
		}
		return nil
	}, Close: func(context.Context, int) error { return nil }})
	if err := r.Hook().Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Probe(context.Background()) }()
	<-entered
	if err := r.Hook().Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("in-flight probe published success after stop")
	}
}

func TestZeroValueAcquiredWithFactoryErrorIsClosed(t *testing.T) {
	cause := errors.New("partial open")
	closes := 0
	r, err := resource.New(resource.Options[int]{Name: "handle", Open: func(context.Context) (int, error) { return 0, cause }, Close: func(_ context.Context, v int) error {
		closes++
		if v != 0 {
			t.Errorf("value=%d", v)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Hook().Start(context.Background()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if err = r.Hook().Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if closes != 1 {
		t.Fatalf("closes=%d", closes)
	}
}
