//go:build darwin

package daemonctl

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func realCodexMonitorProcessStartedAt(pid int) (time.Time, error) {
	if pid <= 0 {
		return time.Time{}, fmt.Errorf("pid must be positive: %d", pid)
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, fmt.Errorf("get process start time for pid %d: %w", pid, err)
	}
	return time.Unix(process.Proc.P_starttime.Sec, int64(process.Proc.P_starttime.Usec)*1000), nil
}
