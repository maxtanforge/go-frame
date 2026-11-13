package pagination

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestWindowBounds(t *testing.T) {
	w, err := NewWindow(3, 20, DefaultLimits())
	if err != nil || w.Current() != 3 || w.Size() != 20 || w.Offset() != 40 || w.FetchSize() != 21 {
		t.Fatalf("window: %+v %v", w, err)
	}
	for _, tc := range []struct {
		current, size int64
		limits        Limits
	}{
		{0, 20, DefaultLimits()}, {1, 0, DefaultLimits()}, {-1, 20, DefaultLimits()}, {1, -1, DefaultLimits()},
		{1, 101, DefaultLimits()}, {10002, 100, DefaultLimits()},
		{math.MaxInt64, 2, Limits{MaxSize: 2, MaxOffset: math.MaxInt64}},
		{1, 1, Limits{}}, {1, 1, Limits{MaxSize: math.MaxInt64, MaxOffset: 0}}, {1, 1, Limits{MaxSize: 1, MaxOffset: -1}},
	} {
		if _, err := NewWindow(tc.current, tc.size, tc.limits); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted bad bounds: %+v %v", tc, err)
		}
	}
	if err := (Window{}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero window accepted")
	}
	if _, err := NewWindow(10001, 100, DefaultLimits()); err != nil {
		t.Fatal("inclusive offset limit", err)
	}
	if _, err := NewWindow(math.MaxInt64, 1, Limits{MaxSize: 1, MaxOffset: math.MaxInt64}); err != nil {
		t.Fatal("safe large window", err)
	}
}

func TestSliceProbeAndWireContract(t *testing.T) {
	w, _ := NewWindow(2, 2, DefaultLimits())
	rows := []string{"a", "b", "probe"}
	p, err := NewSlice(rows, w)
	if err != nil || !p.HasMore || len(p.Records) != 2 || p.Current != 2 || p.Size != 2 {
		t.Fatalf("slice: %+v %v", p, err)
	}
	rows[0] = "changed"
	if p.Records[0] != "a" {
		t.Fatal("record backing slice retained")
	}
	for _, count := range []int{0, 1, 2} {
		p, err := NewSlice(make([]int, count), w)
		if err != nil || p.HasMore {
			t.Fatal("false extra page", p, err)
		}
	}
	if _, err := NewSlice(make([]int, 4), w); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded rows accepted")
	}
	if _, err := NewSlice[int](nil, Window{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid window accepted")
	}
	empty, err := NewSlice[int](nil, w)
	b, _ := json.Marshal(empty)
	if err != nil || string(b) != `{"current":2,"size":2,"has_more":false,"records":[]}` {
		t.Fatalf("slice JSON: %s %v", b, err)
	}
}

func TestPageJSONContract(t *testing.T) {
	w, _ := NewWindow(1, 2, DefaultLimits())
	p, err := NewPage([]int{1, 2}, w, 3)
	if err != nil || p.Pages != 2 || p.Total != 3 {
		t.Fatal(p, err)
	}
	b, _ := json.Marshal(p)
	if string(b) != `{"current":1,"size":2,"total":3,"pages":2,"records":[1,2]}` {
		t.Fatal(string(b))
	}
	p, err = NewPage[int](nil, w, 0)
	b, _ = json.Marshal(p)
	if err != nil || string(b) != `{"current":1,"size":2,"total":0,"pages":0,"records":[]}` {
		t.Fatalf("empty: %s %v", b, err)
	}
	for _, total := range []int64{-1, math.MinInt64} {
		if _, err := NewPage[int](nil, w, total); !errors.Is(err, ErrInvalid) {
			t.Fatal("negative total accepted")
		}
	}
	if _, err := NewPage([]int{1, 2, 3}, w, 3); !errors.Is(err, ErrInvalid) {
		t.Fatal("extra rows accepted")
	}
	if _, err := NewPage[int](nil, Window{}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid window accepted")
	}
	w, _ = NewWindow(math.MaxInt64, 1, Limits{MaxSize: 1, MaxOffset: math.MaxInt64})
	p, err = NewPage([]int{1}, w, math.MaxInt64)
	if err != nil || p.Pages != math.MaxInt64 {
		t.Fatal("overflow", p, err)
	}
}
func TestSortValidation(t *testing.T) {
	allowed := []string{"id", "title"}
	if err := ValidateSort([]Sort{{Field: "title", Desc: true}, {Field: "id"}}, allowed); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSort(nil, allowed); err != nil {
		t.Fatal(err)
	}
	for _, sorts := range [][]Sort{{{Field: ""}}, {{Field: "title; DROP TABLE records"}}, {{Field: "id"}, {Field: "id", Desc: true}}, make([]Sort, 9)} {
		if err := ValidateSort(sorts, allowed); !errors.Is(err, ErrInvalid) {
			t.Fatal("unsafe sort", err)
		}
	}
}
