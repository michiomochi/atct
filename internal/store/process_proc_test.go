package store

import (
	"strings"
	"testing"
)

func TestProcStatStartTimeHandlesNestedComm(t *testing.T) {
	statLine := "1234 (my (weird) proc) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19"

	sec, usec, err := procStatStartTime(statLine, 1000, 100)
	if err != nil {
		t.Fatalf("procStatStartTime: %v", err)
	}
	if sec != 1000 || usec != 190000 {
		t.Fatalf("procStatStartTime = (%d, %d); want (1000, 190000)", sec, usec)
	}
}

func TestProcStatStartTimeConvertsTicksToWallTime(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		btime    int64
		userHZ   int64
		wantSec  int64
		wantUsec int32
	}{
		{
			name:     "hundred hertz",
			line:     "1234 (proc) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 123456",
			btime:    1700000000,
			userHZ:   100,
			wantSec:  1700001234,
			wantUsec: 560000,
		},
		{
			name:     "thousand hertz",
			line:     "1234 (proc) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 2501",
			btime:    1700000000,
			userHZ:   1000,
			wantSec:  1700000002,
			wantUsec: 501000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sec, usec, err := procStatStartTime(tt.line, tt.btime, tt.userHZ)
			if err != nil {
				t.Fatalf("procStatStartTime: %v", err)
			}
			if sec != tt.wantSec || usec != tt.wantUsec {
				t.Fatalf("procStatStartTime = (%d, %d); want (%d, %d)", sec, usec, tt.wantSec, tt.wantUsec)
			}

			formatted := formatProcessStartedAt(sec, usec)
			if len(formatted) != 29 || !strings.HasSuffix(formatted, "    \n") {
				t.Fatalf("formatProcessStartedAt = %q; want 29-byte ps layout", formatted)
			}
		})
	}
}

func TestProcStatStartTimeRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{
			name: "missing fields",
			line: "1234 (proc) S 1 2 3",
		},
		{
			name: "non-numeric start time",
			line: "1234 (proc) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 nope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sec, usec, err := procStatStartTime(tt.line, 1700000000, 100)
			if err == nil {
				t.Fatal("procStatStartTime returned nil error")
			}
			if sec != 0 || usec != 0 {
				t.Fatalf("procStatStartTime = (%d, %d) on error; want zero values", sec, usec)
			}
		})
	}
}
