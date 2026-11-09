//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package httpserver

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// findProcessesOnPort uses lsof, which is available on the BSD family.
func findProcessesOnPort(port int) ([]int, error) {
	out, err := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// lsof exits with 1 when no process matches.
			return nil, nil
		}
		return nil, err
	}
	seen := map[int]bool{}
	pids := []int{}
	for _, line := range strings.Split(string(out), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 1 || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids, nil
}
