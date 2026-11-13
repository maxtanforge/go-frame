package metric

import (
	"database/sql"
	"errors"
	"fmt"
	"runtime/metrics"
	"sort"
	"strings"
)

// PoolSnapshot contains connection counts and cumulative wait statistics.
// A callback returns locally available statistics; it must not perform network I/O.
type PoolSnapshot struct {
	Open, InUse, Idle, Max int64
	WaitCount              uint64
	WaitSeconds            float64
}

// PoolSource registers one stable, bounded pool name. Snapshot must be concurrency-safe.
type PoolSource struct {
	Name     string
	Snapshot func() PoolSnapshot
}

// SQLPool adapts database/sql statistics without taking ownership of the database.
func SQLPool(name string, db *sql.DB) PoolSource {
	if db == nil {
		return PoolSource{Name: name}
	}
	return PoolSource{Name: name, Snapshot: func() PoolSnapshot {
		s := db.Stats()
		return PoolSnapshot{Open: int64(s.OpenConnections), InUse: int64(s.InUse), Idle: int64(s.Idle), Max: int64(s.MaxOpenConnections), WaitCount: uint64(s.WaitCount), WaitSeconds: s.WaitDuration.Seconds()}
	}}
}

// ResourceRecorder samples Go runtime, current process and registered pools on demand.
// It owns no resources and starts no goroutines. It must not be copied.
type ResourceRecorder struct{ pools []PoolSource }

// NewResourceRecorder copies and validates at most 32 stable pool declarations.
func NewResourceRecorder(pools ...PoolSource) (*ResourceRecorder, error) {
	if len(pools) > 32 {
		return nil, errors.New("metric: too many pools")
	}
	seen := map[string]bool{}
	for _, p := range pools {
		if p.Name == "" || len(p.Name) > 64 || p.Snapshot == nil || seen[p.Name] {
			return nil, errors.New("metric: invalid pool declaration")
		}
		for _, c := range p.Name {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
				return nil, errors.New("metric: invalid pool name")
			}
		}
		seen[p.Name] = true
	}
	copyPools := append([]PoolSource(nil), pools...)
	sort.Slice(copyPools, func(i, j int) bool { return copyPools[i].Name < copyPools[j].Name })
	return &ResourceRecorder{pools: copyPools}, nil
}

// ResourceSnapshot holds one independent scrape. CPU is cumulative process CPU
// seconds, not a percentage of host or container capacity. ProcessError leaves
// process values unavailable while runtime/pool statistics remain usable.
type ResourceSnapshot struct {
	HeapBytes, HeapAllocatedBytes, GCCycles, Goroutines uint64
	ProcessCPUSeconds                                   float64
	ProcessResidentBytes                                uint64
	ProcessError                                        error
	Pools                                               map[string]PoolSnapshot
}

// Snapshot reads current counters without polling dependencies or creating timers.
func (r *ResourceRecorder) Snapshot() ResourceSnapshot {
	samples := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/gc/heap/allocs:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}, {Name: "/sched/goroutines:goroutines"}}
	metrics.Read(samples)
	s := ResourceSnapshot{HeapBytes: samples[0].Value.Uint64(), HeapAllocatedBytes: samples[1].Value.Uint64(), GCCycles: samples[2].Value.Uint64(), Goroutines: samples[3].Value.Uint64(), Pools: make(map[string]PoolSnapshot, len(r.pools))}
	s.ProcessCPUSeconds, s.ProcessResidentBytes, s.ProcessError = processResources()
	for _, p := range r.pools {
		s.Pools[p.Name] = p.Snapshot()
	}
	return s
}

// Prometheus renders a scrape. Unavailable process data is omitted and explicitly
// marked by process_resources_available=0. Serve only on a protected metrics endpoint.
func (r *ResourceRecorder) Prometheus() string {
	s := r.Snapshot()
	var b strings.Builder
	write := func(name, kind string, value any) { fmt.Fprintf(&b, "# TYPE %s %s\n%s %v\n", name, kind, name, value) }
	write("go_heap_bytes", "gauge", s.HeapBytes)
	write("go_heap_allocated_bytes_total", "counter", s.HeapAllocatedBytes)
	write("go_gc_cycles_total", "counter", s.GCCycles)
	write("go_goroutines", "gauge", s.Goroutines)
	available := 0
	if s.ProcessError == nil {
		available = 1
		write("process_cpu_seconds_total", "counter", s.ProcessCPUSeconds)
		write("process_resident_memory_bytes", "gauge", s.ProcessResidentBytes)
	}
	write("process_resources_available", "gauge", available)
	for _, field := range []struct {
		name, kind string
		value      func(PoolSnapshot) any
	}{
		{"connections", "gauge", func(p PoolSnapshot) any { return p.Open }},
		{"in_use_connections", "gauge", func(p PoolSnapshot) any { return p.InUse }},
		{"idle_connections", "gauge", func(p PoolSnapshot) any { return p.Idle }},
		{"max_connections", "gauge", func(p PoolSnapshot) any { return p.Max }},
		{"wait_total", "counter", func(p PoolSnapshot) any { return p.WaitCount }},
		{"wait_seconds_total", "counter", func(p PoolSnapshot) any { return p.WaitSeconds }},
	} {
		name := "pool_" + field.name
		fmt.Fprintf(&b, "# TYPE %s %s\n", name, field.kind)
		for _, p := range r.pools {
			fmt.Fprintf(&b, "%s{pool=%q} %v\n", name, p.Name, field.value(s.Pools[p.Name]))
		}
	}
	return b.String()
}
