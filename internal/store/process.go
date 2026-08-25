package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var processStartedAt = realProcessStartedAt

func formatProcessStartedAt(sec int64, usec int32) string {
	startedAt := time.Unix(sec, int64(usec)*1000).Local()
	return startedAt.Format("Mon Jan _2 15:04:05 2006") + "    \n"
}

func procStatStartTime(statLine string, bootTime, userHZ int64) (int64, int32, error) {
	if userHZ <= 0 {
		return 0, 0, fmt.Errorf("user hertz must be positive: %d", userHZ)
	}

	commEnd := strings.LastIndex(statLine, ")")
	if commEnd < 0 {
		return 0, 0, fmt.Errorf("process stat has no command terminator")
	}
	fields := strings.Fields(statLine[commEnd+1:])
	const startTimeIndex = 19 // field 22, after state (field 3)
	if len(fields) <= startTimeIndex {
		return 0, 0, fmt.Errorf("process stat has no start time field")
	}

	ticks, err := strconv.ParseInt(fields[startTimeIndex], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse process start time %q: %w", fields[startTimeIndex], err)
	}
	if ticks < 0 {
		return 0, 0, fmt.Errorf("process start time is negative: %d", ticks)
	}

	sec := bootTime + ticks/userHZ
	usec := int32((ticks % userHZ) * 1_000_000 / userHZ)
	return sec, usec, nil
}
