package config

import "testing"

func TestDefaultDatabaseDriverIsPostgres(t *testing.T) {
	if got := Defaults().Database.Driver; got != "postgres" {
		t.Fatalf("default database driver = %q, want postgres", got)
	}
}
