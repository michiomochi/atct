package store

import "time"

// timestampLayout keeps the fractional second at a fixed nine digits.
//
// These columns are TEXT and SQLite orders TEXT byte by byte, so the stored
// spelling is the sort key. RFC3339Nano drops trailing zeros, which makes that
// spelling vary in length, and then a shorter fraction is compared against the
// longer one's next digit: "…657Z" meets "…657337Z" at 'Z' versus '3', and 'Z'
// is the larger byte. The earlier time sorts last.
//
// Nine digits always, so text order and time order are the same order.
const timestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// formatTimestamp renders a time for storage.
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}
