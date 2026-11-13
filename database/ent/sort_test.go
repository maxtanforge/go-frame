package entdb_test

import (
	"testing"

	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

func TestSorterWhitelistAndStableTieBreaker(t *testing.T) {
	fields := map[string]entdb.SortOptions[string]{"id": {Asc: "id ASC", Desc: "id DESC"}, "price": {Asc: "price ASC", Desc: "price DESC"}}
	ties := []pagination.Sort{{Field: "id"}}
	sorter, err := entdb.NewSorter(fields, ties)
	require.NoError(t, err)
	fields["id"] = entdb.SortOptions[string]{Asc: "changed"}
	ties[0].Field = "changed"
	order, err := sorter.Resolve([]pagination.Sort{{Field: "price", Desc: true}})
	require.NoError(t, err)
	require.Equal(t, []string{"price DESC", "id ASC"}, order)
	order, err = sorter.Resolve([]pagination.Sort{{Field: "id", Desc: true}})
	require.NoError(t, err)
	require.Equal(t, []string{"id DESC"}, order)
	order, err = sorter.Resolve(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"id ASC"}, order)
	order[0] = "mutated"
	order, err = sorter.Resolve(nil)
	require.NoError(t, err)
	require.Equal(t, "id ASC", order[0])
	for _, input := range [][]pagination.Sort{{{Field: "unknown"}}, {{Field: "id"}, {Field: "id"}}} {
		_, err = sorter.Resolve(input)
		require.ErrorIs(t, err, pagination.ErrInvalid)
	}
}
func TestSorterRejectsInvalidConfiguration(t *testing.T) {
	_, err := entdb.NewSorter(map[string]entdb.SortOptions[string]{"id": {Asc: "id"}}, nil)
	require.Error(t, err)
	_, err = entdb.NewSorter(map[string]entdb.SortOptions[func()]{"id": {}}, []pagination.Sort{{Field: "id"}})
	require.Error(t, err)
	var zero entdb.Sorter[string]
	_, err = zero.Resolve(nil)
	require.Error(t, err)
	var absent *entdb.Sorter[string]
	_, err = absent.Resolve(nil)
	require.Error(t, err)
	fields := map[string]entdb.SortOptions[string]{}
	requested := []pagination.Sort{}
	for _, field := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "id"} {
		fields[field] = entdb.SortOptions[string]{Asc: field, Desc: field}
		if field != "id" {
			requested = append(requested, pagination.Sort{Field: field})
		}
	}
	s, err := entdb.NewSorter(fields, []pagination.Sort{{Field: "id"}})
	require.NoError(t, err)
	_, err = s.Resolve(requested)
	require.ErrorIs(t, err, pagination.ErrInvalid)
}
