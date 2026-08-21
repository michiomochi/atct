package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

// Runs against a copy of a real database when ATCT_REAL_DB points at one. The
// unit tests build their own fixtures, and fixtures are what let the original
// defect through -- a goal with no unstarted tasks never reached the publisher,
// and no fixture had that shape.
func TestDetectionAgainstRealDatabaseCopy(t *testing.T) {
	path := os.Getenv("ATCT_REAL_DB")
	if path == "" {
		t.Skip("set ATCT_REAL_DB to a copy of a real database")
	}
	goalID := os.Getenv("ATCT_REAL_GOAL")
	if goalID == "" {
		t.Skip("set ATCT_REAL_GOAL to the goal expected to be reported")
	}

	ctx := context.Background()
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	tracker := newWakeupTracker()
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	if _, err := tracker.evaluate(ctx, s, start); err != nil {
		t.Fatalf("initial evaluate: %v", err)
	}
	events, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter))
	if err != nil {
		t.Fatalf("publish evaluate: %v", err)
	}

	if _, ok := findDetectionEvent(events, store.EventDetectionCompletionReportMissing, goalID); !ok {
		for _, event := range events {
			t.Logf("published %s %#v", event.Name, event.Data)
		}
		t.Fatalf("no completion detection for %s", goalID)
	}
	t.Logf("published %d events; completion detection present for %s", len(events), goalID)

	// Converges: asking again without the condition changing says nothing new.
	again, err := tracker.evaluate(ctx, s, start.Add(wakeupPublishAfter+time.Minute))
	if err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if _, ok := findDetectionEvent(again, store.EventDetectionCompletionReportMissing, goalID); ok {
		t.Fatalf("completion detection repeated for %s", goalID)
	}
}
