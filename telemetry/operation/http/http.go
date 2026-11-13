// Package operationhttp records bounded HTTP operations with route-controlled request capture.
package operationhttp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/internal/isnil"
	"github.com/maxtanforge/go-frame/internal/requestmeta"
	"github.com/maxtanforge/go-frame/internal/responsewriter"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/operation"
	"github.com/maxtanforge/go-frame/telemetry/trace"
)

// Mode selects whether a route records operation events.
type Mode uint8

// Inherit defers to the recorder default; Enabled/Disabled force the outcome.
const (
	Inherit Mode = iota
	Enabled
	Disabled
)

// Metadata is the route-declared operation description attached to events.
type Metadata struct {
	Action       string `json:"action,omitempty"`
	Description  string `json:"description,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	// Route is a trusted route template supplied by a router adapter, never a raw URL.
	Route   string         `json:"route,omitempty"`
	Mode    Mode           `json:"mode"`
	Request RequestOptions `json:"request"`
}

// RequestOptions lets each interface select query, route params and JSON body logging.
// Inherit preserves surrounding metadata; the recorder default is Enabled.
type RequestOptions struct {
	Query  Mode `json:"query"`
	Body   Mode `json:"body"`
	Params Mode `json:"params"`
	// Exact top-level field names. Nil inherits/all fields; an empty slice selects none.
	QueryFields  []string `json:"query_fields"`
	BodyFields   []string `json:"body_fields"`
	ParamsFields []string `json:"params_fields"`
	// Extra sensitive key names, matched recursively after normalization.
	// Lists accumulate across recorder and route policies; nil/empty never clear inherited keys.
	SensitiveFields []string `json:"sensitive_fields"`
}

// Clone detaches mutable route configuration. Nil and empty lists stay distinct.
func (m Metadata) Clone() Metadata {
	m.Request.QueryFields = slices.Clone(m.Request.QueryFields)
	m.Request.BodyFields = slices.Clone(m.Request.BodyFields)
	m.Request.ParamsFields = slices.Clone(m.Request.ParamsFields)
	m.Request.SensitiveFields = slices.Clone(m.Request.SensitiveFields)
	return m
}

// Validate bounds every field with the shared text rules; Route is a template.
func (m Metadata) Validate() error {
	if !operation.TextValid(m.Action, 256) || !operation.TextValid(m.Description, 512) || !operation.TextValid(m.ResourceType, 128) || !operation.TextValid(m.Route, 2048) || m.Mode > Disabled || m.Request.Query > Disabled || m.Request.Body > Disabled || m.Request.Params > Disabled {
		return operation.ErrInvalid
	}
	for _, keys := range [][]string{m.Request.QueryFields, m.Request.BodyFields, m.Request.ParamsFields, m.Request.SensitiveFields} {
		if len(keys) > 32 {
			return operation.ErrInvalid
		}
		for _, key := range keys {
			if key == "" || !operation.TextValid(key, 128) {
				return operation.ErrInvalid
			}
		}
	}
	return nil
}

// Options bounds sink execution and observes its failures.
type Options struct {
	// Request configures default capture. Zero values enable all query/params/JSON body
	// fields; route metadata may select fields or disable either source.
	Request RequestOptions
	// Timeout bounds cooperative sink work; zero defaults to 100ms.
	Timeout time.Duration
	// OnError observes sink failures. It must be fast, concurrency-safe and must
	// not panic; a panic is isolated. Nil logs a generic diagnostic without cause.
	OnError func(context.Context, error)
}

// Recorder fans structured events out to one sink within the configured bounds.
type Recorder struct {
	sink    operation.Sink
	options Options
}

// New validates the sink and snapshots the options.
func New(sink operation.Sink, o Options) (*Recorder, error) {
	if isnil.Check(sink) {
		return nil, errors.New("operation/http: sink required")
	}
	if o.Timeout < 0 {
		return nil, errors.New("operation/http: invalid timeout")
	}
	if o.Timeout == 0 {
		o.Timeout = 100 * time.Millisecond
	}
	o.Request = mergeRequest(RequestOptions{Query: Enabled, Body: Enabled, Params: Enabled}, o.Request)
	metadata := Metadata{Request: o.Request}.Clone()
	if err := metadata.Validate(); err != nil {
		return nil, err
	}
	o.Request = metadata.Request
	return &Recorder{sink: sink, options: o}, nil
}

func mergeRequest(parent, child RequestOptions) RequestOptions {
	if child.Params != Inherit {
		parent.Params = child.Params
	}
	if child.ParamsFields != nil {
		parent.ParamsFields = child.ParamsFields
	}
	if child.Query != Inherit {
		parent.Query = child.Query
	}
	if child.Body != Inherit {
		parent.Body = child.Body
	}
	if child.QueryFields != nil {
		parent.QueryFields = child.QueryFields
	}
	if child.BodyFields != nil {
		parent.BodyFields = child.BodyFields
	}
	if len(child.SensitiveFields) > 0 {
		keys := make([]string, 0, len(parent.SensitiveFields)+len(child.SensitiveFields))
		seen := make(map[string]bool, cap(keys))
		for _, list := range [][]string{parent.SensitiveFields, child.SensitiveFields} {
			for _, key := range list {
				normalized := normalizeKey(key)
				if !seen[normalized] {
					keys = append(keys, key)
					seen[normalized] = true
				}
			}
		}
		parent.SensitiveFields = keys
	}
	return parent
}

type contextKey struct{}
type state struct {
	mu                       sync.Mutex
	active                   bool
	metadata                 Metadata
	resourceType, resourceID string
	fields                   map[string]any
	query                    string
	queryTooLarge            bool
	body                     *bodyCapture
	bodyUnsupported          bool
	bodyPresent              bool
}

func ensure(ctx context.Context) (context.Context, *state) {
	if s, ok := ctx.Value(contextKey{}).(*state); ok {
		return ctx, s
	}
	s := &state{}
	return context.WithValue(ctx, contextKey{}, s), s
}

// With supplies route metadata to the surrounding recorder. It does not create
// an additional event. Invalid metadata fails before invoking the handler.
func With(m Metadata) func(http.Handler) http.Handler {
	m = m.Clone()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := m.Validate(); err != nil {
				respond.WriteError(w, r, err)
				return
			}
			ctx, s := ensure(r.Context())
			s.mu.Lock()
			if m.Action != "" {
				s.metadata.Action = m.Action
			}
			if m.Route != "" {
				s.metadata.Route = m.Route
			}
			if m.ResourceType != "" {
				s.metadata.ResourceType = m.ResourceType
			}
			if m.Description != "" {
				s.metadata.Description = m.Description
			}
			if m.Mode != Inherit {
				s.metadata.Mode = m.Mode
			}
			s.metadata.Request = mergeRequest(s.metadata.Request, m.Request)
			captureRequest(r, s)
			s.mu.Unlock()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SetResource attaches explicitly selected resource identity, not a raw URL.
// It requires Recorder.Middleware; route metadata alone is insufficient.
func SetResource(ctx context.Context, kind, id string) error {
	if !operation.TextValid(kind, 128) || !operation.TextValid(id, 128) {
		return operation.ErrInvalid
	}
	s, ok := ctx.Value(contextKey{}).(*state)
	if !ok {
		return errors.New("operation/http: recorder missing")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return errors.New("operation/http: recorder missing")
	}
	s.resourceType, s.resourceID = kind, id
	return nil
}

// SetFields replaces custom fields with a validated independent snapshot.
// It requires Recorder.Middleware; route metadata alone is insufficient.
func SetFields(ctx context.Context, fields map[string]any) error {
	snapshot, err := operation.CopyFields(fields)
	if err != nil {
		return err
	}
	s, ok := ctx.Value(contextKey{}).(*state)
	if !ok {
		return errors.New("operation/http: recorder missing")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return errors.New("operation/http: recorder missing")
	}
	s.fields = snapshot
	return nil
}

// Middleware records one event for the wrapped handler; hijacked connections
// and cancellations keep their distinct outcomes.
func (rec *Recorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, s := ensure(requestmeta.Ensure(r.Context()))
		s.mu.Lock()
		if s.active {
			s.mu.Unlock()
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		s.active = true
		s.metadata.Request = mergeRequest(rec.options.Request, s.metadata.Request)
		captureRequest(r, s)
		s.mu.Unlock()
		r = r.WithContext(ctx)
		rw := &responsewriter.Writer{ResponseWriter: w}
		start := time.Now()
		path := requestmeta.Path(r)
		defer func() {
			finished := time.Now()
			requestmeta.Finish(ctx, finished)
			failure := recover()
			if failure != nil {
				defer func() { panic(failure) }()
			}
			if rc := chi.RouteContext(ctx); rc != nil {
				requestmeta.PublishParams(ctx, rc.URLParams.Keys, rc.URLParams.Values)
			}
			params, paramsTooLarge := requestmeta.Params(ctx)
			s.mu.Lock()
			m := s.metadata
			e := operation.Event{ResourceType: s.resourceType, ResourceID: s.resourceID, Fields: s.fields}
			var request []byte
			if m.Mode != Disabled {
				request = requestSnapshot(s, m.Request, params, paramsTooLarge)
			}
			s.mu.Unlock()
			if e.ResourceType == "" {
				e.ResourceType = m.ResourceType
			}
			if m.Mode == Disabled {
				return
			}
			e.Request = request
			route := "<unmatched>"
			if rc := chi.RouteContext(ctx); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			if m.Route != "" {
				route = m.Route
			}
			e.Method = r.Method
			if !operation.TextValid(e.Method, 32) {
				e.Method = "_OTHER"
			}
			e.Route = route
			e.Path = path
			e.Action = m.Action
			if e.Action == "" {
				e.Action = e.Method + " " + route
				if !operation.TextValid(e.Action, 256) {
					e.Action = e.Method + " <route>"
				}
			}
			if !operation.TextValid(e.Route, 2048) {
				e.Route = "<route>"
			}
			e.Description = m.Description
			e.TraceID = trace.FromContext(ctx)
			e.Timestamp = start.UTC().UnixMilli()
			e.DurationMS = float64(finished.Sub(start)) / float64(time.Millisecond)
			e.Status = rw.Status()
			e.Outcome = operation.Completed
			switch {
			case failure != nil:
				e.Outcome = operation.Aborted
			case rw.Hijacked():
				e.Outcome = operation.Hijacked
			case errors.Is(ctx.Err(), context.Canceled):
				e.Outcome = operation.Canceled
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				e.Outcome = operation.Failed
			case e.Status == 401 || e.Status == 403:
				e.Outcome = operation.Denied
			case e.Status >= 400:
				e.Outcome = operation.Failed
			case e.Status == 0:
				e.Status = 200
			}
			if actor, ok := requestmeta.Actor(ctx); ok {
				e.ActorID, e.ActorName, e.ActorIssuer = actor.ID, actor.Name, actor.Issuer
			} else if actor, ok := principal.FromContext(ctx); ok {
				e.ActorID, e.ActorName, e.ActorIssuer = actor.ID, actor.Name, actor.Issuer
			}
			snapshot, err := e.Snapshot()
			if err != nil {
				rec.report(ctx, err)
				return
			}
			sinkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rec.options.Timeout)
			defer cancel()
			if err := record(sinkCtx, rec.sink, snapshot); err != nil {
				rec.report(sinkCtx, err)
			}
		}()
		next.ServeHTTP(rw, r)
	})
}
func record(ctx context.Context, sink operation.Sink, e operation.Event) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("operation/http: sink panic")
		}
	}()
	return sink.Record(ctx, e)
}
func (rec *Recorder) report(ctx context.Context, err error) {
	defer func() {
		if recover() != nil {
			logger.FromContext(ctx).ErrorContext(ctx, "operation failure observer panicked", "kind", "diagnostic")
		}
	}()
	if rec.options.OnError != nil {
		rec.options.OnError(ctx, err)
		return
	}
	logger.FromContext(ctx).ErrorContext(ctx, "operation sink failed", "kind", "diagnostic")
}
