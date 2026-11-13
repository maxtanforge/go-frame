//go:build !windows

package consumer

import (
	"io"
	"os/exec"
	"syscall"
	"time"
)

func platformStart(dir string, env []string, out, stderr io.Writer, program string, args []string) (*child, error) {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	gate, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		_ = gate.Close()
		return nil, e
	}
	return &child{cmd: cmd, stdin: gate, done: make(chan struct{}), clean: func() error {
		e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if e == syscall.ESRCH {
			return nil
		}
		return e
	}}, nil
}
func ChildMain([]string) int         { return 2 }
func signalTerminate(p *child) error { return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM) }
