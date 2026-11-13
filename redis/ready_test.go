package redis

import (
	"bufio"
	"context"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenRejectsPoolOverflow(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("int cannot exceed int32")
	}
	limit := int64(math.MaxInt32) + 1
	for _, o := range []Options{
		{Addr: "127.0.0.1:6379", PoolSize: int(limit)},
		{Addr: "127.0.0.1:6379", MinIdleConns: int(limit)},
		{Addr: "127.0.0.1:6379", MaxIdleConns: int(limit)},
	} {
		require.NotPanics(t, func() {
			c, err := Open(o)
			if c != nil {
				_ = c.Close()
			}
			require.Error(t, err)
			require.Nil(t, c)
			c, err = OpenReady(context.Background(), o)
			if c != nil {
				_ = c.Close()
			}
			require.Error(t, err)
			require.Nil(t, c)
		})
	}
}

func TestOpenRejectsPrewarm(t *testing.T) {
	c, err := Open(Options{Addr: "127.0.0.1:6379", MinIdleConns: 1})
	if c != nil {
		_ = c.Close()
	}
	require.ErrorContains(t, err, "OpenReady")
	require.Nil(t, c)
}

func TestOpenReadyContextAndValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, contextValue := range []context.Context{nil, ctx} {
		c, err := OpenReady(contextValue, Options{Addr: "127.0.0.1:6379"})
		require.Error(t, err)
		require.Nil(t, c)
	}
	c, err := OpenReady(context.Background(), Options{})
	require.Error(t, err)
	require.Nil(t, c)
}

func TestOpenReadyPrewarmAndPing(t *testing.T) {
	addr, _ := lostReplyServer(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := OpenReady(ctx, Options{Addr: addr, PoolSize: 2, MinIdleConns: 1})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.Equal(t, 1, c.Client.Options().MinIdleConns)
	require.Greater(t, c.Client.PoolStats().TotalConns, uint32(0))
}

func TestOpenReadyDeadlineClosesConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		r := bufio.NewReader(conn)
		for {
			cmd, err := readTestCommand(r)
			if err != nil {
				return
			}
			switch strings.ToLower(cmd[0]) {
			case "hello":
				_, _ = io.WriteString(conn, "-ERR unknown command 'hello'\r\n")
			case "ping":
				_, _ = io.Copy(io.Discard, conn)
				return
			default:
				_, _ = io.WriteString(conn, "+OK\r\n")
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	c, err := OpenReady(ctx, Options{Addr: ln.Addr().String()})
	require.Error(t, err)
	require.Nil(t, c)
	require.Less(t, time.Since(start), time.Second)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed startup left connection open")
	}
}

func TestOpenDoesNotDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	c, err := Open(Options{Addr: ln.Addr().String()})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.NoError(t, ln.(*net.TCPListener).SetDeadline(time.Now().Add(50*time.Millisecond)))
	conn, err := ln.Accept()
	if conn != nil {
		_ = conn.Close()
		t.Fatal("lazy constructor opened a connection")
	}
	require.Error(t, err)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	require.True(t, netErr.Timeout())
}

func TestOpenReadyRejectsCancellationWithSuccessfulReply(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		r := bufio.NewReader(conn)
		for {
			cmd, err := readTestCommand(r)
			if err != nil {
				return
			}
			switch strings.ToLower(cmd[0]) {
			case "hello":
				_, _ = io.WriteString(conn, "-ERR unknown command 'hello'\r\n")
			case "ping":
				cancel()
				_, _ = io.WriteString(conn, "+PONG\r\n")
				_, _ = io.Copy(io.Discard, conn)
				return
			default:
				_, _ = io.WriteString(conn, "+OK\r\n")
			}
		}
	}()
	c, err := OpenReady(ctx, Options{Addr: ln.Addr().String()})
	if c != nil {
		_ = c.Close()
	}
	require.Nil(t, c)
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled startup left a connection open")
	}
}
