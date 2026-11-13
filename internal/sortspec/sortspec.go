// Package sortspec carries the sort-whitelist algorithm shared by the SQL
// query helpers and the Ent adapter. Both public sorters must stay equivalent;
// the equivalence matrix lives in database/query tests.
package sortspec

import "github.com/maxtanforge/go-frame/pagination"

// MaxTerms bounds the combined requested-and-tie-breaker order expression.
const MaxTerms = 8

// Combine validates requested sorts against allowed aliases, keeps the first
// occurrence of each field in request order, appends tie-breakers missing from
// the request in their declared order, and rejects more than MaxTerms terms.
// Tie-breakers are assumed already validated against allowed by the caller:
// the SQL helper validates per call, the Ent sorter at construction.
func Combine(requested, ties []pagination.Sort, allowed []string) ([]pagination.Sort, error) {
	if err := pagination.ValidateSort(requested, allowed); err != nil {
		return nil, err
	}
	terms := append([]pagination.Sort(nil), requested...)
	seen := make(map[string]bool, len(terms)+len(ties))
	for _, term := range terms {
		seen[term.Field] = true
	}
	for _, term := range ties {
		if !seen[term.Field] {
			terms = append(terms, term)
			seen[term.Field] = true
		}
	}
	if len(terms) > MaxTerms {
		return nil, pagination.ErrInvalid
	}
	return terms, nil
}
