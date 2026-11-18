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
	runFinished      []bool
	shutdownOnce     sync.Once
	shutdownDone     chan struct{}
	shutdownErrors   []error
	shutdownFinished []bool
	closeOnce        []sync.Once
	closeErrors      []error
}

// NewGroup validates and copies uniquely named services. It may start empty so
// a composition Hook can Add services after their resources become available.
// Run requires at least one member unless the group has already been stopped.
func NewGroup(members ...NamedService) (*Group, error) {
	g := &Group{runDone: make(chan struct{}), shutdownDone: make(chan struct{})}
	if err := g.Add(members...); err != nil {
		return nil, err
	}
	return g, nil
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
	g.runFinished = make([]bool, n)
	g.shutdownErrors = make([]error, n)
	g.shutdownFinished = make([]bool, n)
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
			g.runFinished[i] = true
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
			for i := range g.members {
				g.runFinished[i] = true
			}
			close(g.runDone)
		}
	}
}

// Shutdown invokes every child concurrently with the same context, then waits
// for shutdown callbacks and child Run calls within ctx. A member whose Shutdown
// fails is closed while siblings continue draining. For a nested Group that
// exhausts its budget, only pending members are force-closed. The first call owns the child
// shutdown context; later calls only wait using their own budgets. Errors retain
// member names and support errors.Is. Child stop panics become PanicError without
// interrupting sibling cleanup. Nil contexts are rejected.
func (g *Group) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("app: shutdown context is required")
	}
	return errors.Join(g.waitForDrain(ctx), g.diagnostics(nil))
}

func (g *Group) waitForDrain(ctx context.Context) error {
	g.stop()
	g.shutdownOnce.Do(func() {
		var wg sync.WaitGroup
		for i, m := range g.members {
			wg.Go(func() {
				err := runSafely(func() error { return drainService(ctx, m.Service) })
				if err != nil {
					g.mu.Lock()
					g.shutdownErrors[i] = fmt.Errorf("app: shutdown %s: %w", m.Name, err)
					g.mu.Unlock()
					g.forceMember(i)
				}
				g.mu.Lock()
				g.shutdownFinished[i] = true
				g.mu.Unlock()
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
	return waitErr
}

func (g *Group) diagnostics(reportedRunErr error) error {
	return g.collectDiagnostics(reportedRunErr, make(map[*Group]bool), false)
}

func (g *Group) collectDiagnostics(reportedRunErr error, parents map[*Group]bool, onlyClose bool) error {
	if parents[g] {
		return nil
	}
	parents[g] = true
	defer delete(parents, g)
	g.mu.Lock()
	errs := append([]error(nil), g.closeErrors...)
	var runErrors []error
	if !onlyClose {
		errs = append(errs, g.shutdownErrors...)
		runErrors = append([]error(nil), g.runErrors...)
	}
	members := g.members
	g.mu.Unlock()
	for i, member := range members {
		var runErr error
		if !onlyClose {
			runErr = runErrors[i]
			// Run returns the same wrapped member error stored in this slice.
			if runErr != nil && runErr != reportedRunErr {
				errs = append(errs, runErr)
			}
		}
		if child, ok := member.Service.(*Group); ok {
			if childErr := child.collectDiagnostics(errors.Unwrap(runErr), parents, onlyClose); childErr != nil {
				errs = append(errs, fmt.Errorf("app: group %s: %w", member.Name, childErr))
			}
		}
	}
	return errors.Join(errs...)
}

// On budget exhaustion, only members that still have work in flight need Close.
func (g *Group) closePending() {
	var wg sync.WaitGroup
	g.closeMembers(&wg, make(map[*Group]bool), true)
	wg.Wait()
}

func (g *Group) closeMembers(wg *sync.WaitGroup, seen map[*Group]bool, pendingOnly bool) {
	if seen[g] {
		return
	}
	seen[g] = true
	g.stop()
	g.mu.Lock()
	var pending []int
	for i := range g.members {
		if !pendingOnly || !g.runFinished[i] || !g.shutdownFinished[i] {
			pending = append(pending, i)
		}
	}
	g.mu.Unlock()
	for _, i := range pending {
		if child, ok := g.members[i].Service.(*Group); ok {
			child.closeMembers(wg, seen, pendingOnly)
		} else {
			wg.Go(func() { g.closeMember(i) })
		}
	}
}

func (g *Group) forceMember(i int) {
	if child, ok := g.members[i].Service.(*Group); ok {
		child.closePending()
		return
	}
	g.closeMember(i)
}

// Close calls each child's immediate Close at most once and caches all errors,
// including PanicError diagnostics from child panics.
// It does not wait for Run; each child Close must promptly unblock its Run.
func (g *Group) Close() error {
	var wg sync.WaitGroup
	g.closeMembers(&wg, make(map[*Group]bool), false)
	wg.Wait()
	return g.collectDiagnostics(nil, make(map[*Group]bool), true)
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
