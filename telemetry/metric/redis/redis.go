// Package metricredis adapts Redis client pool statistics without network I/O.
package metricredis

import (
	"github.com/maxtanforge/go-frame/telemetry/metric"
	"github.com/redis/go-redis/v9"
)

// Pool borrows a single-node client. Counts exclude PubSub and dedicated pipeline
// pools. Max is the configured PoolSize target, not a hard driver connection cap.
func Pool(name string, client *redis.Client) metric.PoolSource {
	if client == nil {
		return metric.PoolSource{Name: name}
	}
	return metric.PoolSource{Name: name, Snapshot: func() metric.PoolSnapshot {
		s := client.PoolStats()
		return metric.PoolSnapshot{Open: int64(s.TotalConns), Idle: int64(s.IdleConns), InUse: max(0, int64(s.TotalConns)-int64(s.IdleConns)), Max: int64(client.Options().PoolSize), WaitCount: uint64(s.WaitCount), WaitSeconds: float64(s.WaitDurationNs) / 1e9}
	}}
}
