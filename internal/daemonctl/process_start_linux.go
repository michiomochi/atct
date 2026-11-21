//go:build linux

package daemonctl

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func realCodexMonitorProcessStartedAt(pid int) (time.Time, error) {
	if pid <= 0 {
		return time.Time{}, fmt.Errorf("pid must be positive: %d", pid)
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, fmt.Errorf("get process start time for pid %d: %w", pid, err)
	}
	commEnd := strings.LastIndex(string(stat), ")")
	if commEnd < 0 {
		return time.Time{}, fmt.Errorf("process %d stat has no command terminator", pid)
	}
	fields := strings.Fields(string(stat)[commEnd+1:])
	const startTimeIndex = 19 // field 22, after state (field 3)
	if len(fields) <= startTimeIndex {
		return time.Time{}, fmt.Errorf("process %d stat has no start time field", pid)
	}
	ticks, err := strconv.ParseInt(fields[startTimeIndex], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse process %d start time %q: %w", pid, fields[startTimeIndex], err)
	}
	if ticks < 0 {
		return time.Time{}, fmt.Errorf("process %d start time is negative: %d", pid, ticks)
	}
	boot, err := linuxCodexMonitorBootTime()
	if err != nil {
		return time.Time{}, err
	}
	const userHZ int64 = 100
	return time.Unix(boot+ticks/userHZ, (ticks%userHZ)*10_000_000), nil
}

func linuxCodexMonitorBootTime() (int64, error) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, fmt.Errorf("read process boot time: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		boot, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse process boot time: %w", err)
		}
		return boot, nil
	}
	return 0, errors.New("process boot time is unavailable")
}
