package store

import (
	"time"
)

var processStartedAt = realProcessStartedAt

func formatProcessStartedAt(sec int64, usec int32) string {
	startedAt := time.Unix(sec, int64(usec)*1000).Local()
	return startedAt.Format("Mon Jan _2 15:04:05 2006") + "    \n"
}
