package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

func benchmarkRuntime(b *testing.B, compress bool) *Runtime {
	b.Helper()
	backups, age := 1, 0
	r, err := Open(RuntimeOptions{Options: Options{Format: "json"}, File: &FileOptions{
		Path: filepath.Join(b.TempDir(), "service.log"), MaxSizeMB: 2,
		MaxBackups: &backups, MaxAgeDays: &age, Compress: &compress,
	}})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := r.Close(); err != nil {
			b.Error(err)
		}
	})
	return r
}

func BenchmarkLogWrite(b *testing.B) {
	for _, sink := range []string{"discard", "file"} {
		b.Run(sink, func(b *testing.B) {
			lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
			var owned *Runtime
			if sink == "file" {
				owned = benchmarkRuntime(b, false)
				lg = owned.Logger()
			}
			// Exclude one-time time-zone and JSON-handler initialization.
			lg.LogAttrs(context.Background(), slog.LevelInfo, "warmup", slog.Int("status", 200))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					lg.LogAttrs(context.Background(), slog.LevelInfo, "request complete", slog.String("route", "/items/{id}"), slog.Int("status", 200))
				}
			})
			if owned != nil && owned.ErrorCount() != 0 {
				b.Fatal("log write failed")
			}
		})
	}
}

// Measures rotation plus completed maintenance of a fixed 64 KiB archive.
// Fixture writing is excluded. Retention bounds disk usage even on long runs.
func BenchmarkLogRotate(b *testing.B) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	for _, compress := range []bool{false, true} {
		b.Run(fmt.Sprintf("compress_%t", compress), func(b *testing.B) {
			r := benchmarkRuntime(b, compress)
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				if _, err := r.write(payload); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := r.Rotate(); err != nil {
					b.Fatal(err)
				}
				waitMaintenance(r)
			}
		})
	}
}

// Isolates maintenance of an empty active file and its otherwise empty directory.
// No archives are created; this measures scanning and matching setup overhead.
func BenchmarkLogMaintenance(b *testing.B) {
	r := benchmarkRuntime(b, false)
	b.ReportAllocs()
	for b.Loop() {
		if err := r.Rotate(); err != nil {
			b.Fatal(err)
		}
		waitMaintenance(r)
	}
}

func BenchmarkErrorStack(b *testing.B) {
	lg := slog.New(buildHandler(Options{StackOnError: true}, io.Discard))
	b.ReportAllocs()
	for b.Loop() {
		lg.Error("failure")
	}
}
