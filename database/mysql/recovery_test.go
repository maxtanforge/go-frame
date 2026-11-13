package mysql

import (
	"context"
	"net"
	"os"
	"testing"

	driver "github.com/go-sql-driver/mysql"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/internal/faulttest"
)

func TestRecoveryIntegration(t *testing.T) {
	if os.Getenv("FRAME_TEST_RECOVERY") != "1" {
		t.Skip("set FRAME_TEST_RECOVERY=1 to opt in")
	}
	if os.Getenv("FRAME_TEST_MYSQL_DSN") == "" {
		t.Fatal("MySQL test DSN required")
	}
	cfg, err := driver.ParseDSN(os.Getenv("FRAME_TEST_MYSQL_DSN"))
	if err != nil || cfg.Net != "tcp" {
		t.Fatal("recovery requires explicit MySQL TCP test DSN")
	}
	p := faulttest.NewProxy(t, cfg.Addr)
	cfg.DialFunc = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, p.Addr())
	}
	c, err := driver.NewConnector(cfg)
	if err != nil {
		t.Fatal("invalid MySQL test connector")
	}
	db, err := database.Open(c, database.Options{MaxOpenConns: 1, MaxIdleConns: 1})
	if err != nil {
		t.Fatal("cannot construct MySQL test pool")
	}
	t.Cleanup(func() {
		if db.Close() != nil {
			t.Error("SQL pool cleanup failed")
		}
	})
	faulttest.SQLRecovery(t, db, p)
}
