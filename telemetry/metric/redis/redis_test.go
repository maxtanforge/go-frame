package metricredis

import (
	"testing"

	"github.com/maxtanforge/go-frame/telemetry/metric"
	"github.com/redis/go-redis/v9"
)

func TestPoolSamplingDoesNotDial(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "invalid.local:0", PoolSize: 7})
	t.Cleanup(func() { _ = client.Close() })
	r, err := metric.NewResourceRecorder(Pool("cache", client))
	if err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot().Pools["cache"]
	if s.Open != 0 || s.Max != 7 || s.WaitCount != 0 {
		t.Fatalf("%+v", s)
	}
	if _, err := metric.NewResourceRecorder(Pool("nil", nil)); err == nil {
		t.Fatal("nil accepted")
	}
}
