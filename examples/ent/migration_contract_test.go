package main

import (
	"os"
	"strings"
	"testing"
)

// The application also exposes this enum through Ent, but raw SQL and bulk
// imports must face the same restriction at the PostgreSQL database boundary.
func TestPostgresEventActionMigrationConstraint(t *testing.T) {
	raw, err := os.ReadFile("migrations/postgres/202609300001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	statement := string(raw)
	if !strings.Contains(statement, "CONSTRAINT product_events_action_enum CHECK (action IN ('created', 'updated', 'deleted'))") {
		t.Fatal("PostgreSQL action column must enforce Ent enum values")
	}
}
