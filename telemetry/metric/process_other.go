//go:build !linux && !windows

package metric

import "errors"

func processResources() (float64, uint64, error) {
	return 0, 0, errors.New("metric: process resources unsupported on this platform")
}
