//go:build linux

package httpserver

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// findProcessesOnPort returns the PIDs listening on port by matching the
// socket inodes from /proc/net/tcp with the file descriptors of each process.
func findProcessesOnPort(port int) ([]int, error) {
	inodes, err := listenSocketInodes(port)
	if err != nil {
		return nil, err
	}
	if len(inodes) == 0 {
		return nil, nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	self := os.Getpid()
	seen := map[int]bool{}
	pids := []int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == self || seen[pid] {
			continue
		}
		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if _, ok := inodes[link]; !ok {
				continue
			}
			seen[pid] = true
			pids = append(pids, pid)
			break
		}
	}
	return pids, nil
}

// listenSocketInodes returns the socket:[inode] link targets of all TCP
// listeners on port.
func listenSocketInodes(port int) (map[string]struct{}, error) {
	inodes := map[string]struct{}{}
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			local := fields[1]
			colon := strings.LastIndexByte(local, ':')
			if colon < 0 {
				continue
			}
			localPort, err := strconv.ParseUint(local[colon+1:], 16, 16)
			if err != nil || int(localPort) != port {
				continue
			}
			inode, err := strconv.ParseUint(fields[9], 10, 64)
			if err != nil || inode == 0 {
				continue
			}
			inodes["socket:["+strconv.FormatUint(inode, 10)+"]"] = struct{}{}
		}
	}
	return inodes, nil
}
