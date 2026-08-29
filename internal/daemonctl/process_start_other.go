//go:build !darwin && !linux

package daemonctl

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func realCodexMonitorProcessStartedAt(pid int) (time.Time, error) {
	if pid <= 0 {
		return time.Time{}, fmt.Errorf("pid must be positive: %d", pid)
	}
	raw, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("get process start time for pid %d: %w", pid, err)
	}
	startedAt, err := time.Parse("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(raw)))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse process start time for pid %d: %w", pid, err)
	}
	return startedAt, nil
}
