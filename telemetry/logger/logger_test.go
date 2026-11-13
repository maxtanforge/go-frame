package logger

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUnknownLevelFallsBackToInfo(t *testing.T) {
	l := New(Options{Level: "verbose-nonsense"})
	require.NotNil(t, l)
	assert.False(t, l.Enabled(context.Background(), slog.LevelDebug), "未知 level 不应放行 debug")
	assert.True(t, l.Enabled(context.Background(), slog.LevelInfo), "未知 level 应放行 info")
}

func TestNewKnownLevels(t *testing.T) {
	for _, c := range []struct {
		level string
		debug bool
	}{
		{"debug", true},
		{"warn", false},
		{"error", false},
	} {
		l := New(Options{Level: c.level})
		assert.Equal(t, c.debug, l.Enabled(context.Background(), slog.LevelDebug), "level=%s", c.level)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, slog.Default(), FromContext(ctx), "缺省应返回 slog.Default()")

	l := New(Options{Level: "debug"})
	ctx = IntoContext(ctx, l)
	got := FromContext(ctx)
	assert.Same(t, l, got)
}

// capture 构造一个写入 buffer 的 Options logger,返回 buffer(两行:Info + Error)。
func capture(t *testing.T, opts Options) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	l := slog.New(buildHandler(opts, &buf))
	l.Info("hello", "k", "v")
	l.Error("boom")
	return &buf
}

func TestStackOnError(t *testing.T) {
	buf := capture(t, Options{Format: "json", Level: "info", StackOnError: true})
	out := buf.String()
	assert.Contains(t, out, `"msg":"boom"`)
	assert.Contains(t, out, `"stack"`)
	assert.NotContains(t, strings.Split(out, "\n")[0], `"stack"`) // info 行不带
}

func TestStackOffByDefault(t *testing.T) {
	buf := capture(t, Options{Format: "json", Level: "info"})
	assert.NotContains(t, buf.String(), `"stack"`)
}

func TestAddSource(t *testing.T) {
	buf := capture(t, Options{Format: "json", Level: "info", AddSource: true})
	assert.Contains(t, buf.String(), `"source"`)
}

func TestErrAttrs(t *testing.T) {
	assert.Nil(t, ErrAttrs(nil))
	attrs := ErrAttrs(errors.New("db down"))
	require.Len(t, attrs, 2)
	assert.Equal(t, "error_name", attrs[0].Key)
	assert.Equal(t, "*errors.errorString", attrs[0].Value.String())
	assert.Equal(t, "error_message", attrs[1].Key)
	assert.Equal(t, "db down", attrs[1].Value.String())
}

func TestNewWarnsOnUnknownLevel(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	New(Options{Format: "json", Level: "warnn"})
	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "logger: unknown level, using info")
	assert.Contains(t, buf.String(), `"level":"warnn"`)
}
