package store

import (
	"testing"
	"time"
)

// Timestamps are stored as RFC3339Nano text and ordered as text. That format
// trims trailing zeros, so two stamps in the same second can have fractions of
// different lengths, and then 'Z' is compared against a digit: 'Z' is larger,
// so the shorter fraction sorts last however early it actually was.
//
// No production row hits this today, and every ORDER BY on these columns is
// one prefix collision away from reversing two rows.
func TestRFC3339NanoTextOrderCanReverseTwoTimes(t *testing.T) {
	base := time.Date(2026, 9, 19, 14, 22, 31, 657000000, time.UTC)
	later := base.Add(337 * time.Microsecond)

	earlierText := base.Format(time.RFC3339Nano)
	laterText := later.Format(time.RFC3339Nano)

	if !base.Before(later) {
		t.Fatal("the fixture is not ordered in time")
	}
	if earlierText < laterText {
		t.Skipf("text order agrees with time order for %q and %q", earlierText, laterText)
	}
	t.Logf("text order disagrees with time order: %q sorts after %q", earlierText, laterText)
}
