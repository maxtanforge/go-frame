package logger

import (
	"context"
	"log/slog"
	"time"
)

// slsContent is the four-field JSON representation for SLS. The message is the
// same uncolored text written in console format, including metadata and stack.
type slsContent struct {
	Message string `json:"message"`
	Env     string `json:"env"`
	Level   string `json:"level"`
	PID     string `json:"pid"`
}

func logLocation(name string) (*time.Location, error) {
	if name == "" {
		return nil, nil
	}
	return time.LoadLocation(name)
}

type timezoneHandler struct {
	slog.Handler
	location *time.Location
}

func (h timezoneHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.location != nil {
		r.Time = r.Time.In(h.location)
	}
	return h.Handler.Handle(ctx, r)
}

func (h timezoneHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.Handler = h.Handler.WithAttrs(attrs)
	return h
}

func (h timezoneHandler) WithGroup(name string) slog.Handler {
	h.Handler = h.Handler.WithGroup(name)
	return h
}
