//go:build !darwin

package store

import (
	"fmt"
	"os/exec"
	"strconv"
)

func realProcessStartedAt(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pid must be positive: %d", pid)
	}

	startedAt, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", fmt.Errorf("get process start time for pid %d: %w", pid, err)
	}
	if len(startedAt) == 0 {
		return "", fmt.Errorf("process %d has no start time", pid)
	}
	return string(startedAt), nil
}
