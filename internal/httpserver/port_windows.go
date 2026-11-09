//go:build windows

package httpserver

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// findProcessesOnPort parses netstat output for TCP listeners on port.
func findProcessesOnPort(port int) ([]int, error) {
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	pids := []int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[0], "TCP") || !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		local := fields[1]
		colon := strings.LastIndexByte(local, ':')
		if colon < 0 {
			continue
		}
		localPort, err := strconv.Atoi(local[colon+1:])
		if err != nil || localPort != port {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || pid <= 1 || pid == os.Getpid() || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids, nil
}

// terminateProcesses force kills the listening processes with taskkill.
func terminateProcesses(pids []int) error {
	for _, pid := range pids {
		if pid <= 1 || pid == os.Getpid() {
			continue
		}
		out, err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F", "/T").CombinedOutput()
		if err != nil {
			return fmt.Errorf("taskkill %d: %w: %s", pid, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
