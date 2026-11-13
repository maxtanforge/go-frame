//go:build windows

package dev

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func startProcess(dir, program string, args []string, stdout, stderr io.Writer, stopTimeout time.Duration) (process, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	cmd := command(dir, executable, append([]string{"__frame_child", program}, args...), stdout, stderr)
	gate, err := cmd.StdinPipe()
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = gate.Close()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	// The trusted helper only reads stdin until assignment succeeds. Arbitrary
	// app/build code cannot spawn a child outside this job during assignment.
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
		_ = windows.CloseHandle(handle)
	}
	if err != nil {
		_ = gate.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("assign process job: %w", err)
	}
	p := &commandProcess{cmd: cmd, done: make(chan struct{}), stopTimeout: stopTimeout, cleanup: func(timeout time.Duration) error {
		defer func() { _ = windows.CloseHandle(job) }()
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return err
		}
		return waitForDrain(timeout, func() (bool, error) {
			var accounting jobAccounting
			err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
			return accounting.ActiveProcesses == 0, err
		})
	}}
	_, err = gate.Write([]byte{1})
	_ = gate.Close()
	if err != nil {
		cleanErr := p.clean(stopTimeout)
		_ = cmd.Wait()
		return nil, errors.Join(err, cleanErr)
	}
	go p.finish()
	return p, nil
}

func (p *commandProcess) Stop(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if err := p.clean(timeout); err != nil {
		return err
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(time.Until(deadline)):
		return fmt.Errorf("process did not exit within %s", timeout)
	}
}

// Win32 JOBOBJECT_BASIC_ACCOUNTING_INFORMATION (four LARGE_INTEGERs and four DWORDs).
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// ChildMain is a private CLI entry point. It must run before ordinary parsing.
func ChildMain(args []string) int {
	if len(args) == 0 {
		return 2
	}
	var token [1]byte
	if _, err := io.ReadFull(os.Stdin, token[:]); err != nil || token[0] != 1 {
		return 2
	}
	cmd := command("", args[0], args[1:], os.Stdout, os.Stderr)
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "frame child:", err)
		return 1
	}
	return 0
}
