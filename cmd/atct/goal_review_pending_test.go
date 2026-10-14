package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// goalReviewFlow drives a real goal through the commander's goal-review steps.
type goalReviewFlow struct {
	t                         *testing.T
	s                         *store.Store
	projectID, goalID         int64
	commanderID, subcommander int64
	handoffID                 string
}

func newGoalReviewFlow(t *testing.T) *goalReviewFlow {
	t.Helper()
	ctx := context.Background()
	s, projectID := newReconcileContractStore(t)
	f := &goalReviewFlow{t: t, s: s, projectID: projectID, handoffID: "gh-pending",
		commanderID:  newReconcileContractSession(t, s, projectID),
		subcommander: newReconcileContractSession(t, s, projectID)}
	claimReconcileContractProject(t, s, projectID, f.commanderID)
	goal, err := s.CreateGoal(ctx, projectID, "pending goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	f.goalID = goal.ID
	f.must(s.RequestGoalHandoff(ctx, f.handoffID, f.goalID, f.commanderID, "deliver it"))
	f.must(s.ReceiveGoalHandoff(ctx, f.handoffID, f.goalID, f.subcommander))
	f.submit()
	return f
}

func (f *goalReviewFlow) must(_ any, err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatalf("goal review flow step: %v", err)
	}
}

// submit is the subcommander's review request plus the commander's receipt.
func (f *goalReviewFlow) submit() {
	f.t.Helper()
	ctx := context.Background()
	f.must(f.s.RequestGoalHandoffReview(ctx, f.handoffID, f.goalID, f.subcommander, "ready"))
	f.must(f.s.ReceiveGoalHandoffReview(ctx, f.handoffID, f.goalID, f.commanderID))
}

func (f *goalReviewFlow) askHuman() domain.Decision {
	f.t.Helper()
	d, err := f.s.RequestGoalReview(context.Background(), f.goalID, f.commanderID, domain.CompletionReport{
		WorkDone: "done", NowPossible: "now", HowToVerify: "verify", Surprises: "none", NeedsReview: "none", NextGoalIDs: []int64{},
	})
	if err != nil {
		f.t.Fatalf("RequestGoalReview: %v", err)
	}
	return d
}

func (f *goalReviewFlow) reject(d domain.Decision) {
	f.t.Helper()
	if err := f.s.RejectGoalReview(context.Background(), d.ID, "revise"); err != nil {
		f.t.Fatalf("RejectGoalReview: %v", err)
	}
}

func (f *goalReviewFlow) approve(d domain.Decision) {
	f.t.Helper()
	f.must(f.s.ApproveGoalReview(context.Background(), d.ID))
}

// commanderView reconciles once for the commander and returns what it emitted,
// the liveness target, and the pending state the store marks the goal with.
func (f *goalReviewFlow) commanderView(w *commanderRoutingWatch) (out string, state watchReconciliation, pending string) {
	f.t.Helper()
	var snapshot watchReconciliation
	out = w.runWithState(commanderScope(f.projectID), &snapshot)
	pending, err := f.s.GoalPendingState(context.Background(), f.goalID)
	if err != nil {
		f.t.Fatalf("GoalPendingState: %v", err)
	}
	return out, snapshot, pending
}

func (w *commanderRoutingWatch) runWithState(scope watchScope, state *watchReconciliation) string {
	w.t.Helper()
	var out strings.Builder
	if err := reconcileWatchScope(
		context.Background(), w.client.Client(), w.client.URL, scope, &out,
		w.delivered, &w.last, w.discrep, w.wakeup, newWatchScopeFilter(""), nil, state,
	); err != nil {
		w.t.Fatalf("reconcileWatchScope: %v", err)
	}
	return out.String()
}

func TestReconcileDeliversApprovedAndRejectedGoalReviewToCommanderOnce(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		resolve    func(*goalReviewFlow, domain.Decision)
	}{
		{"approve", "atct goal review approved", (*goalReviewFlow).approve},
		{"reject", "atct goal review rejected", (*goalReviewFlow).reject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGoalReviewFlow(t)
			tc.resolve(f, f.askHuman())
			w := newCommanderRoutingWatch(t, f.s)
			out, _, _ := f.commanderView(w)
			if got := strings.Count(out, tc.want); got != 1 {
				t.Fatalf("commander output has %d %q, want 1:\n%s", got, tc.want, out)
			}
			if out, _, _ := f.commanderView(w); strings.Contains(out, tc.want) {
				t.Fatalf("second reconcile re-delivered %q:\n%s", tc.want, out)
			}
			other := newCommanderRoutingWatch(t, f.s)
			if sub := other.run(subcommanderScope(f.projectID, f.goalID)); strings.Contains(sub, "atct goal review") {
				t.Fatalf("subcommander received a commander-addressed event:\n%s", sub)
			}
		})
	}
}

// The pending marker the store puts on atct_goal_list and the commander's
// liveness target must agree at every step of a reject / resubmit / approve run.
func TestGoalReviewPendingMarkerAndLivenessTargetAgree(t *testing.T) {
	f := newGoalReviewFlow(t)
	goalID := strconv.FormatInt(f.goalID, 10)
	w := newCommanderRoutingWatch(t, f.s)
	rejectedTarget := func(state watchReconciliation) bool {
		kind, _, id := watchCommanderLivenessTarget(state)
		return kind == watchKindGoalReviewRejected && id == goalID
	}
	step := func(name, wantPending string, wantRejectedTarget bool) watchReconciliation {
		t.Helper()
		_, state, pending := f.commanderView(w)
		if pending != wantPending {
			t.Fatalf("%s: pending state = %q, want %q", name, pending, wantPending)
		}
		if got := rejectedTarget(state); got != wantRejectedTarget {
			kind, handoffID, id := watchCommanderLivenessTarget(state)
			t.Fatalf("%s: rejected-review target = %v (target %q %q %q), want %v", name, got, kind, handoffID, id, wantRejectedTarget)
		}
		return state
	}

	first := f.askHuman()
	step("review open", "", false)

	f.reject(first)
	step("rejected, handoff not yet rejected", store.GoalPendingRejectedHandoffReject, true)

	f.must(f.s.RejectGoalHandoffReview(context.Background(), f.handoffID, f.goalID, f.commanderID, "revise"))
	state := step("handoff rejected", "", false)
	if watchLivenessActionable(commanderScope(f.projectID), state) {
		t.Fatal("handoff rejected: commander liveness remained")
	}

	f.must(f.s.ReceiveGoalHandoffReviewRejection(context.Background(), f.handoffID, f.goalID, f.subcommander))
	f.submit()
	step("resubmitted and received", "", false)

	second := f.askHuman()
	step("a later goal_review is open", "", false)

	f.approve(second)
	state = step("approved and applied", store.GoalPendingApprovedComplete, false)
	if !watchLivenessActionable(commanderScope(f.projectID), state) {
		t.Fatal("approved and applied: commander liveness is gone before the goal is completed")
	}
	if _, _, id := watchCommanderLivenessTarget(state); id != goalID {
		t.Fatalf("approved and applied: target goal = %q, want %q", id, goalID)
	}

	f.must(f.s.FinalizeGoalReview(context.Background(), f.goalID, f.commanderID))
	_, state, _ = f.commanderView(w)
	if watchLivenessActionable(commanderScope(f.projectID), state) {
		t.Fatal("goal completed: commander liveness remained")
	}
}
