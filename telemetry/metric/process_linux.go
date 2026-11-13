package metric

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func processResources() (float64, uint64, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, 0, err
	}
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("metric: invalid process memory statistics")
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	seconds := float64(usage.Utime.Sec) + float64(usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
	return seconds, pages * uint64(os.Getpagesize()), nil
}
