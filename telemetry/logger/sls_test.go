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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSLSMatchesFileMessage(t *testing.T) {
	var text, output bytes.Buffer
	opts := Options{Timezone: "Asia/Shanghai", Environment: "test"}
	console := buildHandler(opts, &text)
	opts.Format = "sls"
	sls := buildHandler(opts, &output)
	attrs := []slog.Attr{slog.String("service", "orders"), slog.String("env", "user-value"), slog.String("pid", "user-value")}
	console, sls = console.WithAttrs(attrs).WithGroup("request"), sls.WithAttrs(attrs).WithGroup("request")
	record := slog.NewRecord(time.Date(2026, 10, 10, 12, 0, 0, 123000000, time.UTC), slog.LevelError, "failed", 0)
	record.AddAttrs(slog.Int("status", 500), slog.String("stack", "nested\nstack"))
	require.NoError(t, console.Handle(context.Background(), record))
	require.NoError(t, sls.Handle(context.Background(), record))
	var content map[string]string
	require.NoError(t, json.Unmarshal(output.Bytes(), &content))
	require.Len(t, content, 4)
	require.Equal(t, strings.TrimSuffix(text.String(), "\n"), content["message"])
	require.Contains(t, content["message"], "[2026-10-10 20:00:00.123] [error] failed")
	require.Equal(t, "test", content["env"])
	require.Equal(t, "ERROR", content["level"])
	require.Equal(t, strconv.Itoa(os.Getpid()), content["pid"])
	require.NotContains(t, output.String(), "\x1b")
}

func TestSLSEscapesStackAndUsesDefaultEnvironment(t *testing.T) {
	var output bytes.Buffer
	l := slog.New(buildHandler(Options{Format: "SLS", StackOnError: true}, &output)).With("stack", "origin\ntrace")
	l.Error("failure", "message", "user-value", "level", "user-value")
	var content slsContent
	require.NoError(t, json.Unmarshal(output.Bytes(), &content))
	require.Equal(t, "production", content.Env)
	require.Equal(t, "ERROR", content.Level)
	require.Contains(t, content.Message, "[error] failure")
	require.Contains(t, content.Message, "\n  origin\n  trace")
	require.Contains(t, content.Message, "message: 'user-value'")
	require.Equal(t, 1, strings.Count(output.String(), "\n"))
}

func TestFormatsShareConfiguredTimezone(t *testing.T) {
	for _, format := range []string{"console", "json", "sls"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			h := buildHandler(Options{Format: format, Timezone: "America/Chicago"}, &output).WithGroup("request").WithAttrs([]slog.Attr{slog.String("id", "42")})
			record := slog.NewRecord(time.Date(2026, 10, 10, 12, 0, 0, 123000000, time.UTC), slog.LevelInfo, "ready", 0)
			require.NoError(t, h.Handle(context.Background(), record))
			if format == "json" {
				require.Contains(t, output.String(), `"time":"2026-10-10T07:00:00.123-05:00"`)
			} else {
				require.Contains(t, output.String(), "[2026-10-10 07:00:00.123] [info]")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "not-created", "app.log")
	r, err := Open(RuntimeOptions{Options: Options{Timezone: "missing/zone"}, File: &FileOptions{Path: path}})
	require.Error(t, err)
	require.Nil(t, r)
	require.NoDirExists(t, filepath.Dir(path))
}

func TestConsoleAndSLSLosslessAutomaticRotation(t *testing.T) {
	for _, format := range []string{"console", "sls"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			r, err := Open(RuntimeOptions{Options: Options{Format: format}, File: &FileOptions{Path: path, MaxSizeMB: 1, MaxBackups: ptr(0), MaxAgeDays: ptr(0)}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Close() })
			for i := 0; i < 2500; i++ {
				r.Logger().Error(fmt.Sprintf("entry_%06d", i), "payload", strings.Repeat("x", 800), "stack", "origin\ntrace")
			}
			require.NoError(t, r.Close())
			files, err := filepath.Glob(path + "*")
			require.NoError(t, err)
			require.Greater(t, len(files), 2, "size-based rotation must run without Rotate()")
			seen := map[int]bool{}
			marker := regexp.MustCompile(`entry_([0-9]{6})`)
			archives := 0
			for _, file := range files {
				data, err := os.ReadFile(file)
				require.NoError(t, err)
				if strings.HasSuffix(file, ".gz") {
					archives++
					z, err := gzip.NewReader(bytes.NewReader(data))
					require.NoError(t, err)
					data, err = io.ReadAll(z)
					require.NoError(t, err)
					require.NoError(t, z.Close())
				}
				require.LessOrEqual(t, len(data), 1<<20)
				for _, match := range marker.FindAllSubmatch(data, -1) {
					index, err := strconv.Atoi(string(match[1]))
					require.NoError(t, err)
					require.False(t, seen[index], "duplicate record")
					seen[index] = true
				}
				if format == "sls" {
					for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
						var content slsContent
						require.NoError(t, json.Unmarshal(line, &content))
						require.Contains(t, content.Message, "\n  origin\n  trace")
					}
				} else {
					require.Equal(t, len(marker.FindAll(data, -1)), bytes.Count(data, []byte("\n  origin\n  trace\n")), "stack blocks must stay with their records")
				}
			}
			require.Positive(t, archives)
			require.Len(t, seen, 2500)
			require.Zero(t, r.ErrorCount())
		})
	}
}
