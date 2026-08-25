//go:build linux

package store

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Linux UAPI's __USER_HZ is 100; it is distinct from the configurable kernel CONFIG_HZ.
const linuxUserHZ int64 = 100

func realProcessStartedAt(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pid must be positive: %d", pid)
	}

	startedAt, psErr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if psErr == nil && len(startedAt) > 0 {
		return string(startedAt), nil
	}

	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", fmt.Errorf("get process start time for pid %d: %w", pid, err)
	}
	bootTime, err := procBootTime()
	if err != nil {
		return "", fmt.Errorf("get process boot time: %w", err)
	}
	sec, usec, err := procStatStartTime(string(stat), bootTime, linuxUserHZ)
	if err != nil {
		return "", fmt.Errorf("parse process start time for pid %d: %w", pid, err)
	}
	return formatProcessStartedAt(sec, usec), nil
}

func procBootTime() (int64, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(stat), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		bootTime, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse btime %q: %w", fields[1], err)
		}
		return bootTime, nil
	}
	return 0, fmt.Errorf("/proc/stat has no btime")
}
