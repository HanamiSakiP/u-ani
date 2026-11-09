package httpserver

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/logger"
)

// portReleaseTimeout is how long ensurePortFree waits for an old process to
// release the port before giving up.
const portReleaseTimeout = 5 * time.Second

// Stop terminates the process(es) listening on port and returns their PIDs.
func Stop(port int) ([]int, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	pids, err := findProcessesOnPort(port)
	if err != nil {
		return nil, fmt.Errorf("find process listening on port %d: %w", port, err)
	}
	if len(pids) == 0 {
		return nil, nil
	}
	if err := terminateProcesses(pids); err != nil {
		return pids, fmt.Errorf("stop process(es) %v: %w", pids, err)
	}
	return pids, nil
}

// ensurePortFree makes sure address:port can be bound. When the port is held by
// an old instance, that process is terminated and the bind is retried.
func ensurePortFree(addr string, port int) error {
	if port <= 0 || port > 65535 {
		return nil
	}

	deadline := time.Now().Add(portReleaseTimeout)
	for {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			_ = listener.Close()
			return nil
		}
		if !isAddressInUse(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("port %d is still in use: %w", port, err)
		}

		pids, findErr := findProcessesOnPort(port)
		if findErr != nil {
			return fmt.Errorf("port %d is already in use and the old process could not be identified: %w", port, findErr)
		}
		if len(pids) == 0 {
			// The listener may belong to a process we cannot inspect, or the
			// socket is still being released by the kernel. Wait and retry.
			time.Sleep(200 * time.Millisecond)
			continue
		}

		logger.Warning("port %d is already in use, stopping old process(es): %v", port, pids)
		if killErr := terminateProcesses(pids); killErr != nil {
			return fmt.Errorf("port %d is already in use and the old process could not be stopped: %w", port, killErr)
		}
	}
}

// isAddressInUse reports whether err was caused by a port that is already
// bound. The string checks keep this working across operating systems without
// depending on platform-specific errno constants.
func isAddressInUse(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "address in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}
