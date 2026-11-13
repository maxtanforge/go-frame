package query_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/maxtanforge/go-frame/database/query"
	"github.com/maxtanforge/go-frame/pagination"
)

func TestOrderBy(t *testing.T) {
	columns := map[string]string{"id": "records.id", "title": "records.title", "code": "_data.Code_2"}
	for _, tc := range []struct {
		dialect query.Dialect
		want    string
	}{
		{query.MySQL, " ORDER BY `records`.`title` DESC, `records`.`id` ASC"},
		{query.Postgres, ` ORDER BY "records"."title" DESC, "records"."id" ASC`},
	} {
		got, err := query.OrderBy(tc.dialect, []pagination.Sort{{Field: "title", Desc: true}}, columns, []pagination.Sort{{Field: "id"}})
		if err != nil || got != tc.want {
			t.Fatalf("OrderBy = %q, %v; want %q", got, err, tc.want)
		}
	}
	got, err := query.OrderBy(query.Postgres, []pagination.Sort{{Field: "id", Desc: true}}, columns, []pagination.Sort{{Field: "id"}, {Field: "code", Desc: true}})
	if err != nil || got != ` ORDER BY "records"."id" DESC, "_data"."Code_2" DESC` {
		t.Fatalf("requested tie direction = %q, %v", got, err)
	}
	got, err = query.OrderBy(query.MySQL, nil, columns, []pagination.Sort{{Field: "id"}})
	if err != nil || got != " ORDER BY `records`.`id` ASC" {
		t.Fatalf("tie only = %q, %v", got, err)
	}
}

func TestOrderByRejectsInvalidInput(t *testing.T) {
	valid := map[string]string{"id": "r.id", "title": "r.title"}
	for _, tc := range []struct {
		name    string
		d       query.Dialect
		sorts   []pagination.Sort
		columns map[string]string
		ties    []pagination.Sort
	}{
		{"dialect", "sqlite", nil, valid, []pagination.Sort{{Field: "id"}}},
		{"missing tie", query.MySQL, nil, valid, nil},
		{"unknown", query.MySQL, []pagination.Sort{{Field: "unknown"}}, valid, []pagination.Sort{{Field: "id"}}},
		{"empty field", query.MySQL, []pagination.Sort{{Field: ""}}, valid, []pagination.Sort{{Field: "id"}}},
		{"duplicate", query.MySQL, []pagination.Sort{{Field: "id"}, {Field: "id", Desc: true}}, valid, []pagination.Sort{{Field: "id"}}},
		{"unknown tie", query.MySQL, nil, valid, []pagination.Sort{{Field: "unknown"}}},
		{"duplicate tie", query.MySQL, nil, valid, []pagination.Sort{{Field: "id"}, {Field: "id"}}},
		{"nil columns", query.MySQL, nil, nil, []pagination.Sort{{Field: "id"}}},
		{"sort injection", query.MySQL, []pagination.Sort{{Field: "id; DROP TABLE records"}}, valid, []pagination.Sort{{Field: "id"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := query.OrderBy(tc.d, tc.sorts, tc.columns, tc.ties)
			if got != "" || !errors.Is(err, query.ErrInvalid) {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	for _, bad := range []string{"", "r..id", ".id", "id.", "1id", "r.id DESC", "r.id;DROP TABLE records", "r.id--", "r.id/*x*/", "lower(id)", "id\"", "id`", "r.*", "résumé", "id\x00", "a b"} {
		t.Run("column "+bad, func(t *testing.T) {
			got, err := query.OrderBy(query.Postgres, nil, map[string]string{"id": bad}, []pagination.Sort{{Field: "id"}})
			if got != "" || !errors.Is(err, query.ErrInvalid) || strings.Contains(err.Error(), bad) && bad != "" {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestOrderByTermLimit(t *testing.T) {
	columns := map[string]string{}
	sorts := make([]pagination.Sort, 8)
	for i := range sorts {
		name := string(rune('a' + i))
		columns[name] = name
		sorts[i] = pagination.Sort{Field: name}
	}
	if _, err := query.OrderBy(query.Postgres, sorts, columns, []pagination.Sort{{Field: "a"}}); err != nil {
		t.Fatal(err)
	}
	columns["z"] = "z"
	if _, err := query.OrderBy(query.Postgres, sorts, columns, []pagination.Sort{{Field: "z"}}); !errors.Is(err, query.ErrInvalid) {
		t.Fatalf("nine final terms: %v", err)
	}
	sorts = append(sorts, pagination.Sort{Field: "z"})
	if _, err := query.OrderBy(query.Postgres, sorts, columns, []pagination.Sort{{Field: "a"}}); !errors.Is(err, query.ErrInvalid) {
		t.Fatalf("nine requested terms: %v", err)
	}
}

func TestLimitOffset(t *testing.T) {
	w, err := pagination.NewWindow(3, 20, pagination.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		d     query.Dialect
		start int
		extra bool
		sql   string
		limit int64
	}{
		{query.MySQL, 1, false, " LIMIT ? OFFSET ?", 20},
		{query.MySQL, 42, true, " LIMIT ? OFFSET ?", 21},
		{query.Postgres, 1, false, " LIMIT $1 OFFSET $2", 20},
		{query.Postgres, 4, true, " LIMIT $4 OFFSET $5", 21},
		{query.Postgres, 65534, true, " LIMIT $65534 OFFSET $65535", 21},
	} {
		got, args, err := query.LimitOffset(tc.d, w, tc.start, tc.extra)
		if err != nil || got != tc.sql || !reflect.DeepEqual(args, []any{tc.limit, int64(40)}) {
			t.Fatalf("LimitOffset = %q, %#v, %v", got, args, err)
		}
	}
	for _, tc := range []struct {
		d     query.Dialect
		w     pagination.Window
		start int
	}{
		{query.MySQL, w, 0}, {query.MySQL, w, -1}, {query.MySQL, w, 65535}, {query.Postgres, w, 0}, {query.Postgres, w, 65535}, {"sqlite", w, 1}, {query.MySQL, pagination.Window{}, 1},
	} {
		got, args, err := query.LimitOffset(tc.d, tc.w, tc.start, false)
		if got != "" || args != nil || !errors.Is(err, query.ErrInvalid) {
			t.Fatalf("invalid LimitOffset = %q, %#v, %v", got, args, err)
		}
	}
}
