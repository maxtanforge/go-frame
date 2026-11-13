package main

import (
	"errors"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"net"
	"testing"
)

// Driver errors can contain connection details. Keep live-test diagnostics
// bounded to the stage and error type, never DSNs or raw server messages.
func integrationOK(t *testing.T, stage string, err error) {
	t.Helper()
	if err != nil {
		var my *mysqldriver.MySQLError
		if errors.As(err, &my) {
			t.Fatalf("%s failed (MySQL code %d, SQLSTATE %s)", stage, my.Number, my.SQLState)
		}
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			t.Fatalf("%s failed (PostgreSQL SQLSTATE %s)", stage, pg.Code)
		}
		var network *net.OpError
		if errors.As(err, &network) {
			t.Fatalf("%s failed (network operation %s, timeout=%t)", stage, network.Op, network.Timeout())
		}
		t.Fatalf("%s failed (%T)", stage, err)
	}
}
