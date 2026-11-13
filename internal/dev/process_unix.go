//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package dev

import (
	"fmt"
	"io"
	"syscall"
	"time"
)

func startProcess(dir, program string, args []string, stdout, stderr io.Writer, stopTimeout time.Duration) (process, error) {
	cmd := command(dir, program, args, stdout, stderr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &commandProcess{cmd: cmd, done: make(chan struct{}), stopTimeout: stopTimeout, cleanup: func(timeout time.Duration) error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return err
		}
		return waitForDrain(timeout, func() (bool, error) {
			err := syscall.Kill(-cmd.Process.Pid, 0)
			if err == syscall.ESRCH {
				return true, nil
			}
			return false, err
		})
	}}
	go p.finish()
	return p, nil
}

func (p *commandProcess) Stop(timeout time.Duration) error {
	select {
	case <-p.done:
		return p.cleanupErr
	default:
	}
	// ESRCH means the process group is already gone; anything else (EPERM 等)
	// would leave Stop waiting on a signal that was never delivered.
	if killErr := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGINT); killErr != nil && killErr != syscall.ESRCH {
		return killErr
	}
	deadline := time.Now().Add(timeout)
	timer := time.NewTimer(timeout * 9 / 10)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.cleanupErr
	case <-timer.C:
		if err := p.clean(time.Until(deadline)); err != nil {
			return err
		}
	}
	// Reserve the final tenth of the budget for forced termination and reaping.
	select {
	case <-p.done:
		return p.cleanupErr
	case <-time.After(time.Until(deadline)):
		return fmt.Errorf("process did not exit after SIGKILL")
	}
}

func ChildMain([]string) int { return 2 }
