package store

import (
	"testing"
	"time"
)

// The stored spelling is the sort key, so two times must compare as text the
// way they compare as time. RFC3339Nano did not: it trims trailing zeros, and
// a shorter fraction meets the longer one's digit at 'Z', which is larger.
func TestFormatTimestampSortsTheSameWayTimeDoes(t *testing.T) {
	base := time.Date(2026, 9, 19, 14, 22, 31, 657000000, time.UTC)
	cases := []struct {
		name  string
		later time.Time
	}{
		{"a prefix apart", base.Add(337 * time.Microsecond)},
		{"a nanosecond apart", base.Add(time.Nanosecond)},
		{"a whole second apart", base.Add(time.Second)},
		{"across a minute", base.Add(time.Minute)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			earlier, later := formatTimestamp(base), formatTimestamp(tc.later)
			if !(earlier < later) {
				t.Fatalf("%q does not sort before %q, though it is the earlier time", earlier, later)
			}
			if len(earlier) != len(later) {
				t.Fatalf("stored lengths differ: %d and %d; a varying length is what breaks the order", len(earlier), len(later))
			}
		})
	}
}

// RFC3339Nano still has to parse what is written, since every read uses it.
func TestFormatTimestampIsReadableAsRFC3339Nano(t *testing.T) {
	want := time.Date(2026, 9, 19, 14, 22, 31, 657000000, time.UTC)
	got, err := time.Parse(time.RFC3339Nano, formatTimestamp(want))
	if err != nil {
		t.Fatalf("stored timestamp does not parse: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("round trip = %s, want %s", got, want)
	}
}
