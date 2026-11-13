// Package schedule runs explicitly registered, process-local recurring tasks.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	// 嵌入时区数据库:Cron 任务声明 IANA 时区,容器内无需系统 tzdata。
	_ "time/tzdata" //nolint:revive // blank import embeds the tz database

	"github.com/robfig/cron/v3"
)

// Task describes an immutable registration. Set exactly one of IntervalMS or
// Cron. Cron uses five fields (minute/hour/day/month/weekday) and requires an
// explicit IANA Timezone. TimeoutMS zero disables the cooperative time limit.
// Run must honor cancellation; the scheduler cannot terminate a goroutine.
type Task struct {
	Name       string
	IntervalMS int64
	Cron       string
	Timezone   string
	TimeoutMS  int64
	Run        func(context.Context) error
}

// State identifies an execution event.
type State string

// Execution states reported through Event.
const (
	Started   State = "started"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Canceled  State = "canceled"
	Skipped   State = "skipped"
)

// Event describes a start, result, or skipped busy trigger. DurationMS is set
// on terminal events. Err can contain application data and is not public text.
type Event struct {
	Name       string
	State      State
	DurationMS int64
	Err        error
}

// Options supplies an optional execution observer. OnEvent can run concurrently
// even for the same task and must return promptly. It must not call Shutdown
// synchronously. Nil uses slog.Default (failures at ERROR, other events DEBUG).
type Options struct {
	OnEvent func(Event)
}

type registration struct {
	task     Task
	interval time.Duration
	timeout  time.Duration
	cron     cron.Schedule
	next     time.Time // owned by Run
	busy     bool      // protected by Scheduler.mu
}

// Scheduler is a one-shot app.Service. Task failures are execution events;
// they do not terminate scheduling or trigger immediate retries.
type Scheduler struct {
	entries     []*registration
	opts        Options
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	started     bool
	stopped     bool
	loopDone    bool
	active      int
	drained     chan struct{}
	drainClosed bool
}

// New validates all registrations and snapshots the task slice. Each name must
// be unique. Durations are milliseconds and are checked before conversion.
func New(tasks []Task, opts Options) (*Scheduler, error) {
	entries := make([]*registration, 0, len(tasks))
	names := make(map[string]bool, len(tasks))
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	for _, task := range tasks {
		if strings.TrimSpace(task.Name) == "" || names[task.Name] || task.Run == nil {
			return nil, errors.New("schedule: each task requires a unique nonempty name and callback")
		}
		names[task.Name] = true
		if task.IntervalMS < 0 || task.IntervalMS > math.MaxInt64/int64(time.Millisecond) || task.TimeoutMS < 0 || task.TimeoutMS > math.MaxInt64/int64(time.Millisecond) {
			return nil, fmt.Errorf("schedule: task %q has invalid millisecond duration", task.Name)
		}
		entry := &registration{task: task, interval: time.Duration(task.IntervalMS) * time.Millisecond, timeout: time.Duration(task.TimeoutMS) * time.Millisecond}
		if (task.IntervalMS > 0) == (task.Cron != "") {
			return nil, fmt.Errorf("schedule: task %q requires exactly one interval or Cron", task.Name)
		}
		if task.Cron == "" {
			if task.Timezone != "" {
				return nil, fmt.Errorf("schedule: interval task %q cannot specify timezone", task.Name)
			}
		} else {
			if task.Timezone == "" || task.Timezone == "Local" {
				return nil, fmt.Errorf("schedule: Cron task %q requires explicit timezone", task.Name)
			}
			location, err := time.LoadLocation(task.Timezone)
			if err != nil {
				return nil, fmt.Errorf("schedule: task %q timezone: %w", task.Name, err)
			}
			// Keep timezone separate; the parser's optional embedded timezone and
			// descriptors are deliberately outside this five-field contract.
			if len(strings.Fields(task.Cron)) != 5 {
				return nil, fmt.Errorf("schedule: task %q requires five Cron fields", task.Name)
			}
			parsed, err := parser.Parse(task.Cron)
			if err != nil {
				return nil, fmt.Errorf("schedule: task %q Cron: %w", task.Name, err)
			}
			parsed.(*cron.SpecSchedule).Location = location
			if parsed.Next(time.Now()).IsZero() {
				return nil, fmt.Errorf("schedule: task %q Cron has no next occurrence", task.Name)
			}
			entry.cron = parsed
		}
		entries = append(entries, entry)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{entries: entries, opts: opts, ctx: ctx, cancel: cancel, drained: make(chan struct{})}, nil
}

func (e *registration) advance(now time.Time) {
	if e.cron != nil {
		e.next = e.cron.Next(now)
	} else {
		e.next = now.Add(e.interval)
	}
}

// Run schedules until stopped. The first interval starts after one full period;
// Cron starts at its next occurrence. Delayed triggers are coalesced, not replayed.
func (s *Scheduler) Run() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("schedule: Run may only be called once")
	}
	s.started = true
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.loopDone = true; s.closeDrained(); s.mu.Unlock() }()
	now := time.Now()
	for _, entry := range s.entries {
		entry.advance(now)
	}
	for {
		var next time.Time
		for _, entry := range s.entries {
			if !entry.next.IsZero() && (next.IsZero() || entry.next.Before(next)) {
				next = entry.next
			}
		}
		if next.IsZero() {
			<-s.ctx.Done()
			return nil
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		now = time.Now()
		for _, entry := range s.entries {
			if entry.next.IsZero() || entry.next.After(now) {
				continue
			}
			entry.advance(now)
			s.mu.Lock()
			if s.stopped {
				s.mu.Unlock()
				return nil
			}
			if entry.busy {
				s.mu.Unlock()
				s.emit(Event{Name: entry.task.Name, State: Skipped})
				continue
			}
			entry.busy = true
			s.active++
			s.mu.Unlock()
			go s.execute(entry)
		}
	}
}

func (s *Scheduler) execute(entry *registration) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		entry.busy = false
		s.active--
		s.closeDrained()
	}()
	ctx := s.ctx
	if entry.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, entry.timeout)
		defer cancel()
	}
	start := time.Now()
	s.emit(Event{Name: entry.task.Name, State: Started})
	err := invoke(ctx, entry.task.Run)
	if err == nil {
		err = ctx.Err()
	}
	state := Succeeded
	if err != nil {
		state = Failed
		if s.ctx.Err() != nil && ctx.Err() == context.Canceled && pureCancellation(err) { //nolint:errorlint // 只认真实父 context 取消,不以 errors.Is 接受自定义匹配
			state = Canceled
		}
	}
	s.emit(Event{Name: entry.task.Name, State: state, DurationMS: time.Since(start).Milliseconds(), Err: err})
}

// pureCancellation retains joined errors and panic diagnostics as failures.
// Only a single-cause chain ending in the cancellation sentinel is expected stop.
func pureCancellation(err error) bool {
	for err != nil {
		if _, ok := err.(*PanicError); ok { //nolint:errorlint // 逐层排除 panic 诊断
			return false
		}
		if err == context.Canceled { //nolint:errorlint // 刻意只匹配哨兵身份
			return true
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
}

// PanicError retains a task panic and its stack for trusted observability sinks.
type PanicError struct {
	value any
	stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("schedule: task panic: %v", e.value) }

// Value returns the original panic payload, not necessarily an error.
func (e *PanicError) Value() any { return e.value }

// Stack returns an independent copy of the captured stack.
func (e *PanicError) Stack() []byte { return append([]byte(nil), e.stack...) }
func (e *PanicError) Unwrap() error { err, _ := e.value.(error); return err }

func invoke(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &PanicError{value: value, stack: debug.Stack()}
		}
	}()
	return fn(ctx)
}

func (s *Scheduler) emit(event Event) {
	if s.opts.OnEvent != nil {
		defer func() {
			if value := recover(); value != nil {
				slog.Error("schedule observer panic", "task", event.Name, "panic", value)
			}
		}()
		s.opts.OnEvent(event)
		return
	}
	level := slog.LevelDebug
	if event.State == Failed {
		level = slog.LevelError
	}
	slog.Log(context.Background(), level, "scheduled task", "task", event.Name, "state", event.State, "duration_ms", event.DurationMS, "error", event.Err)
}

// closeDrained must be called with mu held. No waiter goroutine is created.
func (s *Scheduler) closeDrained() {
	if s.stopped && s.loopDone && s.active == 0 && !s.drainClosed {
		close(s.drained)
		s.drainClosed = true
	}
}

func (s *Scheduler) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.cancel()
	if !s.started {
		s.loopDone = true
	}
	s.closeDrained()
}

// Shutdown stops triggers, cancels in-flight tasks and waits within ctx. An
// expired budget does not spawn a waiter; later calls may continue waiting.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("schedule: shutdown context is required")
	}
	s.stop()
	select {
	case <-s.drained:
		return nil
	default:
	}
	select {
	case <-s.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close requests immediate cancellation without waiting for task callbacks.
func (s *Scheduler) Close() error { s.stop(); return nil }
