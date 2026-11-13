// Package respond provides JSON envelopes, error handling, and explicit error mapping.
package respond

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/maxtanforge/go-frame/telemetry/trace"
	"github.com/maxtanforge/go-frame/validation"
)

// Handler returns envelope data or an error. Direct writes bypass the envelope.
type Handler func(http.ResponseWriter, *http.Request) (any, error)

const (
	msgSuccess = "成功"
	msgFail    = "失败"
	msgProgram = "服务处理异常"
)

// Envelope is the shared response shape. Data uses encoding/json without transformation.
type Envelope struct {
	Code      int    `json:"code"`
	Data      any    `json:"data"`
	Message   string `json:"message"`
	Success   bool   `json:"success"`
	Timestamp int64  `json:"timestamp"`
	TraceID   string `json:"traceId"`
}
type withMessage struct {
	data    any
	message string
}

// WithMessage supplies a custom success message.
func WithMessage(data any, message string) any { return withMessage{data, message} }

// Adapter wraps a returning handler, including panic recovery. Deadlines are cooperative:
// an expired context before the response commits produces 504. No worker is detached.
func Adapter(h Handler) http.HandlerFunc {
	return Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := w.(*wroteTracker)
		data, err := h(rw, r)
		if rw.wrote {
			if err != nil {
				logError(r.Context(), "handler error after write", err)
			}
			return
		}
		if err != nil || requestContextError(r.Context()) != nil {
			WriteError(rw, r, err)
			return
		}
		msg := msgSuccess
		if wm, ok := data.(withMessage); ok {
			data, msg = wm.data, wm.message
		}
		writeJSON(rw, r, http.StatusOK, envelope(r.Context(), 200, data, msg, true))
	})).ServeHTTP
}

// Recoverer handles ordinary and adapted handlers. A panic after commitment is
// logged and rethrown as ErrAbortHandler so net/http aborts the partial response.
// No envelope is appended. Hijacked connections remain owned by their handler.
// ErrAbortHandler itself always propagates to net/http without an additional log.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &wroteTracker{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler { //nolint:errorlint // panic 载荷身份比较,中止协议为精确匹配
					panic(rec)
				}
				logger.FromContext(r.Context()).ErrorContext(r.Context(), "panic recovered", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				if rw.wrote {
					panic(http.ErrAbortHandler)
				}
				// Recovery already recorded the cause and stack. Keep the normal
				// cancellation/deadline response policy without logging it again.
				writeError(rw, r, nil, false)
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// WriteError writes the shared error envelope. Canceled request contexts receive
// no further writes; operation cancellation alone remains a normal error. Wrapped
// request deadlines produce 504; child-operation deadlines follow normal error
// mapping, including an explicit application error. With a writer supplied by
// Adapter/Recoverer, committed responses are never rewritten. Other callers must invoke
// WriteError before writing headers or a body.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	writeError(w, r, err, true)
}

func writeError(w http.ResponseWriter, r *http.Request, err error, logCause bool) {
	if rw, ok := w.(*wroteTracker); ok && rw.wrote {
		if err != nil && logCause {
			logError(r.Context(), "error after write", err)
		}
		return
	}
	ctx := r.Context()
	// Only request cancellation means the client can no longer receive a
	// response. Cancellation of a child operation must not turn into empty 200.
	contextErr := requestContextError(ctx)
	if errors.Is(contextErr, context.Canceled) {
		if logCause {
			if err != nil {
				logError(ctx, "handler error", err)
			} else {
				logCanceled(ctx, contextErr)
			}
		}
		return
	}
	if errors.Is(contextErr, context.DeadlineExceeded) {
		if err != nil && logCause && !pureContextError(err, context.DeadlineExceeded) {
			logError(ctx, "handler error", err)
		}
		writeJSON(w, r, http.StatusGatewayTimeout, envelope(ctx, http.StatusGatewayTimeout, nil, http.StatusText(http.StatusGatewayTimeout), false))
		return
	}
	var app *httperror.Error
	if errors.As(err, &app) && app != nil {
		status, code, msg := normalize(app)
		if logCause {
			if app.Status < 400 || app.Status > 599 || errors.Is(err, context.DeadlineExceeded) {
				logError(ctx, "application error", err)
			} else if app.Err != nil {
				logError(ctx, "application error", app.Err)
			}
		}
		writeJSON(w, r, status, envelope(ctx, code, nil, msg, false))
		return
	}
	if errors.Is(err, principal.ErrUnauthenticated) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, r, 401, envelope(ctx, 401, nil, "Unauthorized", false))
		return
	}
	// errors.As also unwraps business-wrapped (%w) collections; schema-invalid
	// collections keep falling through to the internal-error path.
	isSchema := errors.Is(err, validation.ErrSchema)
	if !isSchema && errors.Is(err, validation.ErrLimit) {
		writeJSON(w, r, 400, envelope(ctx, 400, nil, "请求参数不正确", false))
		return
	}
	var issues *validation.Errors
	if !isSchema && errors.As(err, &issues) && issues != nil {
		writeJSON(w, r, 400, envelope(ctx, 400, nil, issues.FirstMessage(), false))
		return
	}
	var field *validation.FieldError
	if !isSchema && errors.As(err, &field) && field != nil {
		writeJSON(w, r, 400, envelope(ctx, 400, nil, "请求参数不正确", false))
		return
	}
	if err != nil && logCause {
		logError(ctx, "handler error", err)
	}
	writeJSON(w, r, 500, envelope(ctx, 500, nil, msgProgram, false))
}
func normalize(e *httperror.Error) (status, code int, msg string) {
	status = e.Status
	if status < 400 || status > 599 {
		return http.StatusInternalServerError, http.StatusInternalServerError, msgProgram
	}
	code = e.Code
	if code <= 0 {
		code = status
	}
	msg = e.Message
	if msg == "" {
		msg = msgFail
	}
	return
}
func envelope(ctx context.Context, code int, data any, msg string, success bool) Envelope {
	return Envelope{code, data, msg, success, time.Now().UnixMilli(), trace.FromContext(ctx)}
}
func logError(ctx context.Context, msg string, err error) {
	if ctx.Err() == context.Canceled && pureContextError(err, context.Canceled) { //nolint:errorlint // context.Err returns the exact cancellation sentinel
		logCanceled(ctx, err)
		return
	}
	logger.FromContext(ctx).LogAttrs(ctx, slog.LevelError, msg, logger.ErrAttrs(err)...)
}

// pureContextError follows only single-error wrappers. errors.Is would also
// match a context sentinel in a joined error and hide independent failures.
func pureContextError(err, cause error) bool {
	for err != nil {
		if err == cause { //nolint:errorlint // exact leaf identity is required, rather than errors.Is on a joined error
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

// logCanceled records pure request cancellation at DEBUG: client disconnects
// are routine, matching the ent driver policy, and stay invisible at default
// levels instead of polluting error streams.
func logCanceled(ctx context.Context, err error) {
	logger.FromContext(ctx).LogAttrs(ctx, slog.LevelDebug, "request canceled", logger.ErrAttrs(err)...)
}

func requestContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A deadline timer can be due before its callback publishes Err/Done.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, env Envelope) {
	ctx := r.Context()
	payload, err := json.Marshal(env)
	if err != nil {
		logError(ctx, "response marshal failed", err)
		status = 500
		// The fallback has nil Data and only built-in scalar fields, so JSON
		// marshaling cannot fail; no user marshaler is invoked on this path.
		payload, _ = json.Marshal(envelope(ctx, 500, nil, msgProgram, false))
	}
	// Serialization can invoke user code and consume the remaining request budget.
	contextErr := requestContextError(ctx)
	if errors.Is(contextErr, context.Canceled) {
		logCanceled(ctx, contextErr)
		return
	}
	if errors.Is(contextErr, context.DeadlineExceeded) && status != http.StatusGatewayTimeout {
		status = http.StatusGatewayTimeout
		// As above, this scalar-only envelope cannot fail to marshal.
		payload, _ = json.Marshal(envelope(ctx, http.StatusGatewayTimeout, nil, http.StatusText(http.StatusGatewayTimeout), false))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Del("Content-Length")
	w.WriteHeader(status)
	if _, err = w.Write(payload); err != nil {
		logError(ctx, "response write failed", err)
	}
}
