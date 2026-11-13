package metric

import (
	"syscall"
	"unsafe"
)

var getProcessMemoryInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// Layout of PROCESS_MEMORY_COUNTERS; SIZE_T fields follow native pointer size.
type processMemoryCounters struct {
	Size, PageFaultCount                               uint32
	PeakWorkingSetSize, WorkingSetSize                 uintptr
	QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage       uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage uintptr
	PagefileUsage, PeakPagefileUsage                   uintptr
}

func processResources() (float64, uint64, error) {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, 0, err
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return 0, 0, err
	}
	if err := getProcessMemoryInfo.Find(); err != nil {
		return 0, 0, err
	}
	var memory processMemoryCounters
	memory.Size = uint32(unsafe.Sizeof(memory))
	result, _, callErr := getProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.Size))
	if result == 0 {
		return 0, 0, callErr
	}
	kernelTicks := uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)
	userTicks := uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime)
	return float64(kernelTicks+userTicks) / 1e7, uint64(memory.WorkingSetSize), nil
}
