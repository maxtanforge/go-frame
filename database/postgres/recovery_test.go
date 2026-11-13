package postgres

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/maxtanforge/go-frame/database"
	"github.com/maxtanforge/go-frame/internal/faulttest"
)

func TestRecoveryIntegration(t *testing.T) {
	if os.Getenv("FRAME_TEST_RECOVERY") != "1" {
		t.Skip("set FRAME_TEST_RECOVERY=1 to opt in")
	}
	if os.Getenv("FRAME_TEST_POSTGRES_DSN") == "" {
		t.Fatal("PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("FRAME_TEST_POSTGRES_DSN"))
	if err != nil || cfg.Host == "" || cfg.Host[0] == '/' {
		t.Fatal("recovery requires PostgreSQL TCP test DSN")
	}
	for _, fallback := range cfg.Fallbacks {
		if fallback.Host != cfg.Host || fallback.Port != cfg.Port {
			t.Fatal("multi-host recovery is not supported")
		}
	}
	p := faulttest.NewProxy(t, net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))))
	cfg.DialFunc = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, p.Addr())
	}
	db, err := database.Open(stdlib.GetConnector(*cfg), database.Options{MaxOpenConns: 1, MaxIdleConns: 1})
	if err != nil {
		t.Fatal("cannot construct PostgreSQL test pool")
	}
	t.Cleanup(func() {
		if db.Close() != nil {
			t.Error("SQL pool cleanup failed")
		}
	})
	faulttest.SQLRecovery(t, db, p)
}
