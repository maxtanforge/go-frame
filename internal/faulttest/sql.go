package faulttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/database"
)

// SQLRecovery exercises pool exhaustion, connection loss and recovery without
// writes or backend administration. db must have MaxOpenConns=1 and dial via p.
func SQLRecovery(t *testing.T, db *database.DB, p *Proxy) {
	t.Helper()
	ping := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return db.Ping(ctx)
	}
	if ping() != nil {
		t.Fatal("initial SQL connectivity failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	conn, err := db.SQLDB().Conn(ctx)
	cancel()
	if err != nil {
		t.Fatal("cannot hold SQL pool connection")
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = db.Ping(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("pool exhaustion did not honor deadline")
	}
	if db.SQLDB().Stats().WaitCount == 0 {
		t.Fatal("pool exhaustion was not observed")
	}
	_ = conn.Close()
	p.Block(true)
	ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
	start := time.Now()
	err = db.Ping(ctx)
	cancel()
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("disconnected SQL request did not fail within budget")
	}
	p.Block(false)
	if ping() != nil {
		t.Fatal("same SQL pool did not recover after reconnect")
	}
	if db.SQLDB().Stats().InUse != 0 {
		t.Fatal("SQL connection remained in use after recovery")
	}
}
