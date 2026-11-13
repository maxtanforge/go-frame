package logger

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type reentrantLogValue struct {
	logger *slog.Logger
	calls  *atomic.Int32
}

func TestLongConsoleMetadataAndSLSRemainLossless(t *testing.T) {
	var text, output, structured bytes.Buffer
	r := slog.NewRecord(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), slog.LevelInfo, "operation completed", 0)
	r.AddAttrs(slog.String("service", "orders"), slog.Group("event",
		slog.String("path", "/orders/42"), slog.Uint64("id", 9007199254740993),
		slog.Group("request", slog.Any("items", []string{strings.Repeat("a", 80), strings.Repeat("b", 80)}))),
		slog.String("stack", "origin\nframe"))
	require.NoError(t, buildHandler(Options{}, &text).Handle(context.Background(), r))
	require.NoError(t, buildHandler(Options{Format: "sls"}, &output).Handle(context.Background(), r))
	require.NoError(t, buildHandler(Options{Format: "json"}, &structured).Handle(context.Background(), r))
	require.Contains(t, text.String(), "[info] operation completed { service: 'orders', event: { path: '/orders/42', id: 9007199254740993, request: { items: ['")
	require.Contains(t, text.String(), "'] } } }\n  origin\n  frame\n")
	require.Equal(t, 3, strings.Count(text.String(), "\n"), "only the explicit stack may span lines")
	require.Contains(t, text.String(), strings.Repeat("a", 80))
	require.Contains(t, text.String(), strings.Repeat("b", 80))
	var content slsContent
	require.NoError(t, json.Unmarshal(output.Bytes(), &content))
	require.Equal(t, strings.TrimSuffix(text.String(), "\n"), content.Message)
	require.Equal(t, 1, strings.Count(output.String(), "\n"))
	require.Equal(t, 1, strings.Count(structured.String(), "\n"))
	require.Contains(t, structured.String(), `"id":9007199254740993`)
}

func TestLongSingleLineRecordsSurviveConcurrentRotation(t *testing.T) {
	for _, format := range []string{"console", "sls"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			r, err := Open(RuntimeOptions{Options: Options{Format: format}, File: &FileOptions{
				Path: path, MaxSizeMB: 1, MaxBackups: ptr(0), MaxAgeDays: ptr(0),
			}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Close() })
			payload := strings.Repeat("x", 70000) + "\n\t'\\"
			var wg sync.WaitGroup
			for worker := range 4 {
				wg.Go(func() {
					for sequence := range 4 {
						index := worker*4 + sequence
						r.Logger().Info(fmt.Sprintf("record-%02d", index), "event", slog.GroupValue(
							slog.Int("index", index), slog.String("payload", payload)))
					}
				})
			}
			wg.Wait()
			require.NoError(t, r.Close())
			require.Zero(t, r.ErrorCount())
			files, err := filepath.Glob(path + "*")
			require.NoError(t, err)
			require.Greater(t, len(files), 1, "exercise automatic rotation")
			seen := map[int]bool{}
			for _, file := range files {
				data, err := os.ReadFile(file)
				require.NoError(t, err)
				if strings.HasSuffix(file, ".gz") {
					archive, err := gzip.NewReader(bytes.NewReader(data))
					require.NoError(t, err)
					data, err = io.ReadAll(archive)
					require.NoError(t, err)
					require.NoError(t, archive.Close())
				}
				require.LessOrEqual(t, len(data), 1024*1024)
				for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
					if len(line) == 0 {
						continue
					}
					message := string(line)
					if format == "sls" {
						var content slsContent
						require.NoError(t, json.Unmarshal(line, &content))
						message = content.Message
					}
					require.NotContains(t, message, "\n")
					require.Contains(t, message, strings.Repeat("x", 70000))
					matches := 0
					for index := range 16 {
						if strings.Contains(message, fmt.Sprintf("[info] record-%02d {", index)) {
							require.False(t, seen[index], "duplicate record %d", index)
							require.Contains(t, message, fmt.Sprintf("index: %d, payload: '", index))
							seen[index] = true
							matches++
						}
					}
					require.Equal(t, 1, matches, "record split or interleaved")
				}
			}
			require.Len(t, seen, 16)
		})
	}
}

func TestConsoleAndSLSMessageControlCharacters(t *testing.T) {
	controls := []rune{0x7f, 0x85, 0x9b, 0x2028, 0x2029, 0x202e}
	for r := rune(0); r < 32; r++ {
		controls = append(controls, r)
	}
	for _, format := range []string{"console", "sls"} {
		t.Run(format, func(t *testing.T) {
			for _, control := range controls {
				var out bytes.Buffer
				message := "untrusted" + string(control) + "message"
				record := slog.NewRecord(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), slog.LevelInfo, message, 0)
				require.NoError(t, buildHandler(Options{Format: format}, &out).Handle(context.Background(), record))
				line := out.String()
				if format == "sls" {
					var content slsContent
					require.NoError(t, json.Unmarshal(out.Bytes(), &content))
					line = content.Message + "\n"
				}
				require.Equal(t, "[2026-10-10 12:00:00.000] [info] "+strconv.Quote(message)+"\n", line, "control U+%04X", control)
			}
		})
	}
}

func TestConsoleAndSLSStackControlCharacters(t *testing.T) {
	for _, format := range []string{"console", "sls"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			record := slog.NewRecord(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), slog.LevelError, "错误", 0)
			record.AddAttrs(slog.String("stack", "origin\\literal\\n\r\n\tframe\b\v\f\x00\x1b\u009b\u2028\n"))
			require.NoError(t, buildHandler(Options{Format: format}, &out).Handle(context.Background(), record))
			line := out.String()
			if format == "sls" {
				var content slsContent
				require.NoError(t, json.Unmarshal(out.Bytes(), &content))
				line = content.Message + "\n"
			}
			require.Equal(t, "[2026-10-10 12:00:00.000] [error] 错误\n  origin\\literal\\n\n  \tframe\\b\\v\\f\\x00\\x1b\\u009b\\u2028\n", line)
		})
	}
}

func (v reentrantLogValue) LogValue() slog.Value {
	v.calls.Add(1)
	v.logger.Info("nested log")
	return slog.StringValue("resolved")
}

type reentrantJSONValue struct {
	logger *slog.Logger
	calls  *atomic.Int32
}

func (v reentrantJSONValue) MarshalJSON() ([]byte, error) {
	v.calls.Add(1)
	v.logger.Info("nested log")
	return []byte(`"resolved"`), nil
}

func TestConsoleLogValuerCanLogRecursively(t *testing.T) {
	for _, format := range []string{"console", "sls"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			var calls atomic.Int32
			l := slog.New(buildHandler(Options{Format: format}, &output))
			done := make(chan struct{})
			go func() {
				l.Info("outer log", slog.Group("request", "detail", reentrantLogValue{logger: l, calls: &calls}))
				l.Info("outer JSON log", "detail", reentrantJSONValue{logger: l, calls: &calls})
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("LogValuer deadlocked when writing a nested log")
			}
			require.Equal(t, int32(2), calls.Load())
			require.Contains(t, output.String(), "nested log")
			require.Contains(t, output.String(), "outer log")
			require.Contains(t, output.String(), "detail: 'resolved'")
		})
	}
}

func TestConsoleAttributesAndGroups(t *testing.T) {
	var output bytes.Buffer
	h := buildHandler(Options{}, &output).WithAttrs([]slog.Attr{slog.String("service", "orders")}).WithGroup("request").WithAttrs([]slog.Attr{slog.Int("id", 42)})
	r := slog.NewRecord(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), slog.LevelInfo, "request complete", 0)
	r.AddAttrs(slog.String("msg", "custom"), slog.String("level", "custom"), slog.Group("user", "name", "a b"), slog.Attr{})
	require.NoError(t, h.Handle(context.Background(), r))
	require.Equal(t, "[2026-10-10 12:00:00.000] [info] request complete { service: 'orders', request: { id: 42, msg: 'custom', level: 'custom', user: { name: 'a b' } } }\n", output.String())
}

func TestConsoleSourceStackAndEscapedMessage(t *testing.T) {
	var output bytes.Buffer
	l := slog.New(buildHandler(Options{AddSource: true, StackOnError: true}, &output))
	l.Error("failure\nforged\x1b[31m", "error", fmt.Errorf("bad\ninput"))
	require.Greater(t, strings.Count(output.String(), "\n"), 2)
	require.Contains(t, output.String(), `"failure\nforged\x1b[31m"`)
	require.Contains(t, output.String(), "console_test.go:")
	require.Contains(t, output.String(), "\n  goroutine ")
	require.Contains(t, output.String(), `error: 'bad\ninput'`)
	require.NotContains(t, output.String(), "\x1b")
}

func TestConsoleContextIsOrdinaryMetadata(t *testing.T) {
	var output bytes.Buffer
	l := slog.New(buildHandler(Options{}, &output)).With("service", "orders").With("context", "Bootstrap")
	l.Info("starting", "context", "HTTP")
	l.WithGroup("request").Info("done", "context", "nested")
	l.Info("ready")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Contains(t, lines[0], "[info] starting")
	require.Contains(t, lines[0], "context: 'HTTP'")
	require.Contains(t, lines[1], "[info] done")
	require.Contains(t, lines[1], "request: { context: 'nested' }")
	require.Contains(t, lines[2], "[info] ready")
	require.Contains(t, lines[2], "context: 'Bootstrap'")
}

func TestConsoleMetadataValues(t *testing.T) {
	var output bytes.Buffer
	l := slog.New(buildHandler(Options{}, &output))
	l.Info("values", "id", uint64(9007199254740993), "items", []any{"a'b\"c", true, nil}, "odd key", "line\nnext", "payload", map[string]any{"valid": false})
	require.Contains(t, output.String(), "id: 9007199254740993")
	require.Contains(t, output.String(), `items: ['a\'b"c', true, null]`)
	require.Contains(t, output.String(), `'odd key': 'line\nnext'`)
	require.Contains(t, output.String(), "payload: { valid: false }")
}

func TestConsoleInheritedStackAndNestedStack(t *testing.T) {
	var output bytes.Buffer
	l := slog.New(buildHandler(Options{StackOnError: true}, &output)).With("stack", "origin\ntrace")
	l.Error("failure")
	require.Equal(t, "\n  origin\n  trace\n", output.String()[strings.Index(output.String(), "\n"):])
	require.NotContains(t, output.String(), "stack:")
	output.Reset()
	l.WithGroup("request").Error("nested", "stack", "detail\ntrace")
	require.Contains(t, output.String(), `request: { stack: 'detail\ntrace' }`)
	require.Contains(t, output.String(), "\n  origin\n  trace\n")
}

func TestConsoleTerminalColorSink(t *testing.T) {
	for _, tc := range []struct{ level, color string }{{"info", "32"}, {"warn", "33"}, {"error", "31"}, {"debug", "34"}} {
		var output bytes.Buffer
		w := consoleColorWriter{writer: &output}
		line := []byte("[2026-10-10 12:00:00.000] [" + tc.level + "] hello\n")
		n, err := w.Write(line)
		require.NoError(t, err)
		require.Equal(t, len(line), n)
		require.Equal(t, "[2026-10-10 12:00:00.000] \x1b["+tc.color+"m["+tc.level+"] hello\x1b[0m\n", output.String())
	}
	w := consoleColorWriter{writer: brokenWriter{short: true}}
	_, err := w.Write([]byte("[time] [error] failure\n"))
	require.ErrorIs(t, err, io.ErrShortWrite)
}

func TestRuntimeBothConsoleSinks(t *testing.T) {
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = old; _ = f.Close() })
	path := filepath.Join(t.TempDir(), "app.log")
	r, err := Open(RuntimeOptions{File: &FileOptions{Path: path}, Stdout: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Logger().Error("failure", "stack", "origin\ntrace")
	require.NoError(t, r.Close())
	file, err := os.ReadFile(path)
	require.NoError(t, err)
	console, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	require.Equal(t, file, console)
	require.NotContains(t, string(file), "\x1b")
	require.Contains(t, string(file), "[error] failure\n  origin\n  trace\n")
}

func TestNewUnknownFormatWarnsAndUsesConsole(t *testing.T) {
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = old; _ = f.Close() })
	l := New(Options{Format: "yaml"})
	l.Info("ready")
	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	require.Contains(t, string(data), "[warn] logger: unknown format, using console { format: 'yaml' }")
	require.Contains(t, string(data), "[info] ready")
}

func TestRuntimeFormatsAndConcurrentChildren(t *testing.T) {
	for _, format := range []string{"", "console", "json", "sls"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			r, err := Open(RuntimeOptions{Options: Options{Format: format}, File: &FileOptions{Path: path}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Close() })
			child := r.Logger().With("service", "orders").WithGroup("request")
			child.Debug("hidden")
			require.NoError(t, r.SetLevel("debug"))
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					for j := 0; j < 25; j++ {
						child.With("worker", i).Debug("complete", "index", j)
					}
				}(i)
			}
			wg.Wait()
			require.NoError(t, r.Close())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
			require.Len(t, lines, 200)
			for _, line := range lines {
				if format == "json" {
					var record map[string]any
					require.NoError(t, json.Unmarshal(line, &record))
					require.Equal(t, "DEBUG", record["level"])
					require.Equal(t, "complete", record["msg"])
					require.Equal(t, "orders", record["service"])
					require.Len(t, record["request"], 2)
				} else if format == "sls" {
					var content slsContent
					require.NoError(t, json.Unmarshal(line, &content))
					require.Equal(t, "DEBUG", content.Level)
					require.Contains(t, content.Message, "[debug] complete { service: 'orders', request: { worker: ")
					require.Contains(t, content.Message, ", index: ")
				} else {
					require.Contains(t, string(line), "[debug] complete { service: 'orders', request: { worker: ")
					require.Contains(t, string(line), ", index: ")
				}
			}
			require.Zero(t, r.ErrorCount())
		})
	}
}

func TestInvalidFormatDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "app.log")
	r, err := Open(RuntimeOptions{Options: Options{Format: "yaml"}, File: &FileOptions{Path: path}})
	require.Error(t, err)
	require.Nil(t, r)
	require.NoDirExists(t, filepath.Dir(path))
}

func TestConsoleShortWrite(t *testing.T) {
	h := buildHandler(Options{}, brokenWriter{short: true})
	require.Error(t, h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)))
}
