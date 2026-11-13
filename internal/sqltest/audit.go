package sqltest

import (
	"context"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/database/audit"
)

func exerciseAudit(ctx context.Context, t *testing.T, db *database.DB, base string, postgres bool) {
	t.Helper()
	table := base + "_audit"
	timestamp := "DATETIME(3)"
	if postgres {
		timestamp = "TIMESTAMPTZ(3)"
	}
	ddl := "CREATE TABLE " + table + " (id INTEGER PRIMARY KEY, created_at " + timestamp + " NOT NULL, updated_at " + timestamp + " NOT NULL, created_kind VARCHAR(16) NOT NULL, created_id VARCHAR(128) NOT NULL, updated_kind VARCHAR(16) NOT NULL, updated_id VARCHAR(128) NOT NULL)"
	if _, err := db.SQLDB().ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.SQLDB().ExecContext(cleanup, "DROP TABLE "+table); err != nil {
			t.Errorf("cleanup audit fixture: %v", err)
		}
	}()
	created, err := audit.New(audit.Actor{Kind: audit.System, ID: "fixture"}, time.Date(2026, 9, 28, 12, 34, 56, 123456789, time.FixedZone("east", 8*3600)))
	if err != nil {
		t.Fatal(err)
	}
	bind := "?, ?, ?, ?, ?, ?, ?"
	if postgres {
		bind = "$1, $2, $3, $4, $5, $6, $7"
	}
	if _, err := db.SQLDB().ExecContext(ctx, "INSERT INTO "+table+" (id,created_at,updated_at,created_kind,created_id,updated_kind,updated_id) VALUES ("+bind+")", 1, created.CreatedAt, created.UpdatedAt, created.CreatedBy.Kind, created.CreatedBy.ID, created.UpdatedBy.Kind, created.UpdatedBy.ID); err != nil {
		t.Fatal(err)
	}
	read := func() audit.Fields {
		var got audit.Fields
		var createdTime, updatedTime any
		err := db.SQLDB().QueryRowContext(ctx, "SELECT created_at,updated_at,created_kind,created_id,updated_kind,updated_id FROM "+table+" WHERE id=1").Scan(&createdTime, &updatedTime, &got.CreatedBy.Kind, &got.CreatedBy.ID, &got.UpdatedBy.Kind, &got.UpdatedBy.ID)
		if err != nil {
			t.Fatal(err)
		}
		got.CreatedAt = fixtureTime(t, createdTime)
		got.UpdatedAt = fixtureTime(t, updatedTime)
		if err := got.Validate(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(); got != created {
		t.Fatalf("creation round trip differs: %+v", got)
	}
	updated, err := created.WithUpdate(audit.Actor{Kind: audit.Subject, ID: "fixture-user"}, created.UpdatedAt.Add(time.Hour+1234567*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	set := "updated_at=?,updated_kind=?,updated_id=? WHERE id=?"
	if postgres {
		set = "updated_at=$1,updated_kind=$2,updated_id=$3 WHERE id=$4"
	}
	if _, err := db.SQLDB().ExecContext(ctx, "UPDATE "+table+" SET "+set, updated.UpdatedAt, updated.UpdatedBy.Kind, updated.UpdatedBy.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != updated {
		t.Fatalf("update round trip differs: %+v", got)
	}
}

// Generic adapter fixtures also accept MySQL DSNs without parseTime. The records
// example separately requires parseTime=true and loc=UTC before pool creation.
func fixtureTime(t *testing.T, v any) time.Time {
	t.Helper()
	switch v := v.(type) {
	case time.Time:
		return v.UTC()
	case []byte:
		parsed, err := time.ParseInLocation("2006-01-02 15:04:05.999999", string(v), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	case string:
		parsed, err := time.ParseInLocation("2006-01-02 15:04:05.999999", v, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	default:
		t.Fatalf("unsupported fixture timestamp type %T", v)
	}
	return time.Time{}
}
