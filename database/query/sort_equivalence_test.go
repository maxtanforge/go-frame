package query_test

import (
	"strings"
	"testing"

	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/database/query"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

// 锁定两个公开排序入口的契约等价:同一输入矩阵下,字段与方向的最终序列一致。
// 本文件是测试层跨包导入的唯一先例,理由见 specs/2026-09-30-shared-sort-design.md:
// 守护测试只检查生产依赖,此处不产生 query→ent 的编译期依赖边。

type option struct{ sql string }

type term struct {
	field string
	desc  bool
}

var sortColumns = map[string]string{
	"id": "r.id", "title": "r.title", "price": "r.price", "created_at": "r.created_at",
	"a": "r.a", "b": "r.b", "c": "r.c", "d": "r.d", "e": "r.e", "f": "r.f", "g": "r.g",
}

func newEntSorter(t *testing.T, ties []pagination.Sort) *entdb.Sorter[option] {
	t.Helper()
	fields := map[string]entdb.SortOptions[option]{}
	for alias, column := range sortColumns {
		fields[alias] = entdb.SortOptions[option]{
			Asc:  option{column + " ASC"},
			Desc: option{column + " DESC"},
		}
	}
	sorter, err := entdb.NewSorter(fields, ties)
	require.NoError(t, err)
	return sorter
}

func parseSQL(t *testing.T, sql string) []term {
	t.Helper()
	reverse := map[string]string{}
	for alias, column := range sortColumns {
		reverse[column] = alias
	}
	parts := strings.Split(strings.TrimPrefix(sql, " ORDER BY "), ", ")
	terms := make([]term, 0, len(parts))
	for _, part := range parts {
		part = strings.NewReplacer(`"`, "", "`", "").Replace(part)
		column, direction, ok := strings.Cut(part, " ")
		require.True(t, ok)
		alias, ok := reverse[column]
		require.True(t, ok, column)
		terms = append(terms, term{field: alias, desc: direction == "DESC"})
	}
	return terms
}

func parseOptions(options []option) []term {
	terms := make([]term, 0, len(options))
	for _, o := range options {
		column, direction, _ := strings.Cut(o.sql, " ")
		terms = append(terms, term{field: strings.TrimPrefix(column, "r."), desc: direction == "DESC"})
	}
	return terms
}

func TestSortWhitelistsStayEquivalent(t *testing.T) {
	requested := func(fields ...string) []pagination.Sort {
		sorts := make([]pagination.Sort, 0, len(fields))
		for _, f := range fields {
			desc := strings.HasPrefix(f, "-")
			sorts = append(sorts, pagination.Sort{Field: strings.TrimPrefix(f, "-"), Desc: desc})
		}
		return sorts
	}
	ties := []pagination.Sort{{Field: "id"}}
	for _, tc := range []struct {
		name string
		sort []pagination.Sort
	}{
		{"empty", nil},
		{"single-asc", requested("title")},
		{"single-desc", requested("-price")},
		{"multi", requested("title", "-price", "created_at")},
		{"explicit-tie-keeps-position", requested("-id", "title")},
		{"seven-fields", requested("a", "b", "c", "d", "e", "f", "g")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := query.OrderBy(query.Postgres, tc.sort, sortColumns, ties)
			require.NoError(t, err)
			options, err := newEntSorter(t, ties).Resolve(tc.sort)
			require.NoError(t, err)
			require.Equal(t, parseSQL(t, sql), parseOptions(options))
		})
	}
	for _, tc := range []struct {
		name string
		sort []pagination.Sort
	}{
		{"unknown-field", requested("secret")},
		{"duplicate-field", requested("title", "title")},
		{"combined-over-eight", requested("a", "b", "c", "d", "e", "f", "-title")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, sqlErr := query.OrderBy(query.Postgres, tc.sort, sortColumns, []pagination.Sort{{Field: "id"}, {Field: "created_at"}})
			_, optErr := newEntSorter(t, []pagination.Sort{{Field: "id"}, {Field: "created_at"}}).Resolve(tc.sort)
			require.Error(t, sqlErr)
			require.Error(t, optErr)
		})
	}
}
