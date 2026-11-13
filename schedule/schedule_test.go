package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/maxtanforge/go-frame/app"
	"github.com/stretchr/testify/require"
)

func TestCanceledEventDefaultLogIsDebug(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)
	s := &Scheduler{}
	s.emit(Event{Name: "task", State: State("canceled"), Err: context.Canceled})
	require.Contains(t, output.String(), `"level":"DEBUG"`)
}

func TestIndependentCancellationRemainsFailure(t *testing.T) {
	events := make(chan Event, 2)
	s, err := New([]Task{{Name: "task", IntervalMS: 10, Run: func(context.Context) error { return context.Canceled }}}, Options{OnEvent: func(e Event) { events <- e }})
	require.NoError(t, err)
	entry := s.entries[0]
	entry.busy = true
	s.active = 1
	s.execute(entry)
	<-events
	require.Equal(t, Failed, (<-events).State)
}

var _ app.Service = (*Scheduler)(nil)

func TestValidation(t *testing.T) {
	valid := Task{Name: "clean", IntervalMS: 10, Run: func(context.Context) error { return nil }}
	for name, change := range map[string]func(*Task){
		"empty name":              func(x *Task) { x.Name = "" },
		"blank name":              func(x *Task) { x.Name = "  " },
		"nil run":                 func(x *Task) { x.Run = nil },
		"missing plan":            func(x *Task) { x.IntervalMS = 0 },
		"negative interval":       func(x *Task) { x.IntervalMS = -1 },
		"overflow interval":       func(x *Task) { x.IntervalMS = math.MaxInt64 },
		"negative timeout":        func(x *Task) { x.TimeoutMS = -1 },
		"overflow timeout":        func(x *Task) { x.TimeoutMS = math.MaxInt64 },
		"two plans":               func(x *Task) { x.Cron = "* * * * *"; x.Timezone = "UTC" },
		"interval timezone":       func(x *Task) { x.Timezone = "UTC" },
		"missing timezone":        func(x *Task) { x.IntervalMS = 0; x.Cron = "* * * * *" },
		"bad timezone":            func(x *Task) { x.IntervalMS = 0; x.Cron = "* * * * *"; x.Timezone = "missing/zone" },
		"bad cron":                func(x *Task) { x.IntervalMS = 0; x.Cron = "bad"; x.Timezone = "UTC" },
		"implicit local timezone": func(x *Task) { x.IntervalMS = 0; x.Cron = "* * * * *"; x.Timezone = "Local" },
		"impossible cron":         func(x *Task) { x.IntervalMS = 0; x.Cron = "0 0 30 2 *"; x.Timezone = "UTC" },
		"embedded timezone":       func(x *Task) { x.IntervalMS = 0; x.Cron = "CRON_TZ=UTC * * * * *"; x.Timezone = "UTC" },
	} {
		t.Run(name, func(t *testing.T) {
			task := valid
			change(&task)
			s, err := New([]Task{task}, Options{})
			require.Error(t, err)
			require.Nil(t, s)
		})
	}
	_, err := New([]Task{valid, valid}, Options{})
	require.Error(t, err)
}

func TestIntervalSnapshotEventsAndNoOverlap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var mu sync.Mutex
		var events []Event
		tasks := []Task{{Name: "clean", IntervalMS: 10, Run: func(ctx context.Context) error {
			calls.Add(1)
			<-ctx.Done()
			return ctx.Err()
		}}}
		s, err := New(tasks, Options{OnEvent: func(e Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }})
		require.NoError(t, err)
		tasks[0].Run = func(context.Context) error { panic("changed registration") }
		done := make(chan error, 1)
		go func() { done <- s.Run() }()
		synctest.Wait()
		time.Sleep(35 * time.Millisecond)
		synctest.Wait()
		require.EqualValues(t, 1, calls.Load())
		require.NoError(t, s.Shutdown(context.Background()))
		require.NoError(t, <-done)
		mu.Lock()
		defer mu.Unlock()
		require.Len(t, events, 4)
		require.Equal(t, Started, events[0].State)
		require.Equal(t, Skipped, events[1].State)
		require.Equal(t, Skipped, events[2].State)
		require.Equal(t, State("canceled"), events[3].State)
		require.ErrorIs(t, events[3].Err, context.Canceled)
		require.EqualValues(t, 25, events[3].DurationMS)
		require.Equal(t, "clean", events[3].Name)
		require.Error(t, s.Run())
	})
}

func TestShutdownCancellationClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result func(context.Context) error
		want   State
	}{
		{"nil", func(context.Context) error { return nil }, State("canceled")},
		{"wrapped", func(ctx context.Context) error { return fmt.Errorf("stopping: %w", ctx.Err()) }, State("canceled")},
		{"joined", func(ctx context.Context) error { return errors.Join(ctx.Err(), errors.New("cleanup failed")) }, Failed},
		{"panic", func(ctx context.Context) error { panic(ctx.Err()) }, Failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				events := make(chan Event, 2)
				s, err := New([]Task{{Name: "task", IntervalMS: 10, Run: func(ctx context.Context) error { <-ctx.Done(); return tc.result(ctx) }}}, Options{OnEvent: func(e Event) { events <- e }})
				require.NoError(t, err)
				done := make(chan error, 1)
				go func() { done <- s.Run() }()
				synctest.Wait()
				time.Sleep(10 * time.Millisecond)
				synctest.Wait()
				require.NoError(t, s.Shutdown(context.Background()))
				require.NoError(t, <-done)
				<-events
				require.Equal(t, tc.want, (<-events).State)
			})
		})
	}
}

func TestTimeoutAndPanicEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan Event, 20)
		cause := errors.New("job panic")
		s, err := New([]Task{
			{Name: "timeout", IntervalMS: 100, TimeoutMS: 5, Run: func(ctx context.Context) error { <-ctx.Done(); return nil }},
			{Name: "panic", IntervalMS: 100, Run: func(context.Context) error { panic(cause) }},
			{Name: "success", IntervalMS: 100, Run: func(context.Context) error { return nil }},
		}, Options{OnEvent: func(e Event) { events <- e }})
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- s.Run() }()
		synctest.Wait()
		time.Sleep(110 * time.Millisecond)
		synctest.Wait()
		require.NoError(t, s.Shutdown(context.Background()))
		require.NoError(t, <-done)
		close(events)
		terminal := map[string]Event{}
		for event := range events {
			if event.State != Started {
				terminal[event.Name] = event
			}
		}
		require.Equal(t, Failed, terminal["timeout"].State)
		require.ErrorIs(t, terminal["timeout"].Err, context.DeadlineExceeded)
		require.EqualValues(t, 5, terminal["timeout"].DurationMS)
		require.ErrorIs(t, terminal["panic"].Err, cause)
		var panicErr *PanicError
		require.ErrorAs(t, terminal["panic"].Err, &panicErr)
		require.NotEmpty(t, panicErr.Stack())
		require.Equal(t, Succeeded, terminal["success"].State)
	})
}

func TestShutdownBudgetAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		s, err := New([]Task{{Name: "blocked", IntervalMS: 10, Run: func(context.Context) error { <-release; return nil }}}, Options{})
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- s.Run() }()
		synctest.Wait()
		time.Sleep(11 * time.Millisecond)
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		require.ErrorIs(t, s.Shutdown(ctx), context.DeadlineExceeded)
		require.NoError(t, s.Close())
		require.NoError(t, <-done)
		close(release)
		require.NoError(t, s.Shutdown(context.Background()))
	})
	s, err := New(nil, Options{})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	require.NoError(t, s.Run())
	require.Error(t, s.Run())
	require.NoError(t, s.Shutdown(context.Background()))
}

func TestBusyTriggersAreNotQueued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		s, err := New([]Task{{Name: "busy", IntervalMS: 10, Run: func(context.Context) error {
			if calls.Add(1) == 1 {
				<-release
			}
			return nil
		}}}, Options{})
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- s.Run() }()
		synctest.Wait()
		time.Sleep(35 * time.Millisecond)
		synctest.Wait()
		close(release)
		synctest.Wait()
		require.EqualValues(t, 1, calls.Load(), "busy triggers must not be replayed on completion")
		time.Sleep(5 * time.Millisecond)
		synctest.Wait()
		require.EqualValues(t, 2, calls.Load())
		require.NoError(t, s.Shutdown(context.Background()))
		require.NoError(t, <-done)
	})
}

func TestFailureDoesNotRetryAndObserverPanicIsIsolated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("task failure")
		var calls atomic.Int32
		failed := make(chan Event, 1)
		s, err := New([]Task{{Name: "failure", IntervalMS: 10, Run: func(context.Context) error { calls.Add(1); return cause }}}, Options{OnEvent: func(e Event) {
			if e.State == Failed {
				failed <- e
			}
			panic("observer failure")
		}})
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- s.Run() }()
		synctest.Wait()
		time.Sleep(19 * time.Millisecond)
		synctest.Wait()
		require.EqualValues(t, 1, calls.Load())
		require.ErrorIs(t, (<-failed).Err, cause)
		require.NoError(t, s.Shutdown(context.Background()))
		require.NoError(t, <-done)
	})
}

func TestShutdownBeforeRunAndNilContext(t *testing.T) {
	s, err := New(nil, Options{})
	require.NoError(t, err)
	require.Error(t, s.Shutdown(nil)) //nolint:staticcheck // 负向测试:显式验证 nil context 被拒绝
	require.NoError(t, s.Shutdown(context.Background()))
	require.NoError(t, s.Run())
	require.Error(t, s.Run())
}

func TestCronExplicitTimezone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// synctest starts at midnight UTC. Kathmandu is 05:45 at this instant.
		var calls atomic.Int32
		s, err := New([]Task{{Name: "cron", Cron: "46 5 * * *", Timezone: "Asia/Kathmandu", Run: func(context.Context) error { calls.Add(1); return nil }}}, Options{})
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- s.Run() }()
		synctest.Wait()
		time.Sleep(59 * time.Second)
		synctest.Wait()
		require.Zero(t, calls.Load())
		time.Sleep(time.Second)
		synctest.Wait()
		require.EqualValues(t, 1, calls.Load())
		require.NoError(t, s.Shutdown(context.Background()))
		require.NoError(t, <-done)
	})
}
