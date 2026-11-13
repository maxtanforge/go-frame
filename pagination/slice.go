package pagination

// Slice is an offset window without a count query. It deliberately has no Total
// or Pages fields, so unknown totals cannot be mistaken for a known zero.
type Slice[T any] struct {
	Current int64 `json:"current"`
	Size    int64 `json:"size"`
	HasMore bool  `json:"has_more"`
	Records []T   `json:"records"`
}

// NewSlice accepts up to size+1 rows and removes the extra probe row. Callers
// must fetch Window.FetchSize rows for HasMore=false to prove exhaustion.
// It copies the slice; objects inside each record remain caller-owned.
func NewSlice[T any](rows []T, w Window) (Slice[T], error) {
	if err := w.Validate(); err != nil {
		return Slice[T]{}, err
	}
	if int64(len(rows)) > w.FetchSize() {
		return Slice[T]{}, ErrInvalid
	}
	more := int64(len(rows)) > w.Size()
	if more {
		rows = rows[:int(w.Size())]
	}
	records := make([]T, len(rows))
	copy(records, rows)
	return Slice[T]{Current: w.Current(), Size: w.Size(), HasMore: more, Records: records}, nil
}
