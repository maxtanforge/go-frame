package redis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The server executes INCR and loses its first reply, an ambiguous write outcome.
func TestDefaultDoesNotReplayAmbiguousWrite(t *testing.T) {
	addr, executions := lostReplyServer(t, 1)
	c, err := Open(Options{Addr: addr})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = c.Client.Incr(ctx, "counter").Result()
	require.Error(t, err)
	require.Equal(t, int64(1), executions.Load())
}

func TestExplicitRetryCanRepeatAmbiguousWrite(t *testing.T) {
	addr, executions := lostReplyServer(t, 1)
	c, err := Open(Options{Addr: addr, MaxRetries: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := c.Client.Incr(ctx, "counter").Result()
	require.NoError(t, err)
	require.Equal(t, int64(2), value)
	require.Equal(t, int64(2), executions.Load())
}

func TestNegativeRetriesRejected(t *testing.T) {
	_, err := Open(Options{Addr: "localhost:6379", MaxRetries: -1})
	require.Error(t, err)
}

func TestExplicitRetriesAreBounded(t *testing.T) {
	addr, executions := lostReplyServer(t, 10)
	c, err := Open(Options{Addr: addr, MaxRetries: 2})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = c.Client.Incr(ctx, "counter").Result()
	require.Error(t, err)
	require.Equal(t, int64(3), executions.Load())
}

func lostReplyServer(t *testing.T, lostReplies int64) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var executions atomic.Int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	var conns []net.Conn
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				for {
					cmd, err := readTestCommand(r)
					if err != nil {
						return
					}
					switch strings.ToLower(cmd[0]) {
					case "hello":
						_, err = io.WriteString(conn, "-ERR unknown command 'hello'\r\n")
					case "incr", "eval":
						n := executions.Add(1)
						if n <= lostReplies {
							return
						}
						_, err = fmt.Fprintf(conn, ":%d\r\n", n)
					default:
						_, err = io.WriteString(conn, "+OK\r\n")
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-accepted
		mu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String(), &executions
}

func readTestCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("expected array")
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || n < 1 || n > 64 {
		return nil, fmt.Errorf("invalid array size")
	}
	cmd := make([]string, n)
	for i := range cmd {
		line, err = r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(line, "$") {
			return nil, fmt.Errorf("expected bulk string")
		}
		length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || length < 0 || length > 4096 {
			return nil, fmt.Errorf("invalid bulk string length")
		}
		data := make([]byte, length+2)
		if _, err = io.ReadFull(r, data); err != nil {
			return nil, err
		}
		cmd[i] = string(data[:length])
	}
	return cmd, nil
}
