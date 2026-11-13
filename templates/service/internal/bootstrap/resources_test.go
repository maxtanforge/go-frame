package bootstrap

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/app/resource"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/redis"
)

type recordingServer struct {
	run      func() error
	shutdown func(context.Context) error
	close    func() error
}

func (s recordingServer) Run() error                         { return s.run() }
func (s recordingServer) Shutdown(ctx context.Context) error { return s.shutdown(ctx) }
func (s recordingServer) Close() error                       { return s.close() }

func TestCompositionResolvesResourcesBeforeServingAndRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "serve", true: "rollback"}[fail], func(t *testing.T) {
			var events []string
			db, cache := &database.DB{}, &redis.Pool{}
			dbResource, err := resource.New(resource.Options[*database.DB]{Name: "database", Open: func(context.Context) (*database.DB, error) { events = append(events, "open database"); return db, nil }, Close: func(context.Context, *database.DB) error { events = append(events, "close database"); return nil }})
			if err != nil {
				t.Fatal(err)
			}
			cacheResource, err := resource.New(resource.Options[*redis.Pool]{Name: "redis", Open: func(context.Context) (*redis.Pool, error) { events = append(events, "open redis"); return cache, nil }, Close: func(context.Context, *redis.Pool) error { events = append(events, "close redis"); return nil }})
			if err != nil {
				t.Fatal(err)
			}
			owned := resources{database: dbResource, cache: cacheResource}
			if _, err := owned.resolve(); err == nil {
				t.Fatal("unstarted resources resolved")
			}
			sentinel := errors.New("composition failed")
			closeErr := errors.New("client close failed")
			runErr := errors.New("service exited")
			services, err := app.NewGroup()
			if err != nil {
				t.Fatal(err)
			}
			hooks := append(owned.hooks(), owned.compositionHook(services, func(_ context.Context, deps dependencies, cleanup *app.Cleanup) error {
				events = append(events, "compose")
				if deps.Database != db || deps.Cache != cache {
					t.Fatal("typed dependencies lost")
				}
				if err := cleanup.Add("client", func(context.Context) error {
					events = append(events, "close client")
					return closeErr
				}); err != nil {
					return err
				}
				if err := services.Add(app.NamedService{Name: "http", Service: recordingServer{
					run:      func() error { events = append(events, "serve"); return runErr },
					shutdown: func(context.Context) error { events = append(events, "drain service"); return nil },
					close:    func() error { events = append(events, "close service"); return nil },
				}}); err != nil {
					return err
				}
				if fail {
					return sentinel
				}
				return nil
			}))
			application, err := app.New(services, app.Options{Hooks: hooks})
			if err != nil {
				t.Fatal(err)
			}
			err = application.Run(context.Background())
			if fail != errors.Is(err, sentinel) || !errors.Is(err, closeErr) {
				t.Fatalf("run: %v", err)
			}
			if errors.Is(err, runErr) != !fail {
				t.Fatalf("service run error: %v", err)
			}
			want := []string{"open database", "open redis", "compose"}
			if !fail {
				want = append(want, "serve", "drain service")
			}
			want = append(want, "close service", "close client", "close redis", "close database")
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events %v want %v", events, want)
			}
			if _, err := owned.resolve(); err == nil {
				t.Fatal("stopped resources resolved")
			}
		})
	}
}

func TestDisabledResourcesResolveToNil(t *testing.T) {
	deps, err := (resources{}).resolve()
	if err != nil || deps.Database != nil || deps.Cache != nil {
		t.Fatalf("dependencies: %+v, %v", deps, err)
	}
}

func TestConfiguredResourcesAreRetainedWithoutOpening(t *testing.T) {
	cfg := defaultRuntimeSettings()
	cfg.Database.Driver = "postgres"
	cfg.Database.URL = "postgres://user:secret@127.0.0.1:1/service"
	cfg.Redis.Addr = "127.0.0.1:1"
	owned, err := newResources(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if owned.database == nil || owned.cache == nil {
		t.Fatal("configured handles lost")
	}
	if len(owned.hooks()) != 2 || len(owned.checks()) != 2 {
		t.Fatal("missing lifecycle/readiness adapters")
	}
	for _, check := range owned.checks() {
		if err := check.Probe(context.Background()); err == nil {
			t.Fatalf("%s ready before startup", check.Name)
		}
	}
	if _, err := (resources{cache: owned.cache}).resolve(); err == nil {
		t.Fatal("unstarted cache resolved")
	}
}
