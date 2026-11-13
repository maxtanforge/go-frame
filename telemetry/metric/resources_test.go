package metric

import (
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestResourcesSampleLiveProcessAndBoundedPools(t *testing.T) {
	r, err := NewResourceRecorder(PoolSource{Name: "sql-main", Snapshot: func() PoolSnapshot {
		return PoolSnapshot{Open: 3, InUse: 1, Idle: 2, Max: 5, WaitCount: 4, WaitSeconds: .125}
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if s.Goroutines == 0 || s.HeapBytes == 0 || s.Pools["sql-main"].WaitSeconds != .125 {
		t.Fatalf("%+v", s)
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		if s.ProcessError != nil || s.ProcessResidentBytes == 0 || s.ProcessCPUSeconds < 0 {
			t.Fatalf("%+v", s)
		}
	}
	text := r.Prometheus()
	for _, expected := range []string{"# TYPE process_resources_available gauge", `pool_wait_seconds_total{pool="sql-main"} 0.125`, `pool_connections{pool="sql-main"} 3`, "# TYPE go_heap_allocated_bytes_total counter"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in %s", expected, text)
		}
	}
	s.Pools["sql-main"] = PoolSnapshot{}
	if r.Snapshot().Pools["sql-main"].Open != 3 {
		t.Fatal("snapshot aliases source")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				_ = r.Prometheus()
			}
		})
	}
	wg.Wait()
}

func TestResourcesRejectInvalidDeclarations(t *testing.T) {
	valid := PoolSource{Name: "main", Snapshot: func() PoolSnapshot { return PoolSnapshot{} }}
	for _, pools := range [][]PoolSource{{{Name: "missing"}}, {valid, valid}, {{Name: "bad\nlabel", Snapshot: valid.Snapshot}}, make([]PoolSource, 33), {SQLPool("nil", nil)}} {
		if _, err := NewResourceRecorder(pools...); err == nil {
			t.Fatal("invalid pool accepted")
		}
	}
}
