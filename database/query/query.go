// Package query builds bounded SQL pagination suffixes without executing or
// rewriting queries. Callers own the base SQL, column mappings, and unique order.
package query

import (
	"errors"
	"strconv"
	"strings"

	"github.com/maxtanforge/go-frame/internal/sortspec"
	"github.com/maxtanforge/go-frame/pagination"
)

// Dialect selects SQL identifier quoting and parameter placeholders.
type Dialect string

// SQL dialects with sort-whitelist syntax differences.
const (
	MySQL    Dialect = "mysql"
	Postgres Dialect = "postgres"
)

// ErrInvalid reports invalid configuration or input without echoing it.
var ErrInvalid = errors.New("query: invalid parameters")

// OrderBy maps public sort aliases to qualified SQL identifiers. tieBreakers
// must be nonempty and must provide a unique order for the caller's result set.
// The caller owns that uniqueness guarantee; this package cannot inspect data.
// Requested tie-breakers retain their requested direction and position.
func OrderBy(d Dialect, sorts []pagination.Sort, columns map[string]string, tieBreakers []pagination.Sort) (string, error) {
	if !validDialect(d) || len(tieBreakers) == 0 {
		return "", ErrInvalid
	}
	allowed := make([]string, 0, len(columns))
	quoted := make(map[string]string, len(columns))
	for alias, column := range columns {
		value, ok := quoteIdentifier(d, column)
		if !ok || alias == "" {
			return "", ErrInvalid
		}
		allowed = append(allowed, alias)
		quoted[alias] = value
	}
	if pagination.ValidateSort(tieBreakers, allowed) != nil {
		return "", ErrInvalid
	}
	combined, err := sortspec.Combine(sorts, tieBreakers, allowed)
	if err != nil {
		return "", ErrInvalid
	}
	terms := make([]string, 0, len(combined))
	for _, s := range combined {
		direction := " ASC"
		if s.Desc {
			direction = " DESC"
		}
		terms = append(terms, quoted[s.Field]+direction)
	}
	return " ORDER BY " + strings.Join(terms, ", "), nil
}

// LimitOffset returns a suffix and int64 arguments in limit, offset order.
// firstParameter is 1..65534, including for MySQL's unnumbered placeholders.
// fetchExtra requests size+1 rows for pagination.NewSlice.
func LimitOffset(d Dialect, w pagination.Window, firstParameter int, fetchExtra bool) (string, []any, error) {
	if !validDialect(d) || w.Validate() != nil || firstParameter < 1 || firstParameter > 65534 {
		return "", nil, ErrInvalid
	}
	limit := w.Size()
	if fetchExtra {
		limit = w.FetchSize()
	}
	suffix := " LIMIT ? OFFSET ?"
	if d == Postgres {
		suffix = " LIMIT $" + strconv.Itoa(firstParameter) + " OFFSET $" + strconv.Itoa(firstParameter+1)
	}
	return suffix, []any{limit, w.Offset()}, nil
}

func validDialect(d Dialect) bool { return d == MySQL || d == Postgres }

func quoteIdentifier(d Dialect, identifier string) (string, bool) {
	quote := "`"
	if d == Postgres {
		quote = `"`
	}
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		if part == "" {
			return "", false
		}
		for j := 0; j < len(part); j++ {
			c := part[j]
			letter := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
			if !letter && !(j > 0 && c >= '0' && c <= '9') {
				return "", false
			}
		}
		parts[i] = quote + part + quote
	}
	return strings.Join(parts, "."), true
}
