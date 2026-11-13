package dev

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type commandProcess struct {
	cmd         *exec.Cmd
	done        chan struct{}
	err         error
	cleanup     func(time.Duration) error
	cleanupErr  error
	drainErr    error
	cleanDone   chan struct{}
	stopTimeout time.Duration
	once        sync.Once
}

func (p *commandProcess) Done() <-chan struct{} { return p.done }
func (p *commandProcess) Err() error            { <-p.done; return p.err }
func (p *commandProcess) clean(timeout time.Duration) error {
	p.once.Do(func() {
		p.cleanDone = make(chan struct{})
		go func() { p.drainErr = p.cleanup(timeout); close(p.cleanDone) }()
	})
	select {
	case <-p.cleanDone:
		return p.drainErr
	case <-time.After(timeout):
		return fmt.Errorf("process cleanup exceeded %s", timeout)
	}
}
func (p *commandProcess) finish() {
	p.err = p.cmd.Wait()
	p.cleanupErr = p.clean(p.stopTimeout)
	p.err = errors.Join(p.err, p.cleanupErr)
	close(p.done)
}

func waitForDrain(timeout time.Duration, empty func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		done, err := empty()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("process tree did not drain within %s", timeout)
		}
		time.Sleep(min(10*time.Millisecond, remaining))
	}
}
func command(dir, program string, args []string, stdout, stderr io.Writer) *exec.Cmd {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	return cmd
}
