package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

type onceHarness struct {
	delivered []watchAgentAction
	cancelled atomic.Int32
	cancel    context.CancelFunc
	once      *watchOnce
	sink      watchAgentActionSink
}

func newOnceHarness(t *testing.T, dir, token string, now func() time.Time) *onceHarness {
	t.Helper()
	h := &onceHarness{}
	h.cancel = func() { h.cancelled.Add(1) }
	h.once = newWatchOnce(dir, token, h.cancel, now)
	h.sink = h.once.Sink(func(a watchAgentAction) error {
		h.delivered = append(h.delivered, a)
		return nil
	})
	return h
}

func (h *onceHarness) waitCancelled(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for h.cancelled.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("watch was not cancelled after the grace period")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func onceAction(key, generation string) watchAgentAction {
	return watchAgentAction{line: "line " + key, eventName: "task.handoff.request", deliveryKey: key, generation: generation}
}

func TestWatchOnceDeliversFirstBatchThenCancels(t *testing.T) {
	h := newOnceHarness(t, t.TempDir(), "tok", time.Now)
	if h.once.Fired() {
		t.Fatal("Fired before any delivery")
	}
	if err := h.sink(onceAction("a", "1")); err != nil {
		t.Fatal(err)
	}
	if !h.once.Fired() {
		t.Fatal("Fired = false after delivery")
	}
	if h.cancelled.Load() != 0 {
		t.Fatal("cancelled before the grace period")
	}
	if err := h.sink(onceAction("b", "1")); err != nil {
		t.Fatal(err)
	}
	if len(h.delivered) != 2 {
		t.Fatalf("delivered %d actions in the grace period, want 2", len(h.delivered))
	}
	h.waitCancelled(t)
	time.Sleep(700 * time.Millisecond)
	if n := h.cancelled.Load(); n != 1 {
		t.Fatalf("cancel called %d times, want 1", n)
	}
}

func TestWatchOnceDropsRecordedActionsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	first := newOnceHarness(t, dir, "tok", time.Now)
	if err := first.sink(onceAction("a", "1")); err != nil {
		t.Fatal(err)
	}

	second := newOnceHarness(t, dir, "tok", time.Now)
	if err := second.sink(onceAction("a", "1")); err != nil {
		t.Fatal(err)
	}
	if len(second.delivered) != 0 || second.once.Fired() {
		t.Fatalf("recorded action was delivered again: %#v", second.delivered)
	}
	if err := second.sink(onceAction("a", "2")); err != nil {
		t.Fatal(err)
	}
	if len(second.delivered) != 1 {
		t.Fatalf("new generation delivered %d times, want 1", len(second.delivered))
	}

	other := newOnceHarness(t, dir, "other-token", time.Now)
	if err := other.sink(onceAction("a", "1")); err != nil {
		t.Fatal(err)
	}
	if len(other.delivered) != 1 {
		t.Fatal("a different token must not share the record")
	}
}

func TestWatchOnceControlOnlyIsNeitherRecordedNorTrigger(t *testing.T) {
	dir := t.TempDir()
	h := newOnceHarness(t, dir, "tok", time.Now)
	control := onceAction("c", "1")
	control.controlOnly = true
	if err := h.sink(control); err != nil {
		t.Fatal(err)
	}
	if len(h.delivered) != 1 {
		t.Fatalf("controlOnly must reach the next sink, delivered %d", len(h.delivered))
	}
	if h.once.Fired() {
		t.Fatal("controlOnly set Fired")
	}
	time.Sleep(700 * time.Millisecond)
	if h.cancelled.Load() != 0 {
		t.Fatal("controlOnly triggered cancel")
	}
	// Not recorded: a second instance still passes it on.
	again := newOnceHarness(t, dir, "tok", time.Now)
	if err := again.sink(control); err != nil {
		t.Fatal(err)
	}
	if len(again.delivered) != 1 {
		t.Fatal("controlOnly was deduplicated")
	}
}

func TestWatchOnceLivenessExpires(t *testing.T) {
	dir := t.TempDir()
	clock := time.Unix(1_000_000, 0)
	now := func() time.Time { return clock }
	live := watchAgentAction{line: "alive", eventName: "monitor.liveness", deliveryKey: "live", generation: "g"}

	first := newOnceHarness(t, dir, "tok", now)
	if err := first.sink(live); err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(watchLivenessRepeatInterval - time.Second)
	young := newOnceHarness(t, dir, "tok", now)
	if err := young.sink(live); err != nil {
		t.Fatal(err)
	}
	if len(young.delivered) != 0 {
		t.Fatal("liveness repeated before the interval")
	}

	clock = clock.Add(time.Second)
	old := newOnceHarness(t, dir, "tok", now)
	if err := old.sink(live); err != nil {
		t.Fatal(err)
	}
	if len(old.delivered) != 1 {
		t.Fatal("liveness not delivered after the interval")
	}
}

func TestWatchOnceCorruptRecordIsEmpty(t *testing.T) {
	dir := t.TempDir()
	probe := newWatchOnce(dir, "tok", func() {}, time.Now)
	for _, content := range []string{"", "{not json"} {
		if err := os.WriteFile(probe.path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		h := newOnceHarness(t, dir, "tok", time.Now)
		if err := h.sink(onceAction("a", "1")); err != nil {
			t.Fatal(err)
		}
		if len(h.delivered) != 1 {
			t.Fatalf("content %q: delivered %d, want 1", content, len(h.delivered))
		}
		raw, err := os.ReadFile(probe.path)
		if err != nil {
			t.Fatal(err)
		}
		var rec map[string]int64
		if err := json.Unmarshal(raw, &rec); err != nil || len(rec) != 1 {
			t.Fatalf("content %q: record not rewritten: %q (%v)", content, raw, err)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "watch-once-*.tmp*")); len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

// A record without cancel is the Monitor watch's: it dedups but never ends the watch.
func TestWatchOnceWithoutCancelRecordsAndNeverStartsTimer(t *testing.T) {
	dir := t.TempDir()
	var delivered []watchAgentAction
	next := func(a watchAgentAction) error {
		delivered = append(delivered, a)
		return nil
	}
	first := newWatchOnce(dir, "tok", nil, time.Now)
	sink := first.Sink(next)
	if err := sink(onceAction("a", "1")); err != nil {
		t.Fatal(err)
	}
	if !first.Fired() || len(delivered) != 1 {
		t.Fatalf("Fired = %v, delivered = %d, want true and 1", first.Fired(), len(delivered))
	}
	// A timer on a nil cancel would panic once the grace period is over.
	time.Sleep(watchOnceGrace + 200*time.Millisecond)

	second := newWatchOnce(dir, "tok", nil, time.Now)
	sink = second.Sink(next)
	if err := sink(onceAction("a", "1")); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 || second.Fired() {
		t.Fatalf("recorded action was delivered again: %#v", delivered)
	}
	if err := sink(onceAction("a", "2")); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 2 {
		t.Fatalf("new generation delivered %d times in total, want 2", len(delivered))
	}
}

func TestWatchHealthReporterStopSendsStoppedWithoutRearmOnStop(t *testing.T) {
	var got store.MonitorHealth
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
	}))
	defer server.Close()
	reporter := &watchHealthReporter{
		client:      server.Client(),
		urls:        []string{server.URL},
		health:      store.MonitorHealth{MonitorID: "m"},
		scopeActive: true,
	}
	reporter.Stop()
	if got.State != "stopped" || got.StoppedAt == nil {
		t.Fatalf("Stop without rearmOnStop posted %#v, want stopped", got)
	}
}

func TestWatchHealthReporterStopRearmsOnlyWhenFired(t *testing.T) {
	for _, fired := range []bool{true, false} {
		var got store.MonitorHealth
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode: %v", err)
			}
		}))
		reporter := &watchHealthReporter{
			client:      server.Client(),
			urls:        []string{server.URL},
			health:      store.MonitorHealth{MonitorID: "m"},
			scopeActive: true,
			rearmOnStop: func() bool { return fired },
		}
		reporter.Stop()
		server.Close()
		if fired {
			if got.State != "rearming" || got.StoppedAt != nil || got.Reason != "once: re-arm expected" {
				t.Fatalf("fired Stop posted %#v, want rearming without StoppedAt", got)
			}
		} else if got.State != "stopped" || got.StoppedAt == nil {
			t.Fatalf("unfired Stop posted %#v, want stopped", got)
		}
	}
}
