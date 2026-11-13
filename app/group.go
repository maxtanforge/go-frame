package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/maxtanforge/go-frame/internal/isnil"
)

// NamedService identifies a critical member of a Group.
type NamedService struct {
	Name    string
	Service Service
}

// Group runs critical services concurrently, once. Any member exit causes Run
// to return so its owner can call Shutdown. Run alone does not drain siblings.
// Child Shutdown and Close callbacks must cooperate with the Service contract.
type Group struct {
	members          []NamedService
	mu               sync.Mutex
	started, stopped bool
	runDone          chan struct{}
	first            chan error
	runErrors        []error
	shutdownOnce     sync.Once
	shutdownDone     chan struct{}
	shutdownErrors   []error
	closeOnce        []sync.Once
	closeErrors      []error
}

// NewGroup validates and copies uniquely named services. It may start empty so
// a composition Hook can Add services after their resources become available.
// Run requires at least one member unless the group has already been stopped.
func NewGroup(members ...NamedService) (*Group, error) {
	if err := validateGroupMembers(nil, members); err != nil {
		return nil, err
	}
	return &Group{members: append([]NamedService(nil), members...), runDone: make(chan struct{}), first: make(chan error, len(members)), runErrors: make([]error, len(members)), shutdownDone: make(chan struct{}), shutdownErrors: make([]error, len(members)), closeOnce: make([]sync.Once, len(members)), closeErrors: make([]error, len(members))}, nil
}

func validateGroupMembers(existing, members []NamedService) error {
	names := make(map[string]bool, len(existing)+len(members))
	for _, m := range existing {
		names[m.Name] = true
	}
	for _, m := range members {
		if strings.TrimSpace(m.Name) == "" || names[m.Name] {
			return fmt.Errorf("app: service names must be nonempty and unique: %q", m.Name)
		}
		if isnil.Check(m.Service) {
			return fmt.Errorf("app: service %q is required", m.Name)
		}
		names[m.Name] = true
	}
	return nil
}

// Add registers critical services before Run or any stop call, for example in
// a composition Hook after resources start. Validation is atomic across a batch.
// Accepted services join the group's shutdown/close ownership even if startup
// later fails. Rejected services remain owned by the caller.
func (g *Group) Add(members ...NamedService) error {
	if g == nil {
		return errors.New("app: group is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.runDone == nil {
		return errors.New("app: group must be constructed with NewGroup")
	}
	if g.started || g.stopped {
		return errors.New("app: group registration is closed")
	}
	if err := validateGroupMembers(g.members, members); err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}
	g.members = append(g.members, members...)
	n := len(g.members)
	// No lifecycle callback has run, so rebuilding these empty slots is safe.
	g.first = make(chan error, n)
	g.runErrors = make([]error, n)
	g.shutdownErrors = make([]error, n)
	g.closeOnce = make([]sync.Once, n)
	g.closeErrors = make([]error, n)
	return nil
}

// Run starts every member and returns its first exit. A nil early exit is an
// error because each member is critical. Later errors are retained by Shutdown.
func (g *Group) Run() error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return errors.New("app: group Run may only be called once")
	}
	g.started = true
	if g.stopped {
		g.mu.Unlock()
		return nil
	}
	if len(g.members) == 0 {
		close(g.runDone)
		g.mu.Unlock()
		return errors.New("app: group requires services before Run")
	}
	var wg sync.WaitGroup
	for i, m := range g.members {
		wg.Go(func() {
			err := runSafely(m.Service.Run)
			g.mu.Lock()
			if err != nil {
				err = fmt.Errorf("app: run %s: %w", m.Name, err)
			} else if !g.stopped {
				err = fmt.Errorf("app: service %s exited before shutdown", m.Name)
			}
			g.runErrors[i] = err
			g.mu.Unlock()
			g.first <- err
		})
	}
	go func() { wg.Wait(); close(g.runDone) }()
	g.mu.Unlock()
	return <-g.first
}

func (g *Group) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.stopped {
		g.stopped = true
		if !g.started {
			close(g.runDone)
		}
	}
}

// Shutdown invokes every child concurrently with the same context, then waits
// for shutdown callbacks and all child Run calls. A child whose Shutdown fails
// is immediately closed while siblings continue draining. The first call owns the child
// shutdown context; later calls only wait using their own budgets. Errors retain
// member names and support errors.Is. Child stop panics become PanicError without
// interrupting sibling cleanup. Nil contexts are rejected.
func (g *Group) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("app: shutdown context is required")
	}
	g.stop()
	g.shutdownOnce.Do(func() {
		var wg sync.WaitGroup
		for i, m := range g.members {
			wg.Go(func() {
				err := runSafely(func() error { return m.Service.Shutdown(ctx) })
				if err != nil {
					g.mu.Lock()
					g.shutdownErrors[i] = fmt.Errorf("app: shutdown %s: %w", m.Name, err)
					g.mu.Unlock()
					g.closeMember(i)
				}
			})
		}
		go func() { wg.Wait(); close(g.shutdownDone) }()
	})
	var waitErr error
	for _, done := range []<-chan struct{}{g.shutdownDone, g.runDone} {
		select {
		case <-done:
			continue
		default:
		}
		select {
		case <-done:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
		if waitErr != nil {
			break
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	errs := append([]error{waitErr}, g.shutdownErrors...)
	errs = append(errs, g.runErrors...)
	errs = append(errs, g.closeErrors...)
	return errors.Join(errs...)
}

// Close calls each child's immediate Close at most once and caches all errors,
// including PanicError diagnostics from child panics.
// It does not wait for Run; each child Close must promptly unblock its Run.
func (g *Group) Close() error {
	g.stop()
	var wg sync.WaitGroup
	for i := range g.members {
		wg.Go(func() { g.closeMember(i) })
	}
	wg.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	return errors.Join(g.closeErrors...)
}

func (g *Group) closeMember(i int) {
	g.closeOnce[i].Do(func() {
		m := g.members[i]
		if err := runSafely(m.Service.Close); err != nil {
			g.mu.Lock()
			g.closeErrors[i] = fmt.Errorf("app: close %s: %w", m.Name, err)
			g.mu.Unlock()
		}
	})
}
