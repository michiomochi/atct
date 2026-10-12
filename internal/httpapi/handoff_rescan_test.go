package httpapi_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

// The event streams poll every 100ms (handoffEntryPollInterval). These tests
// pin the scan count: one initial scan, one forced scan on the first tick, no
// scan while the entry watermark is flat, and exactly one scan per append.
const rescanIdle = 600 * time.Millisecond

func waitScans(t *testing.T, srv *httpapi.Server, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.HandoffEntryScans() < want {
		if time.Now().After(deadline) {
			t.Fatalf("handoff entry scans = %d, want %d", srv.HandoffEntryScans(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertScansStay(t *testing.T, srv *httpapi.Server, want int64, why string) {
	t.Helper()
	time.Sleep(rescanIdle)
	if got := srv.HandoffEntryScans(); got != want {
		t.Fatalf("handoff entry scans after %s = %d, want %d", why, got, want)
	}
}

// pinNoRescanOnFlatWatermark runs against an already-open stream: scans are 1
// (initial) + 1 (first tick) and stay there while idle; an append adds exactly
// one scan and then goes idle again.
func pinNoRescanOnFlatWatermark(t *testing.T, f *fixture, srv *httpapi.Server, handoffID string, receiver int64) {
	t.Helper()
	waitScans(t, srv, 2)
	assertScansStay(t, srv, 2, "idle ticks with a flat watermark")

	if _, err := f.store.AppendGoalHandoffEntry(f.ctx, handoffID, store.HandoffEntryKindReviewRequested, "advance the watermark", receiver, ""); err != nil {
		t.Fatalf("AppendGoalHandoffEntry: %v", err)
	}
	waitScans(t, srv, 3)
	assertScansStay(t, srv, 3, "one append followed by idle ticks")
}

func TestSSESkipsHandoffEntryRescanWhileWatermarkIsFlat(t *testing.T) {
	f := newBareFixture(t)
	handoff, receiver := createGoalHandoffForHTTPTest(t, f, "http-sse-rescan")
	api := httpapi.New(f.store)
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	stream, _ := openSSEStream(t, f.ctx, srv.Client(), eventsURLWithGoal(srv.URL, idText(f.goal.ID)))
	defer stream.Body.Close()

	pinNoRescanOnFlatWatermark(t, f, api, handoff.ID, receiver)
}

func TestWebSocketSkipsHandoffEntryRescanWhileWatermarkIsFlat(t *testing.T) {
	f := newBareFixture(t)
	handoff, receiver := createGoalHandoffForHTTPTest(t, f, "http-ws-rescan")
	api := httpapi.New(f.store)
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	conn := openWebSocket(t, websocketURL(srv.URL)+"?goal_id="+idText(f.goal.ID), nil)
	_ = conn

	pinNoRescanOnFlatWatermark(t, f, api, handoff.ID, receiver)
}
