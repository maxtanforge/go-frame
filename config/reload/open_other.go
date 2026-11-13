//go:build !windows

package reload

import "os"

func openWatchFile(path string) (*os.File, error) { return os.Open(path) }
