package daemon

import (
	"context"
	"strings"
	"testing"
)

// The Monitor form check blocks a subcommander only while its one open item is
// an answer it is waiting for. Every case here is one the stop check already
// judges; the form check adds the "waiting" test on top.
func TestMonitorFormCheckBlocksOnlyWhileWaitingForAnAnswer(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, waitingFixture)
		block bool
	}{
		{"plan review pending", func(t *testing.T, w waitingFixture) { w.planReview(t) }, true},
		{"goal review pending", func(t *testing.T, w waitingFixture) { w.goalReview(t) }, true},
		{"executor working", func(t *testing.T, w waitingFixture) { w.taskHandoff(t, true, false) }, true},
		{"executor starting", func(t *testing.T, w waitingFixture) { w.taskHandoff(t, false, false) }, true},
		{"task review is ours to answer", func(t *testing.T, w waitingFixture) { w.taskHandoff(t, true, true) }, false},
		{"goal review pending but a task review waits", func(t *testing.T, w waitingFixture) {
			w.goalReview(t)
			w.taskHandoff(t, true, true)
		}, false},
		{"rejected plan handoff", func(t *testing.T, w waitingFixture) {
			w.planReview(t)
			ctx := context.Background()
			if _, err := w.store.ReceivePlanHandoffReview(ctx, "stop-wait-plan", w.taskGoal.ID, w.commanderID); err != nil {
				t.Fatalf("ReceivePlanHandoffReview: %v", err)
			}
			if _, err := w.store.RejectPlanHandoffReview(ctx, "stop-wait-plan", w.taskGoal.ID, w.commanderID, "fix"); err != nil {
				t.Fatalf("RejectPlanHandoffReview: %v", err)
			}
			if _, err := w.store.ReceivePlanHandoffReviewRejection(ctx, "stop-wait-plan", w.taskGoal.ID, w.subID); err != nil {
				t.Fatalf("ReceivePlanHandoffReviewRejection: %v", err)
			}
		}, false},
		{"design in progress, nothing requested", func(t *testing.T, w waitingFixture) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWaitingFixture(t, true)
			tc.setup(t, w)
			got := w.daemon.monitorFormCheckFor(context.Background(), w.subID)
			if tc.block && got.Decision != "block" {
				t.Fatalf("monitorFormCheckFor = %+v, want a block", got)
			}
			if !tc.block && got.Decision != "" {
				t.Fatalf("monitorFormCheckFor = %+v, want no block", got)
			}
			if tc.block && !strings.Contains(got.Reason, "--once") {
				t.Fatalf("reason %q does not name the --once form", got.Reason)
			}
		})
	}
}

// Only a subcommander's watch is in question; the commander's and an
// unassigned session's are never blocked here.
func TestMonitorFormCheckIgnoresOtherRolesAndUnknownSessions(t *testing.T) {
	w := newWaitingFixture(t, true)
	w.planReview(t)
	if got := w.daemon.monitorFormCheckFor(context.Background(), w.commanderID); got.Decision != "" {
		t.Fatalf("commander: monitorFormCheckFor = %+v, want no block", got)
	}
	if got := w.daemon.monitorFormCheck(context.Background(), "no-such-session-key"); got.Decision != "" {
		t.Fatalf("unknown session key: monitorFormCheck = %+v, want fail-open", got)
	}
}

// An archived project is judged by nobody: the form check allows the Monitor.
func TestMonitorFormCheckAllowsArchivedProject(t *testing.T) {
	w := newWaitingFixture(t, true)
	w.planReview(t)
	if _, err := w.store.ArchiveProject(context.Background(), w.project.ID); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	if got := w.daemon.monitorFormCheckFor(context.Background(), w.subID); got.Decision != "" {
		t.Fatalf("archived project: monitorFormCheckFor = %+v, want no block", got)
	}
}

// Without a Monitor, a waiting subcommander is told how to wait, once, instead
// of the bare "open goal handoff" reason.
func TestStopCheckNamesTheOnceWatchWhenWaitingWithoutMonitor(t *testing.T) {
	w := newWaitingFixture(t, true)
	w.planReview(t)
	detail, err := w.daemon.stopCheckSubcommander(context.Background(), w.subID, w.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander: %v", err)
	}
	if !strings.HasPrefix(detail, "executor has open goal handoff ") || !strings.Contains(detail, "--monitor --token <monitor_token> --once") {
		t.Fatalf("stopCheckSubcommander = %q, want the open goal handoff reason naming the --once watch", detail)
	}
}
