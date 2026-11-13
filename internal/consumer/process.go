package consumer

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var errDependencyNotCached = errors.New("dependency not cached")
var errDependencyNetwork = errors.New("dependency network failed")
var errOutputLimit = errors.New("output limit exceeded")

const outputLimit = 1 << 20

type capture struct {
	overflow bool
	stderr   *capture
	mu       sync.Mutex
	b        []byte
}

func (c *capture) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(b)
	if len(b) > outputLimit-len(c.b) {
		c.overflow = true
	}
	if len(c.b) < outputLimit {
		c.b = append(c.b, b[:min(len(b), outputLimit-len(c.b))]...)
	}
	return n, nil
}
func (c *capture) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.b...)
}

func (c *capture) exceeded() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.overflow }

type child struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	done       chan struct{}
	err        error
	clean      func() error
	once       sync.Once
	cleanupErr error
}

func (p *child) cleanup() error { p.once.Do(func() { p.cleanupErr = p.clean() }); return p.cleanupErr }
func start(ctx context.Context, dir string, env []string, program string, args ...string) (*child, *capture, error) {
	output := &capture{stderr: &capture{}}
	p, e := platformStart(dir, env, output, output.stderr, program, args)
	if e != nil {
		return nil, output, e
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	go func() {
		select {
		case <-ctx.Done():
			_ = p.cleanup()
		case <-p.done:
		}
	}()
	return p, output, nil
}
func (p *child) wait(ctx context.Context) error {
	select {
	case <-p.done:
		return errors.Join(p.err, p.cleanup())
	case <-ctx.Done():
		e := p.cleanup()
		select {
		case <-p.done:
			return errors.Join(ctx.Err(), e)
		case <-time.After(3 * time.Second):
			return errors.New("process cleanup deadline")
		}
	}
}
func runWithStderr(ctx context.Context, dir string, env []string, program string, args ...string) ([]byte, []byte, error) {
	p, c, e := start(ctx, dir, env, program, args...)
	if e != nil {
		return nil, nil, e
	}
	_ = p.stdin.Close()
	e = p.wait(ctx)
	output := c.bytes()
	if c.exceeded() || c.stderr.exceeded() {
		e = errors.Join(e, errOutputLimit)
	}
	if e != nil && (strings.Contains(string(output), "module lookup disabled by GOPROXY=off") || strings.Contains(string(c.stderr.bytes()), "module lookup disabled by GOPROXY=off")) {
		e = errors.Join(e, errDependencyNotCached)
	}
	if e != nil {
		for _, known := range []string{"TLS handshake timeout", "i/o timeout", "no such host", "connection reset", "502 Bad Gateway", "503 Service Unavailable", "unexpected EOF"} {
			if strings.Contains(string(c.stderr.bytes()), known) {
				e = errors.Join(e, errDependencyNetwork)
				break
			}
		}
	}
	return output, c.stderr.bytes(), e
}

func run(ctx context.Context, dir string, env []string, program string, args ...string) ([]byte, error) {
	out, _, err := runWithStderr(ctx, dir, env, program, args...)
	return out, err
}
