package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewRejectsInvalidOptions(t *testing.T) {
	probe := func(context.Context) error { return nil }
	for _, opts := range []Options{
		{Timeout: -time.Millisecond},
		{Concurrency: -1},
		{OptionalConcurrency: -1},
		{Checks: []Check{{Name: "db", Probe: nil}}},
		{Checks: []Check{{Name: "  ", Probe: probe}}},
		{Checks: []Check{{Name: "db", Probe: probe}, {Name: "db", Probe: probe}}},
	} {
		_, err := New(opts)
		require.Error(t, err, "options must be rejected: %+v", opts)
	}
	c, err := New(Options{})
	require.NoError(t, err)
	report := c.Readiness(context.Background())
	require.Equal(t, "ready", report.Status)
	require.Empty(t, report.Checks)
}

// 快照按名称排序:失败清单与 checks 摘要顺序确定,不随声明顺序漂移。
func TestReadinessReportsSortedRequiredFailures(t *testing.T) {
	c, err := New(Options{Checks: []Check{
		{Name: "zeta", Probe: func(context.Context) error { return errors.New("zeta down") }},
		{Name: "alpha", Probe: func(context.Context) error { return nil }},
		{Name: "mid", Probe: func(context.Context) error { return errors.New("mid down") }},
	}})
	require.NoError(t, err)
	report := c.Readiness(context.Background())
	require.Equal(t, "unavailable", report.Status)
	require.Len(t, report.Failures, 2)
	require.Equal(t, "mid", report.Failures[0].Name)
	require.Equal(t, "zeta", report.Failures[1].Name)
	require.Equal(t, "ok", report.Checks["alpha"])
}

func TestReadinessOptionalFailureDegrades(t *testing.T) {
	c, err := New(Options{Checks: []Check{
		{Name: "cache", Optional: true, Probe: func(context.Context) error { return errors.New("cache down") }},
		{Name: "db", Probe: func(context.Context) error { return nil }},
	}})
	require.NoError(t, err)
	report := c.Readiness(context.Background())
	require.Equal(t, "degraded", report.Status)
	require.Equal(t, "unavailable", report.Checks["cache"])
	require.Equal(t, "ok", report.Checks["db"])
	require.True(t, report.Failures[0].Optional)
}

// 摘流立即生效,不再执行任何探测。
func TestReadinessDrainingSkipsProbes(t *testing.T) {
	c, err := New(Options{Checks: []Check{{Name: "db", Probe: func(context.Context) error {
		t.Fatal("probe must not run while draining")
		return nil
	}}}})
	require.NoError(t, err)
	c.SetReady(false)
	report := c.Readiness(context.Background())
	require.Equal(t, "draining", report.Status)
	require.Empty(t, report.Checks)
	require.Empty(t, report.Failures)
}

func TestReadinessBudgetExpires(t *testing.T) {
	c, err := New(Options{Timeout: 30 * time.Millisecond, Checks: []Check{
		{Name: "db", Probe: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
	}})
	require.NoError(t, err)
	report := c.Readiness(context.Background())
	require.Equal(t, "unavailable", report.Status)
	require.ErrorIs(t, report.Failures[0].Err, context.DeadlineExceeded)
}

// 探测 panic 只计为该依赖失败,不外泄堆栈。
func TestProbePanicBecomesIsolatedFailure(t *testing.T) {
	c, err := New(Options{Checks: []Check{{Name: "db", Probe: func(context.Context) error {
		panic("probe exploded")
	}}}})
	require.NoError(t, err)
	report := c.Readiness(context.Background())
	require.Equal(t, "unavailable", report.Status)
	require.Contains(t, report.Failures[0].Err.Error(), "probe exploded")
}
