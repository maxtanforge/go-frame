package entdb

import (
	"errors"

	"github.com/maxtanforge/go-frame/internal/isnil"
	"github.com/maxtanforge/go-frame/internal/sortspec"
	"github.com/maxtanforge/go-frame/pagination"
)

// ErrSortConfig reports an invalid application-owned whitelist or tie-breaker.
var ErrSortConfig = errors.New("entdb: invalid sort configuration")

// SortOptions stores application-built ascending and descending order options,
// for example product.ByPrice() and product.ByPrice(sql.OrderDesc()).
type SortOptions[O any] struct{ Asc, Desc O }

// Sorter maps public aliases to typed options, never to user-supplied SQL.
// Immutable after construction; supplied option functions must be concurrency safe.
type Sorter[O any] struct {
	fields  map[string]SortOptions[O]
	allowed []string
	ties    []pagination.Sort
}

// NewSorter copies configuration. tieBreakers must establish a unique order for
// the application's result set; the helper cannot prove database uniqueness.
// Option values are shallow copied; do not mutate captured closure state.
func NewSorter[O any](fields map[string]SortOptions[O], tieBreakers []pagination.Sort) (*Sorter[O], error) {
	if len(fields) == 0 || len(tieBreakers) == 0 {
		return nil, ErrSortConfig
	}
	s := &Sorter[O]{fields: make(map[string]SortOptions[O], len(fields)), ties: append([]pagination.Sort(nil), tieBreakers...)}
	for alias, options := range fields {
		if alias == "" || isnil.Check(options.Asc) || isnil.Check(options.Desc) {
			return nil, ErrSortConfig
		}
		s.fields[alias] = options
		s.allowed = append(s.allowed, alias)
	}
	if pagination.ValidateSort(s.ties, s.allowed) != nil {
		return nil, ErrSortConfig
	}
	return s, nil
}

// Resolve validates requested order and appends missing unique tie-breakers.
// Explicitly requested tie-breakers keep their direction and position. The
// combined order is limited to eight terms. Each result is an independent slice.
func (s *Sorter[O]) Resolve(requested []pagination.Sort) ([]O, error) {
	if s == nil || len(s.fields) == 0 || len(s.ties) == 0 {
		return nil, ErrSortConfig
	}
	terms, err := sortspec.Combine(requested, s.ties, s.allowed)
	if err != nil {
		return nil, err
	}
	out := make([]O, 0, len(terms))
	for _, term := range terms {
		option := s.fields[term.Field].Asc
		if term.Desc {
			option = s.fields[term.Field].Desc
		}
		out = append(out, option)
	}
	return out, nil
}
