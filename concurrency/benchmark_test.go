package concurrency

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkResults []int

// These callbacks deliberately do little work: this measures framework overhead,
// not the throughput gain of parallel I/O or CPU-bound application operations.
func BenchmarkMap(b *testing.B) {
	inputs := make([]int, 256)
	for i := range inputs {
		inputs[i] = i
	}
	b.Run("sequential", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			results := make([]int, len(inputs))
			for i, v := range inputs {
				results[i] = v * v
			}
			benchmarkResults = results
		}
	})
	for _, workers := range []int{1, 8} {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				results, err := Map(context.Background(), inputs, workers, func(_ context.Context, v int) (int, error) { return v * v, nil })
				if err != nil {
					b.Fatal(err)
				}
				benchmarkResults = results
			}
		})
	}
}

func BenchmarkLimiter(b *testing.B) {
	for _, capacity := range []int{1, 8} {
		b.Run(fmt.Sprintf("parallel_capacity_%d", capacity), func(b *testing.B) {
			limiter, err := NewLimiter(capacity)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					release, err := limiter.Acquire(context.Background())
					if err != nil {
						b.Error(err)
						return
					}
					release()
				}
			})
			if limiter.InUse() != 0 {
				b.Fatal("permits leaked")
			}
		})
	}
}
