package sqltest

import (
	"context"
	"reflect"
	"testing"

	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/query"
	"github.com/maxtanforge/go-frame/pagination"
)

// Uses the fixture-owned table only. Duplicate payloads prove that the explicit
// unique id tie-breaker governs page boundaries on each actual database.
func exercisePagination(ctx context.Context, t *testing.T, db *database.DB, table, insert string, postgres bool) {
	t.Helper()
	for _, id := range []int{3, 4} {
		if _, err := db.SQLDB().ExecContext(ctx, insert, id, "test value"); err != nil {
			t.Fatal(err)
		}
	}
	dialect := query.MySQL
	where := " WHERE id >= ?"
	if postgres {
		dialect = query.Postgres
		where = " WHERE id >= $1"
	}
	order, err := query.OrderBy(dialect, []pagination.Sort{{Field: "payload"}}, map[string]string{"payload": "payload", "id": "id"}, []pagination.Sort{{Field: "id"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		current int64
		ids     []int64
		more    bool
	}{{1, []int64{1, 3}, true}, {2, []int64{4}, false}, {3, []int64{}, false}} {
		window, err := pagination.NewWindow(tc.current, 2, pagination.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		limit, args, err := query.LimitOffset(dialect, window, 2, true)
		if err != nil {
			t.Fatal(err)
		}
		args = append([]any{int64(1)}, args...)
		rows, err := db.SQLDB().QueryContext(ctx, "SELECT id FROM "+table+where+order+limit, args...)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		rowErr := rows.Err()
		closeErr := rows.Close()
		if rowErr != nil {
			t.Fatal(rowErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		page, err := pagination.NewSlice(ids, window)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(page.Records, tc.ids) || page.HasMore != tc.more {
			t.Fatalf("unexpected page: %+v", page)
		}
	}
}
