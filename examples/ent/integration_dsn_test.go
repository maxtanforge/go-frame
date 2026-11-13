package main

import (
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/maxtanforge/go-frame/database/query"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

func integrationDSN(t *testing.T, dialect query.Dialect, dsn string) string {
	// Accept a development application's MySQL URL without printing it.
	if dialect == query.MySQL && strings.HasPrefix(dsn, "mysql://") {
		u, err := url.Parse(dsn)
		integrationOK(t, "parse MySQL URL", err)
		if u.User == nil || u.Hostname() == "" || u.Path == "" {
			t.Fatal("invalid MySQL URL shape")
		}
		c := mysqldriver.NewConfig()
		c.User = u.User.Username()
		c.Passwd, _ = u.User.Password()
		port := u.Port()
		if port == "" {
			port = "3306"
		}
		c.Net, c.Addr, c.DBName = "tcp", net.JoinHostPort(u.Hostname(), port), strings.TrimPrefix(u.Path, "/")
		c.ParseTime, c.Loc = true, time.UTC
		c.Timeout, c.ReadTimeout, c.WriteTimeout = 10*time.Second, 10*time.Second, 10*time.Second
		// URL options are intentionally rejected rather than silently losing
		// TLS/session settings. Use the native driver DSN for these cases.
		if u.RawQuery != "" {
			t.Fatal("MySQL URL options require a native driver DSN")
		}
		dsn = c.FormatDSN()
	}

	return dsn
}
