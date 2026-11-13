package toolkit

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/middleware"
	"github.com/maxtanforge/go-frame/schedule"
	"github.com/stretchr/testify/require"
)

func TestCachedReadsThroughMemoryStore(t *testing.T) {
	store, err := NewMemoryCache(8)
	require.NoError(t, err)
	loads := 0
	load := func(context.Context) ([]byte, error) {
		loads++
		return []byte("payload"), nil
	}
	ctx := context.Background()
	value, err := Cached(ctx, store, "greeting", time.Minute, load)
	require.NoError(t, err)
	require.Equal(t, "payload", string(value))
	_, err = Cached(ctx, store, "greeting", time.Minute, load)
	require.NoError(t, err)
	require.Equal(t, 1, loads, "第二次读取必须命中缓存")
	require.NoError(t, store.Delete(ctx, "greeting"))
	_, err = Cached(ctx, store, "greeting", time.Minute, load)
	require.NoError(t, err)
	require.Equal(t, 2, loads, "删除后必须重新加载")
}

func TestConstructorRejectsInvalidConfiguration(t *testing.T) {
	_, err := NewMemoryCache(0)
	require.Error(t, err)
	_, err = NewLimiter(0)
	require.Error(t, err)
	client, err := NewFetcher(nil)
	require.NoError(t, err)
	client.CloseIdleConnections()
}

func TestLimiterBoundsAdmission(t *testing.T) {
	limiter, err := NewLimiter(1)
	require.NoError(t, err)
	release, err := limiter.Acquire(context.Background())
	require.NoError(t, err)
	admitted := make(chan error, 1)
	go func() {
		second, err := limiter.Acquire(context.Background())
		if err == nil {
			second()
		}
		admitted <- err
	}()
	select {
	case err := <-admitted:
		t.Fatalf("第二个准入不得在释放前成功: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-admitted:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("释放后必须放行等待者")
	}
}

func TestPlannerRunsRegisteredTaskUntilShutdown(t *testing.T) {
	events := make(chan schedule.Event, 8)
	planner, err := NewPlanner([]schedule.Task{{
		Name:       "tick",
		IntervalMS: 40,
		TimeoutMS:  1_000,
		Run:        func(context.Context) error { return nil },
	}}, func(e schedule.Event) {
		select {
		case events <- e:
		default:
		}
	})
	require.NoError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- planner.Run() }()
	succeeded := false
	timeout := time.After(3 * time.Second)
	for !succeeded {
		select {
		case e := <-events:
			succeeded = e.State == schedule.Succeeded && e.Name == "tick"
		case <-timeout:
			t.Fatal("注册任务未在预算内执行")
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, planner.Shutdown(shutdownCtx))
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown 后 Run 必须返回")
	}
}

func TestGetJSONMapsUpstreamStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "fail") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	client, err := NewFetcher(nil)
	require.NoError(t, err)
	defer client.CloseIdleConnections()
	var result struct {
		OK bool `json:"ok"`
	}
	require.NoError(t, GetJSON(context.Background(), client, upstream.URL, &result))
	require.True(t, result.OK)
	err = GetJSON(context.Background(), client, upstream.URL+"/fail", &result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "status 500")
	require.NotContains(t, err.Error(), "httpclient:", "上游错误类型不得外泄给业务调用方")
}

func TestNewHTTPWiresCorrelationAndEnvelope(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	var observed atomic.Int32
	handler := NewHTTP(quiet, func(_ context.Context, event middleware.RequestEvent) {
		if event.Route == "/status" {
			observed.Add(1)
		}
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("x-trace-id", "order-42")
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "order-42", rec.Header().Get("x-trace-id"), "响应头必须回写上游 traceId")
	var envelope struct {
		Code    int  `json:"code"`
		Success bool `json:"success"`
		Data    struct {
			Trace string `json:"trace"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Equal(t, "order-42", envelope.Data.Trace, "业务数据与日志共用同一 traceId")
	require.Equal(t, int32(1), observed.Load(), "完成事件必须恰好观测一次")
}
