package pagination

// Page is the counted page result. Total and Pages are
// always numbers. NewPage ensures empty Records serializes as an array.
type Page[T any] struct {
	Current int64 `json:"current"`
	Size    int64 `json:"size"`
	Total   int64 `json:"total"`
	Pages   int64 `json:"pages"`
	Records []T   `json:"records"`
}

// NewPage accepts at most size rows and a nonnegative, explicitly queried total.
// A separately queried total need not share the list's database snapshot.
// It copies the slice; objects inside each record remain caller-owned.
func NewPage[T any](rows []T, w Window, total int64) (Page[T], error) {
	if err := w.Validate(); err != nil {
		return Page[T]{}, err
	}
	if total < 0 || int64(len(rows)) > w.Size() {
		return Page[T]{}, ErrInvalid
	}
	pages := total / w.Size()
	if total%w.Size() != 0 {
		pages++
	}
	records := make([]T, len(rows))
	copy(records, rows)
	return Page[T]{Current: w.Current(), Size: w.Size(), Total: total, Pages: pages, Records: records}, nil
}
