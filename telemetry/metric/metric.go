// Package metric provides fixed-bucket request-latency histograms with
// dependency-free exports. It stays decoupled from net/http: the caller wires
// its middleware observer (route template, standard method, duration) into
// RequestRecorder.Observe.
package metric

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// DefaultBuckets returns ascending millisecond latency bounds; +Inf is implicit.
func DefaultBuckets() []float64 {
	return []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}
}

// MaxKeys bounds total route/method cardinality including the shared overflow key.
const MaxKeys = 512

// Options configures a request recorder. Zero values select the defaults.
type Options struct {
	// Buckets are ascending observation bounds in the caller's unit
	// (milliseconds by convention); +Inf is implicit.
	Buckets []float64
	// OnObserve receives every observation after recording; export adapters
	// subscribe here. It must be fast and concurrency-safe.
	OnObserve func(context.Context, Observation)
}

// Bucket is one cumulative histogram bucket.
type Bucket struct {
	UpperBound float64
	Count      uint64
}

// HistogramSnapshot is an independent copy of one histogram's state.
type HistogramSnapshot struct {
	Buckets []Bucket
	Count   uint64
	Sum     float64
}

// RequestKey identifies one (route template, standard method) histogram.
type RequestKey struct {
	Route  string
	Method string
}

// Observation contains a route template, method and latency in milliseconds.
type Observation struct {
	Route      string
	Method     string
	DurationMS float64
}

// Histogram is a fixed-bucket cumulative histogram safe for concurrent use.
type Histogram struct {
	mu     sync.RWMutex
	upper  []float64
	counts []uint64
	count  uint64
	sum    float64
}

// NewHistogram validates ascending, finite, non-negative bounds.
func NewHistogram(upper []float64) (*Histogram, error) {
	if len(upper) == 0 {
		return nil, errors.New("metric: buckets must not be empty")
	}
	for i, v := range upper {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || (i > 0 && v <= upper[i-1]) {
			return nil, errors.New("metric: buckets must be ascending and finite")
		}
	}
	return &Histogram{upper: append([]float64(nil), upper...), counts: make([]uint64, len(upper))}, nil
}

func validDuration(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

// Observe records a finite non-negative value. Rejected observations leave the histogram unchanged.
func (h *Histogram) Observe(v float64) error {
	if !validDuration(v) {
		return errors.New("metric: duration must be finite and non-negative")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == math.MaxUint64 || math.IsInf(h.sum+v, 0) {
		return errors.New("metric: histogram capacity exceeded")
	}
	for i, bound := range h.upper {
		if v <= bound {
			h.counts[i]++
		}
	}
	h.count++
	h.sum += v
	return nil
}

// Snapshot returns a coherent independent copy of the histogram.
func (h *Histogram) Snapshot() HistogramSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s := HistogramSnapshot{Buckets: make([]Bucket, len(h.upper)), Count: h.count, Sum: h.sum}
	for i := range h.upper {
		s.Buckets[i] = Bucket{UpperBound: h.upper[i], Count: h.counts[i]}
	}
	return s
}

// RequestRecorder keeps bounded route/method histograms; it must not be copied.
type RequestRecorder struct {
	mu        sync.RWMutex
	onObserve func(context.Context, Observation)
	bounds    []float64
	byKey     map[RequestKey]*Histogram
}

// NewRequestRecorder validates and copies configuration.
func NewRequestRecorder(o Options) (*RequestRecorder, error) {
	buckets := o.Buckets
	if buckets == nil {
		buckets = DefaultBuckets()
	}
	h, err := NewHistogram(buckets)
	if err != nil {
		return nil, err
	}
	return &RequestRecorder{onObserve: o.OnObserve, bounds: h.upper, byKey: map[RequestKey]*Histogram{}}, nil
}

func normalizeKey(o Observation) RequestKey {
	method := o.Method
	switch method {
	case "GET", "HEAD", "POST", "PUT", "DELETE", "CONNECT", "OPTIONS", "TRACE", "PATCH":
	default:
		method = "_OTHER"
	}
	route := o.Route
	if route == "" || len(route) > 2048 || !utf8.ValidString(route) || strings.IndexFunc(route, unicode.IsControl) >= 0 {
		route = "_OTHER"
	}
	return RequestKey{Route: route, Method: method}
}

// Observe records locally and invokes the adapter with the same normalized key.
// Canceled contexts are accepted so completed/canceled requests remain observable.
// One slot is reserved for the shared overflow key; total cardinality never exceeds MaxKeys.
func (r *RequestRecorder) Observe(ctx context.Context, o Observation) error {
	if ctx == nil {
		return errors.New("metric: context must not be nil")
	}
	if !validDuration(o.DurationMS) {
		return errors.New("metric: duration must be finite and non-negative")
	}
	key := normalizeKey(o)
	r.mu.Lock()
	h := r.byKey[key]
	if h == nil {
		if len(r.byKey) >= MaxKeys-1 {
			key = RequestKey{Route: "_OTHER", Method: "_OTHER"}
			h = r.byKey[key]
		}
		if h == nil {
			h = &Histogram{upper: r.bounds, counts: make([]uint64, len(r.bounds))}
			r.byKey[key] = h
		}
	}
	r.mu.Unlock()
	if err := h.Observe(o.DurationMS); err != nil {
		return err
	}
	o.Route, o.Method = key.Route, key.Method
	if r.onObserve != nil {
		r.onObserve(ctx, o)
	}
	return nil
}

// Snapshot returns independent copies of every key's histogram.
func (r *RequestRecorder) Snapshot() map[RequestKey]HistogramSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[RequestKey]HistogramSnapshot, len(r.byKey))
	for key, h := range r.byKey {
		out[key] = h.Snapshot()
	}
	return out
}

// Prometheus renders the recorder as Prometheus text-format exposition: one
// histogram family named http_request_duration_milliseconds, keys sorted by
// (route, method), label values escaped. The caller serves it on its own
// endpoint; this package never listens.
func (r *RequestRecorder) Prometheus() string {
	snapshot := r.Snapshot()
	keys := make([]RequestKey, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Route != keys[j].Route {
			return keys[i].Route < keys[j].Route
		}
		return keys[i].Method < keys[j].Method
	})
	var b strings.Builder
	b.WriteString("# HELP http_request_duration_milliseconds Request duration in milliseconds by route template and standard method.\n")
	b.WriteString("# TYPE http_request_duration_milliseconds histogram\n")
	for _, key := range keys {
		s := snapshot[key]
		labels := `method="` + escapeLabel(key.Method) + `",route="` + escapeLabel(key.Route) + `"`
		for _, bucket := range s.Buckets {
			b.WriteString("http_request_duration_milliseconds_bucket{" + labels + ",le=\"" + formatValue(bucket.UpperBound) + "\"} " + strconv.FormatUint(bucket.Count, 10) + "\n")
		}
		b.WriteString("http_request_duration_milliseconds_bucket{" + labels + ",le=\"+Inf\"} " + strconv.FormatUint(s.Count, 10) + "\n")
		b.WriteString("http_request_duration_milliseconds_sum{" + labels + "} " + formatValue(s.Sum) + "\n")
		b.WriteString("http_request_duration_milliseconds_count{" + labels + "} " + strconv.FormatUint(s.Count, 10) + "\n")
	}
	return b.String()
}

func formatValue(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func escapeLabel(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(v)
}
