package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/app"
	"github.com/stretchr/testify/require"
)

type addedService struct {
	started chan struct{}
	done    chan struct{}
	once    sync.Once
	closed  atomic.Int32
}

func newAddedService() *addedService {
	return &addedService{started: make(chan struct{}), done: make(chan struct{})}
}
func (s *addedService) Run() error { close(s.started); <-s.done; return nil }
func (s *addedService) Shutdown(context.Context) error {
	s.once.Do(func() { close(s.done) })
	return nil
}
func (s *addedService) Close() error { s.closed.Add(1); return s.Shutdown(context.Background()) }

func TestGroupAddsServicesDuringComposition(t *testing.T) {
	base, worker := newAddedService(), newAddedService()
	group, err := app.NewGroup()
	require.NoError(t, err)
	application, err := app.New(group, app.Options{Hooks: []app.Hook{{Name: "compose", Stop: func(context.Context) error { return group.Close() }, Start: func(context.Context) error {
		return group.Add(app.NamedService{Name: "http", Service: base}, app.NamedService{Name: "worker", Service: worker})
	}}}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	for _, started := range []chan struct{}{base.started, worker.started} {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("member did not start")
		}
	}
	require.Error(t, group.Add(app.NamedService{Name: "late", Service: newAddedService()}))
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("application did not stop")
	}
	require.Error(t, group.Add(app.NamedService{Name: "after", Service: newAddedService()}))
}

func TestEmptyGroupRunFailsWithoutHangingShutdown(t *testing.T) {
	group, err := app.NewGroup()
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- group.Run() }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("empty group Run hung")
	}
	require.Error(t, group.Add(app.NamedService{Name: "late", Service: newAddedService()}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, group.Shutdown(ctx))
	require.NoError(t, group.Close())
	require.Error(t, group.Run())
}

func TestEmptyGroupCanStopBeforeComposition(t *testing.T) {
	for _, force := range []bool{false, true} {
		group, err := app.NewGroup()
		require.NoError(t, err)
		if force {
			require.NoError(t, group.Close())
		} else {
			require.NoError(t, group.Shutdown(context.Background()))
		}
		require.Error(t, group.Add(app.NamedService{Name: "late", Service: newAddedService()}))
		require.NoError(t, group.Run())
		require.NoError(t, group.Close())
	}
}

func TestGroupRegistrationFailureDoesNotPartiallyAdoptBatch(t *testing.T) {
	base, rejected := newAddedService(), newAddedService()
	group, err := app.NewGroup(app.NamedService{Name: "http", Service: base})
	require.NoError(t, err)
	require.Error(t, group.Add(app.NamedService{Name: "worker", Service: rejected}, app.NamedService{Name: "http", Service: rejected}))
	require.NoError(t, group.Close())
	require.Equal(t, int32(1), base.closed.Load())
	require.Zero(t, rejected.closed.Load())
}

func TestRegisteredServicesCloseOnStartupRollback(t *testing.T) {
	base, worker := newAddedService(), newAddedService()
	group, err := app.NewGroup()
	require.NoError(t, err)
	failure := errors.New("composition failed")
	application, err := app.New(group, app.Options{Hooks: []app.Hook{{Name: "compose", Stop: func(context.Context) error { return group.Close() }, Start: func(context.Context) error {
		if err := group.Add(app.NamedService{Name: "http", Service: base}, app.NamedService{Name: "worker", Service: worker}); err != nil {
			return err
		}
		return failure
	}}}})
	require.NoError(t, err)
	require.ErrorIs(t, application.Run(context.Background()), failure)
	require.Equal(t, int32(1), base.closed.Load())
	require.Equal(t, int32(1), worker.closed.Load())
	require.NoError(t, group.Close())
	select {
	case <-worker.started:
		t.Fatal("worker ran during failed startup")
	default:
	}
}
