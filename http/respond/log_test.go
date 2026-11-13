package respond

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/telemetry/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctxLogger 构造一个写入 buf 的 ctx logger,验证"原错误走 request log"。
func ctxLogger(buf *strings.Builder) context.Context {
	return logger.IntoContext(context.Background(), slog.New(slog.NewTextHandler(buf, nil)))
}

func TestInvalidApplicationStatusLogsInternalError(t *testing.T) {
	for _, status := range []int{-1, 0, 200, 399, 600} {
		var logs strings.Builder
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctxLogger(&logs))
		Adapter(func(http.ResponseWriter, *http.Request) (any, error) {
			return nil, &httperror.Error{Status: status, Code: 123, Message: "internal-detail"}
		}).ServeHTTP(rec, r)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "internal-detail")
		assert.Contains(t, logs.String(), "level=ERROR")
		assert.Contains(t, logs.String(), "internal-detail")
	}
}

func TestAdapterUnknownErrorLoggedToRequestLog(t *testing.T) {
	buf := &strings.Builder{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctxLogger(buf))

	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return nil, errors.New("db password is hunter2")
	}).ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "hunter2", "响应体不得外泄")
	assert.Contains(t, buf.String(), "hunter2", "原始错误必须进 request log")
	assert.Contains(t, buf.String(), "error_name=*errors.errorString", "spec E3:错误路径须走 ErrAttrs 字段名")
	assert.Contains(t, buf.String(), "error_message=", "spec E3:错误路径须走 ErrAttrs 字段名")
}

func TestAdapterPanicLoggedToRequestLog(t *testing.T) {
	buf := &strings.Builder{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctxLogger(buf))

	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		panic("boom-detail")
	}).ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, buf.String(), "boom-detail", "panic 值必须进 request log")
	assert.Contains(t, buf.String(), "stack", "panic 记录须带堆栈")
	require.Equal(t, 1, strings.Count(buf.String(), "level=ERROR"), "one panic must produce one error log")
	assert.NotContains(t, rec.Body.String(), "boom-detail")
}

func TestPanicLoggingPreservesResponseBoundaries(t *testing.T) {
	for _, mode := range []string{"canceled", "deadline", "committed"} {
		t.Run(mode, func(t *testing.T) {
			buf := &strings.Builder{}
			ctx := ctxLogger(buf)
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
			serve := func() {
				Adapter(func(w http.ResponseWriter, _ *http.Request) (any, error) {
					if mode == "committed" {
						w.WriteHeader(http.StatusAccepted)
					}
					panic("private panic")
				})(rec, req)
			}
			if mode == "committed" {
				require.PanicsWithValue(t, http.ErrAbortHandler, serve)
				require.Equal(t, http.StatusAccepted, rec.Code)
			} else {
				require.NotPanics(t, serve)
			}
			require.Equal(t, 1, strings.Count(buf.String(), "level=ERROR"))
			require.NotContains(t, rec.Body.String(), "private panic")
			if mode == "deadline" {
				require.Equal(t, http.StatusGatewayTimeout, rec.Code)
			} else {
				require.Empty(t, rec.Body.String())
			}
		})
	}
}

func TestAdapterErrAbortHandlerPropagates(t *testing.T) {
	buf := &strings.Builder{}
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctxLogger(buf))

	defer func() {
		rec := recover()
		require.Equal(t, http.ErrAbortHandler, rec, "ErrAbortHandler 是 net/http 协议信号,须透传给上层")
	}()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		panic(http.ErrAbortHandler)
	}).ServeHTTP(httptest.NewRecorder(), req)
	t.Fatal("不应执行到此")
}

func TestAppErrorMessage(t *testing.T) {
	e := &httperror.Error{Status: 418, Code: 418, Message: "teapot", Err: fmt.Errorf("inner")}
	assert.Contains(t, e.Error(), "inner")
}

// debugLogger 构造输出 DEBUG 及以上级别的 ctx logger,使取消记录可见。
func debugLogger(buf *strings.Builder) context.Context {
	return logger.IntoContext(context.Background(), slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// 纯请求取消按 DEBUG 记录且只记录一次,不得产生 ERROR 级断连噪音。
func TestCanceledRequestLogsOnceAtDebug(t *testing.T) {
	buf := &strings.Builder{}
	ctx, cancel := context.WithCancel(debugLogger(buf))
	cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return nil, fmt.Errorf("late failure: %w", context.Canceled)
	}).ServeHTTP(rec, req)

	require.Empty(t, rec.Body.String())
	require.Zero(t, strings.Count(buf.String(), "level=ERROR"), "纯取消不是服务错误")
	require.Equal(t, 1, strings.Count(buf.String(), "request canceled"), "取消只记录一次")
	require.Equal(t, 1, strings.Count(buf.String(), "level=DEBUG"), "取消记录须为 DEBUG 级")
}

type cancelDuringMarshal struct{ cancel context.CancelFunc }

func (c cancelDuringMarshal) MarshalJSON() ([]byte, error) {
	c.cancel()
	return []byte(`"late"`), nil
}

// 序列化途中取消同样只产生一条 DEBUG 记录,与取消前置路径互斥不重复。
func TestCancelDuringMarshalLogsOnceAtDebug(t *testing.T) {
	buf := &strings.Builder{}
	ctx, cancel := context.WithCancel(debugLogger(buf))
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return cancelDuringMarshal{cancel}, nil
	}).ServeHTTP(rec, req)

	require.Empty(t, rec.Body.String())
	require.Zero(t, strings.Count(buf.String(), "level=ERROR"))
	require.Equal(t, 1, strings.Count(buf.String(), "request canceled"))
	require.Equal(t, 1, strings.Count(buf.String(), "level=DEBUG"))
}

func TestRequestCancellationPreservesErrorDiagnostics(t *testing.T) {
	independent := errors.New("independent storage failure")
	for _, path := range []string{"adapter", "write-error"} {
		for _, committed := range []bool{false, true} {
			for _, tc := range []struct {
				name     string
				canceled bool
				err      error
				level    string
			}{
				{"pure", true, context.Canceled, "DEBUG"},
				{"wrapped", true, fmt.Errorf("operation: %w", context.Canceled), "DEBUG"},
				{"joined", true, errors.Join(context.Canceled, independent), "ERROR"},
				{"wrapped joined", true, fmt.Errorf("operation: %w", errors.Join(context.Canceled, independent)), "ERROR"},
				{"independent", true, independent, "ERROR"},
				{"child cancellation", false, context.Canceled, "ERROR"},
			} {
				t.Run(fmt.Sprintf("%s/committed=%v/%s", path, committed, tc.name), func(t *testing.T) {
					buf := &strings.Builder{}
					ctx, cancel := context.WithCancel(debugLogger(buf))
					defer cancel()
					if tc.canceled {
						cancel()
					}
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
					h := func(w http.ResponseWriter, r *http.Request) (any, error) {
						if committed {
							_, _ = w.Write([]byte("committed body"))
						}
						if path == "write-error" {
							WriteError(w, r, tc.err)
							return nil, nil
						}
						return nil, tc.err
					}
					if path == "adapter" {
						Adapter(h)(rec, req)
					} else {
						Recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = h(w, r) })).ServeHTTP(rec, req)
					}
					require.Equal(t, 1, strings.Count(buf.String(), "level="), buf.String())
					require.Contains(t, buf.String(), "level="+tc.level)
					if committed {
						require.Equal(t, "committed body", rec.Body.String())
					} else if tc.canceled {
						require.Empty(t, rec.Body.String())
					} else {
						require.Equal(t, http.StatusInternalServerError, rec.Code)
					}
					if errors.Is(tc.err, independent) {
						require.Contains(t, buf.String(), independent.Error())
					}
				})
			}
		}
	}
}

func TestDeadlinePreservesIndependentErrorDiagnostics(t *testing.T) {
	independent := errors.New("independent storage failure")
	for _, path := range []string{"adapter", "write-error"} {
		for _, expired := range []bool{false, true} {
			for _, tc := range []struct {
				name string
				err  error
			}{
				{"pure", context.DeadlineExceeded},
				{"wrapped", fmt.Errorf("operation: %w", context.DeadlineExceeded)},
				{"joined", errors.Join(context.DeadlineExceeded, independent)},
				{"wrapped joined", fmt.Errorf("operation: %w", errors.Join(context.DeadlineExceeded, independent))},
				{"independent", independent},
			} {
				t.Run(fmt.Sprintf("%s/expired=%v/%s", path, expired, tc.name), func(t *testing.T) {
					buf := &strings.Builder{}
					ctx := debugLogger(buf)
					if expired {
						var cancel context.CancelFunc
						ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
						defer cancel()
					}
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
					if path == "adapter" {
						Adapter(func(http.ResponseWriter, *http.Request) (any, error) {
							return nil, tc.err
						})(rec, req)
					} else {
						WriteError(rec, req, tc.err)
					}
					status := http.StatusGatewayTimeout
					if !expired {
						status = http.StatusInternalServerError
					}
					require.Equal(t, status, rec.Code)
					require.NotContains(t, rec.Body.String(), independent.Error())
					if !expired || errors.Is(tc.err, independent) {
						require.Equal(t, 1, strings.Count(buf.String(), "level=ERROR"), buf.String())
						if errors.Is(tc.err, independent) {
							require.Contains(t, buf.String(), independent.Error())
						} else {
							require.Contains(t, buf.String(), "context deadline exceeded")
						}
					} else {
						require.Empty(t, buf.String(), "pure deadlines must not add error noise")
					}
				})
			}
		}
	}
}
