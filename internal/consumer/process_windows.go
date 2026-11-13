//go:build windows

package consumer

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformStart(dir string, env []string, out, stderr io.Writer, program string, args []string) (*child, error) {
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		_ = windows.CloseHandle(job)
		return nil, e
	}
	cmd := exec.Command(exe, append([]string{"__consumer_child", program}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	gate, e := cmd.StdinPipe()
	if e != nil {
		_ = windows.CloseHandle(job)
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		_ = gate.Close()
		_ = windows.CloseHandle(job)
		return nil, e
	}
	handle, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e == nil {
		e = windows.AssignProcessToJobObject(job, handle)
		_ = windows.CloseHandle(handle)
	}
	if e != nil {
		_ = gate.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, e
	}
	p := &child{cmd: cmd, stdin: gate, done: make(chan struct{}), clean: func() error {
		defer func() { _ = windows.CloseHandle(job) }()
		if e := windows.TerminateJobObject(job, 1); e != nil {
			return e
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			var accounting jobAccounting
			if e := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); e != nil {
				return e
			}
			if accounting.ActiveProcesses == 0 {
				return nil
			}
			if time.Now().After(deadline) {
				return errors.New("process tree did not drain")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}}
	if _, e = gate.Write([]byte{1}); e != nil {
		_ = p.cleanup()
		_ = cmd.Wait()
		return nil, e
	}
	return p, nil
}

// Win32 JOBOBJECT_BASIC_ACCOUNTING_INFORMATION layout.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// ChildMain waits until the helper belongs to a kill-on-close Job before launching code.
func ChildMain(args []string) int {
	if len(args) == 0 {
		return 2
	}
	var token [1]byte
	if _, e := io.ReadFull(os.Stdin, token[:]); e != nil || token[0] != 1 {
		return 2
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Run() != nil {
		return 1
	}
	return 0
}
func signalTerminate(*child) error { return os.ErrInvalid }
