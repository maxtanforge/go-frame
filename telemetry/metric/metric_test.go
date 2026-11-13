package metric

import (
	"context"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistogramCountsCumulativeBuckets(t *testing.T) {
	h, err := NewHistogram([]float64{10, 50, 100})
	require.NoError(t, err)
	for _, v := range []float64{5, 10, 49, 100, 900} {
		if err := h.Observe(v); err != nil {
			t.Error(err)
			return
		}
	}
	s := h.Snapshot()
	require.Equal(t, []float64{10, 50, 100}, []float64{s.Buckets[0].UpperBound, s.Buckets[1].UpperBound, s.Buckets[2].UpperBound})
	require.Equal(t, []uint64{2, 3, 4}, []uint64{s.Buckets[0].Count, s.Buckets[1].Count, s.Buckets[2].Count}, "桶计数必须累计")
	require.Equal(t, uint64(5), s.Count)
	require.InDelta(t, 5+10+49+100+900, s.Sum, 1e-9)
	s.Buckets[0].Count = 99
	require.Equal(t, uint64(2), h.Snapshot().Buckets[0].Count, "快照必须是独立拷贝")
}

func TestHistogramRejectsInvalidBuckets(t *testing.T) {
	for _, buckets := range [][]float64{nil, {50, 10}, {10, 10}, {math.NaN()}, {-1}} {
		_, err := NewHistogram(buckets)
		require.Error(t, err, "%v", buckets)
	}
}

func TestRequestRecorderKeysAndOverflow(t *testing.T) {
	r, err := NewRequestRecorder(Options{})
	require.NoError(t, err)
	if err := r.Observe(context.Background(), Observation{Route: "/orders", Method: "GET", DurationMS: 12}); err != nil {
		t.Error(err)
		return
	}
	if err := r.Observe(context.Background(), Observation{Route: "/orders", Method: "GET", DurationMS: 30}); err != nil {
		t.Error(err)
		return
	}
	if err := r.Observe(context.Background(), Observation{Route: "/orders", Method: "POST", DurationMS: 5}); err != nil {
		t.Error(err)
		return
	}
	for i := 0; i < 600; i++ {
		if err := r.Observe(context.Background(), Observation{Route: "/bulk/" + strconv.Itoa(i), Method: "GET", DurationMS: 1}); err != nil {
			t.Error(err)
			return
		}
	}
	snapshot := r.Snapshot()
	named, other := 0, uint64(0)
	for key, hist := range snapshot {
		if key.Route == "_OTHER" {
			other = hist.Count
			continue
		}
		named++
	}
	require.LessOrEqual(t, named, MaxKeys, "键数不得超上限")
	require.Greater(t, other, uint64(80), "超限观测必须折叠到 _OTHER")
	require.Contains(t, snapshot, RequestKey{Route: "/orders", Method: "GET"})
	orders := snapshot[RequestKey{Route: "/orders", Method: "GET"}]
	require.Equal(t, uint64(2), orders.Count)
	require.InDelta(t, 42.0, orders.Sum, 1e-9)
	require.Equal(t, uint64(1), snapshot[RequestKey{Route: "/orders", Method: "POST"}].Count)
}

func TestRequestRecorderObserveHook(t *testing.T) {
	seen := make(chan [3]any, 4)
	r, err := NewRequestRecorder(Options{OnObserve: func(_ context.Context, o Observation) {
		seen <- [3]any{o.Route, o.Method, o.DurationMS}
	}})
	require.NoError(t, err)
	if err := r.Observe(context.Background(), Observation{Route: "/x", Method: "GET", DurationMS: 7}); err != nil {
		t.Error(err)
		return
	}
	observed := <-seen
	require.Equal(t, "/x", observed[0])
	require.Equal(t, "GET", observed[1])
	require.Equal(t, 7.0, observed[2])
}

func TestRecorderConcurrentObserve(t *testing.T) {
	r, _ := NewRequestRecorder(Options{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if err := r.Observe(context.Background(), Observation{Route: "/orders", Method: "GET", DurationMS: float64(i % 50)}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	total := uint64(0)
	for _, hist := range r.Snapshot() {
		total += hist.Count
	}
	require.Equal(t, uint64(4000), total)
}

func TestPrometheusExposition(t *testing.T) {
	r, err := NewRequestRecorder(Options{Buckets: []float64{100}})
	require.NoError(t, err)
	if err := r.Observe(context.Background(), Observation{Route: "/orders", Method: "GET", DurationMS: 5}); err != nil {
		t.Error(err)
		return
	}
	if err := r.Observe(context.Background(), Observation{Route: "/orders", Method: "GET", DurationMS: 150}); err != nil {
		t.Error(err)
		return
	}
	body := r.Prometheus()
	require.Contains(t, body, "# TYPE http_request_duration_milliseconds histogram")
	require.Contains(t, body, `http_request_duration_milliseconds_bucket{method="GET",route="/orders",le="100"} 1`)
	require.Contains(t, body, `http_request_duration_milliseconds_bucket{method="GET",route="/orders",le="+Inf"} 2`)
	require.Contains(t, body, `http_request_duration_milliseconds_count{method="GET",route="/orders"} 2`)
	require.Contains(t, body, `http_request_duration_milliseconds_sum{method="GET",route="/orders"} 155`)
	require.Regexp(t, `(?m)# HELP http_request_duration_milliseconds .+`, body)

	// 键按 (route, method) 排序输出,标签值转义反斜杠与引号。
	if err := r.Observe(context.Background(), Observation{Route: `/a"b`, Method: "GET", DurationMS: 1}); err != nil {
		t.Error(err)
		return
	}
	full := r.Prometheus()
	require.Contains(t, full, `route="/a\"b"`)
	require.Less(t, strings.Index(full, `route="/a\"b"`), strings.Index(full, `route="/orders"`), "键必须有序")
}
