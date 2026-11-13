//go:build !windows

package consumer

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
)

func descendantGone(pid int) bool {
	if syscall.Kill(pid, 0) == syscall.ESRCH {
		return true
	}
	// A reparented zombie is already dead; container PID 1 may reap it later.
	if runtime.GOOS == "linux" {
		data, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(e) {
			return true
		}
		if e == nil {
			index := strings.LastIndex(string(data), ") ")
			return index >= 0 && strings.HasPrefix(string(data[index+2:]), "Z ")
		}
	}
	return false
}
