//go:build !windows

package httpserver

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// terminateProcesses asks each process to exit with SIGTERM, then force kills
// anything that is still running after a short grace period.
func terminateProcesses(pids []int) error {
	self := os.Getpid()
	alive := map[int]bool{}
	for _, pid := range pids {
		if pid <= 1 || pid == self {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("signal process %d: %w", pid, err)
		}
		alive[pid] = true
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(alive) > 0 && time.Now().Before(deadline) {
		for pid := range alive {
			if syscall.Kill(pid, 0) != nil {
				delete(alive, pid)
			}
		}
		if len(alive) == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	for pid := range alive {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill process %d: %w", pid, err)
		}
	}
	return nil
}
