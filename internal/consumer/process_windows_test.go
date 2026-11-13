//go:build windows

package consumer

import (
	"errors"
	"golang.org/x/sys/windows"
)

func descendantGone(pid int) bool {
	handle, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if e != nil {
		return errors.Is(e, windows.ERROR_INVALID_PARAMETER)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	status, e := windows.WaitForSingleObject(handle, 0)
	return e == nil && status == windows.WAIT_OBJECT_0
}
