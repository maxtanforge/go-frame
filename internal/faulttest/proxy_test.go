package faulttest

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProxyDisconnectAndRecovery(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })
	go func() {
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			go func() { defer func() { _ = c.Close() }(); _, _ = io.Copy(c, c) }()
		}
	}()
	p := NewProxy(t, backend.Addr().String())
	check := func() net.Conn {
		c, err := net.DialTimeout("tcp", p.Addr(), time.Second)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		require.NoError(t, c.SetDeadline(time.Now().Add(time.Second)))
		_, err = c.Write([]byte("ping"))
		require.NoError(t, err)
		buf := make([]byte, 4)
		_, err = io.ReadFull(c, buf)
		require.NoError(t, err)
		require.Equal(t, "ping", string(buf))
		return c
	}
	c := check()
	p.Block(true)
	_, err = c.Read(make([]byte, 1))
	require.Error(t, err)
	c, err = net.DialTimeout("tcp", p.Addr(), time.Second)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.NoError(t, c.SetDeadline(time.Now().Add(time.Second)))
	_, err = c.Read(make([]byte, 1))
	require.Error(t, err)
	p.Block(false)
	check()
}
