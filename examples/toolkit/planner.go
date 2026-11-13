package toolkit

import (
	"context"
	"log/slog"

	"github.com/maxtanforge/go-frame/schedule"
)

// NewPlanner registers fixed recurring jobs with an explicit observer.
// Nothing implicit starts: compose the returned Scheduler into the
// application lifecycle (see the background example). Run blocks until
// Shutdown; the observer may run concurrently and must return promptly.
func NewPlanner(jobs []schedule.Task, onEvent func(schedule.Event)) (*schedule.Scheduler, error) {
	return schedule.New(jobs, schedule.Options{OnEvent: onEvent})
}

// EventLogger adapts scheduler events to structured logs: failures at ERROR,
// everything else at DEBUG, with the event fields the scheduler guarantees.
func EventLogger(lg *slog.Logger) func(schedule.Event) {
	return func(e schedule.Event) {
		level := slog.LevelDebug
		if e.State == schedule.Failed {
			level = slog.LevelError
		}
		lg.Log(context.Background(), level, "scheduled task",
			"task", e.Name, "state", e.State, "duration_ms", e.DurationMS, "error", e.Err)
	}
}
