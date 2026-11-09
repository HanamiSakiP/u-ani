//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package httpserver

import (
	"fmt"
	"runtime"
)

func findProcessesOnPort(port int) ([]int, error) {
	return nil, fmt.Errorf("finding the process using port %d is not supported on %s", port, runtime.GOOS)
}

func terminateProcesses(pids []int) error {
	return fmt.Errorf("terminating processes is not supported on %s", runtime.GOOS)
}
