package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSnapshotFailureNotifierWaitsTwoMinutes(t *testing.T) {
	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if _, due := n.Failure(t0, "snapshot timeout after 5s"); due {
		t.Fatal("a failure line was due at the first failure")
	}
	if _, due := n.Failure(t0.Add(time.Minute+59*time.Second), "snapshot timeout after 5s"); due {
		t.Fatal("a failure line was due before two minutes")
	}
	line, due := n.Failure(t0.Add(2*time.Minute), "snapshot timeout after 5s")
	if !due {
		t.Fatal("no failure line at two minutes")
	}
	want := "ATCT: daemon snapshot failing for 2m: snapshot timeout after 5s"
	if line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
}

func TestSnapshotFailureNotifierRepeatsSameReasonEveryThirtyMinutes(t *testing.T) {
	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	reason := "snapshot timeout after 5s"
	n.Failure(t0, reason)
	if _, due := n.Failure(t0.Add(2*time.Minute), reason); !due {
		t.Fatal("first line missing")
	}
	for _, offset := range []time.Duration{10 * time.Minute, 29*time.Minute + 59*time.Second} {
		if _, due := n.Failure(t0.Add(offset), reason); due {
			t.Fatalf("same reason repeated at %s, before thirty minutes", offset)
		}
	}
	if _, due := n.Failure(t0.Add(32*time.Minute), reason); !due {
		t.Fatal("same reason not repeated after thirty minutes")
	}
}

func TestSnapshotFailureNotifierSaysChangedReasonAtOnce(t *testing.T) {
	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	n.Failure(t0, "snapshot timeout after 5s")
	n.Failure(t0.Add(2*time.Minute), "snapshot timeout after 5s")
	line, due := n.Failure(t0.Add(3*time.Minute), "connection refused (GET http://127.0.0.1:8787/api/inbox)")
	if !due {
		t.Fatal("changed reason waited instead of being said at once")
	}
	if !strings.Contains(line, "connection refused") || !strings.Contains(line, "failing for 3m") {
		t.Fatalf("line = %q", line)
	}
}

func TestSnapshotFailureNotifierCollapsesLineBreaks(t *testing.T) {
	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	n.Failure(t0, "snapshot timeout\nafter 5s\r\n")
	line, due := n.Failure(t0.Add(2*time.Minute), "snapshot timeout\nafter 5s\r\n")
	if !due || strings.ContainsAny(line, "\r\n") || !strings.HasSuffix(line, "snapshot timeout after 5s") {
		t.Fatalf("line = %q (due %v), want one line ending in the reason with spaces", line, due)
	}
}

func TestSnapshotFailureNotifierRecoveryIsSaidOnlyAfterFailureWasSaid(t *testing.T) {
	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	n.Failure(t0, "snapshot timeout after 5s")
	if _, recovered := n.Success(t0.Add(time.Minute)); recovered {
		t.Fatal("recovery said for a failure that was never said")
	}

	n.Failure(t0, "snapshot timeout after 5s")
	n.Failure(t0.Add(2*time.Minute), "snapshot timeout after 5s")
	line, recovered := n.Success(t0.Add(7*time.Minute + 30*time.Second))
	if !recovered || line != "ATCT: daemon snapshot recovered after 7m30s" {
		t.Fatalf("recovery = %q (%v), want the period from the first failure", line, recovered)
	}

	// A success resets the run: the next failure counts two minutes from itself.
	t1 := t0.Add(10 * time.Minute)
	if _, due := n.Failure(t1, "snapshot timeout after 5s"); due {
		t.Fatal("a new run said its failure at once")
	}
	if _, due := n.Failure(t1.Add(2*time.Minute), "snapshot timeout after 5s"); !due {
		t.Fatal("a new run did not say its failure after two minutes")
	}
}

func TestWatchLoopSaysSnapshotFailureAfterTwoMinutes(t *testing.T) {
	fake := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	previous := snapshotFailureNow
	snapshotFailureNow = func() time.Time {
		fake = fake.Add(time.Minute)
		return fake
	}
	t.Cleanup(func() { snapshotFailureNow = previous })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	snapshot := func(context.Context) (string, []watchDecision, error) {
		calls++
		if calls == 6 {
			cancel()
		}
		return "", nil, errors.New("snapshot timeout after 5s (GET http://127.0.0.1:8787/api/inbox): context deadline exceeded")
	}
	var out bytes.Buffer
	if err := watchLoop(ctx, &out, nil, time.Millisecond, snapshot); err != nil {
		t.Fatalf("watchLoop: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "ATCT: daemon snapshot") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("snapshot lines = %q, want exactly one after the run passed two minutes", lines)
	}
	if !strings.HasPrefix(lines[0], "ATCT: daemon snapshot failing for 2m: snapshot timeout after 5s") {
		t.Fatalf("line = %q", lines[0])
	}
}

// fakeSnapshotClock makes every reading one minute after the last, from start.
func fakeSnapshotClock(t *testing.T, start time.Time) {
	t.Helper()
	previous := snapshotFailureNow
	fake := start
	snapshotFailureNow = func() time.Time {
		fake = fake.Add(time.Minute)
		return fake
	}
	t.Cleanup(func() { snapshotFailureNow = previous })
}

func TestWatchLoopSnapshotNoticesReachActionSink(t *testing.T) {
	fakeSnapshotClock(t, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Three failures pass two minutes, then one success, then the watch is stopped.
	calls := 0
	snapshot := func(context.Context) (string, []watchDecision, error) {
		calls++
		if calls == 5 {
			cancel()
		}
		if calls <= 3 {
			return "", nil, errors.New("snapshot timeout after 5s (GET http://127.0.0.1:8787/api/inbox): context deadline exceeded")
		}
		// The reconcile that follows goes to a closed port and fails quickly.
		return "http://127.0.0.1:1", nil, nil
	}
	var actions []watchAgentAction
	sink := func(action watchAgentAction) error {
		actions = append(actions, action)
		return nil
	}
	var out bytes.Buffer
	scope := watchScope{Role: "executor", ProjectID: "1", GoalID: "423", TaskID: "1701"}
	if err := watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(ctx, &out, &http.Client{}, time.Millisecond, snapshot, nil, nil, scope, nil, sink); err != nil {
		t.Fatalf("watch loop: %v", err)
	}
	if strings.Contains(out.String(), "ATCT: daemon snapshot") {
		t.Fatalf("bound watch wrote a snapshot notice to its output: %q", out.String())
	}
	if len(actions) != 2 {
		t.Fatalf("actions = %d, want the failure and the recovery", len(actions))
	}
	failing, recovered := actions[0], actions[1]
	if failing.eventName != watchSnapshotFailingEvent || failing.line != "ATCT: daemon snapshot failing for 2m: snapshot timeout after 5s (GET http://127.0.0.1:8787/api/inbox): context deadline exceeded" {
		t.Fatalf("failing action = %+v", failing)
	}
	if failing.targetRole != "executor" || failing.goalID != "423" || failing.taskID != "1701" || failing.controlOnly {
		t.Fatalf("failing action identity = %+v", failing)
	}
	if recovered.eventName != watchSnapshotRecoveredEvent || !strings.HasPrefix(recovered.line, "ATCT: daemon snapshot recovered after 3m") {
		t.Fatalf("recovered action = %+v", recovered)
	}
	if recovered.generation == failing.generation || recovered.deliveryKey == failing.deliveryKey {
		t.Fatalf("recovery shares a delivery identity with the failure: %+v / %+v", recovered, failing)
	}
}

func TestWatchLoopSnapshotNoticeWritesOutputWithoutActionSink(t *testing.T) {
	fakeSnapshotClock(t, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	snapshot := func(context.Context) (string, []watchDecision, error) {
		calls++
		if calls == 6 {
			cancel()
		}
		return "", nil, errors.New("connection refused (GET http://127.0.0.1:8787/api/inbox)")
	}
	var out bytes.Buffer
	if err := watchLoop(ctx, &out, &http.Client{}, time.Millisecond, snapshot); err != nil {
		t.Fatalf("watch loop: %v", err)
	}
	if n := strings.Count(out.String(), "ATCT: daemon snapshot failing for 2m: connection refused"); n != 1 {
		t.Fatalf("failure line written %d times, want once; output %q", n, out.String())
	}
}

func TestWatchOnceDeliversSnapshotNoticeOncePerRepeatInterval(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	clock := now
	record := newWatchOnce(dir, "token-a", nil, func() time.Time { return clock })
	var delivered []string
	sink := record.Sink(func(action watchAgentAction) error {
		delivered = append(delivered, action.line)
		return nil
	})
	failing := func(reason string) watchAgentAction {
		return watchAgentAction{line: "failing " + reason, eventName: watchSnapshotFailingEvent, deliveryKey: watchSnapshotFailingEvent + "\x00executor\x00" + reason, generation: "current"}
	}

	if err := sink(failing("timeout")); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(10 * time.Minute)
	if err := sink(failing("timeout")); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 {
		t.Fatalf("same reason re-delivered before thirty minutes: %v", delivered)
	}
	clock = now.Add(31 * time.Minute)
	if err := sink(failing("timeout")); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 2 {
		t.Fatalf("same reason not re-delivered after thirty minutes: %v", delivered)
	}
	if err := sink(failing("connection refused")); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 3 {
		t.Fatalf("changed reason not delivered at once: %v", delivered)
	}

	// A new process reads the record: the same reason is still inside its interval.
	reopened := newWatchOnce(dir, "token-a", nil, func() time.Time { return clock })
	var again []string
	reopenedSink := reopened.Sink(func(action watchAgentAction) error {
		again = append(again, action.line)
		return nil
	})
	if err := reopenedSink(failing("connection refused")); err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("a re-armed watch re-delivered a failure it had already delivered: %v", again)
	}
}

func TestWatchOnceDeliversEachSnapshotRecovery(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	record := newWatchOnce(dir, "token-b", nil, func() time.Time { return now })
	var delivered int
	sink := record.Sink(func(watchAgentAction) error {
		delivered++
		return nil
	})
	recovery := func(generation string) watchAgentAction {
		return watchAgentAction{line: "ATCT: daemon snapshot recovered after 3m", eventName: watchSnapshotRecoveredEvent, deliveryKey: watchSnapshotRecoveredEvent + "\x00executor\x00", generation: generation}
	}
	if err := sink(recovery("1")); err != nil {
		t.Fatal(err)
	}
	if err := sink(recovery("2")); err != nil {
		t.Fatal(err)
	}
	if delivered != 2 {
		t.Fatalf("recoveries delivered = %d, want 2", delivered)
	}
}
