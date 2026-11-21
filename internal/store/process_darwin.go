//go:build darwin

package store

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func realProcessStartedAt(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pid must be positive: %d", pid)
	}

	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("get process start time for pid %d: %w", pid, err)
	}
	return formatProcessStartedAt(process.Proc.P_starttime.Sec, process.Proc.P_starttime.Usec), nil
}
