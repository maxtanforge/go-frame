// Package faulttest provides test-only, connection-scoped fault injection.
package faulttest

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// Proxy relays test connections. Blocking closes only connections created
// through this proxy; it does not administer or restart the backend.
type Proxy struct {
	listener    net.Listener
	target      string
	mu          sync.Mutex
	blocked     bool
	closed      bool
	connections map[net.Conn]struct{}
	wg          sync.WaitGroup
}

// NewProxy starts a loopback relay and registers cleanup with t.
func NewProxy(t *testing.T, target string) *Proxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("cannot listen for fault injection")
	}
	p := &Proxy{listener: l, target: target, connections: make(map[net.Conn]struct{})}
	p.wg.Go(p.accept)
	t.Cleanup(p.close)
	return p
}

// Addr is the test-only local endpoint.
func (p *Proxy) Addr() string { return p.listener.Addr().String() }

// Block enables or disables connection refusal, dropping established relays
// when enabled. It is safe to call concurrently with connection setup.
func (p *Proxy) Block(blocked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = blocked
	if blocked {
		for c := range p.connections {
			_ = c.Close()
		}
	}
}

func (p *Proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked || p.closed {
		_ = c.Close()
		return false
	}
	p.connections[c] = struct{}{}
	return true
}

func (p *Proxy) accept() {
	for {
		c, err := p.listener.Accept()
		if err != nil {
			return
		}
		if !p.track(c) {
			continue
		}
		p.wg.Go(func() { p.relay(c) })
	}
}

func (p *Proxy) relay(client net.Conn) {
	var backend net.Conn
	defer func() {
		_ = client.Close()
		if backend != nil {
			_ = backend.Close()
		}
		p.mu.Lock()
		delete(p.connections, client)
		delete(p.connections, backend)
		p.mu.Unlock()
	}()
	var err error
	backend, err = net.DialTimeout("tcp", p.target, time.Second)
	if err != nil || !p.track(backend) {
		return
	}
	// Recheck the client: Block may have closed it while backend dialed.
	p.mu.Lock()
	blocked := p.blocked || p.closed
	p.mu.Unlock()
	if blocked {
		return
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(backend, client); _ = backend.Close(); close(done) }()
	_, _ = io.Copy(client, backend)
	_ = client.Close()
	_ = backend.Close()
	<-done
}

func (p *Proxy) close() {
	p.mu.Lock()
	p.closed = true
	for c := range p.connections {
		_ = c.Close()
	}
	p.mu.Unlock()
	_ = p.listener.Close()
	p.wg.Wait()
}
