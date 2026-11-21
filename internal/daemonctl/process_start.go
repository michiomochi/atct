package daemonctl

import "time"

var codexMonitorProcessStartedAt = realCodexMonitorProcessStartedAt

// CodexMonitorProcessStartTime returns the kernel-reported start time used to
// guard monitor signals against PID reuse.
func CodexMonitorProcessStartTime(pid int) (time.Time, error) {
	return codexMonitorProcessStartedAt(pid)
}
