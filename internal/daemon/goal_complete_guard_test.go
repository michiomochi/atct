package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestGoalCompleteDispatchReturnsNamedReviewMigrationDiagnosticWithoutMutation(t *testing.T) {
	ctx, s, daemon, project, sessionID := newGoalCompleteGuardFixture(t, "retired")
	goal, err := s.CreateGoal(ctx, project.ID, "retired completion goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	before, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal before dispatch: %v", err)
	}
	decisionsBefore, err := s.ListDecisionsForGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListDecisionsForGoal before dispatch: %v", err)
	}

	_, err = daemon.dispatch(ctx, rpc.Request{
		Method: "goal.complete",
		Params: goalCompleteParams(t, goal.ID, sessionID, approvedGoalReport("retired")),
	})
	if err == nil {
		t.Fatal("goal.complete returned success after retirement")
	}
	for _, want := range []string{"atct_goal_review_request", "atct_goal_review_complete"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("goal.complete error = %q, want %q", err, want)
		}
	}

	after, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after dispatch: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("goal changed after retired goal.complete: before=%+v after=%+v", before, after)
	}
	decisionsAfter, err := s.ListDecisionsForGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListDecisionsForGoal after dispatch: %v", err)
	}
	if len(decisionsAfter) != len(decisionsBefore) {
		t.Fatalf("decision count after retired goal.complete = %d, want %d", len(decisionsAfter), len(decisionsBefore))
	}
}

func TestCommanderGoalReviewThenCompleteWritesFinalReport(t *testing.T) {
	ctx, s, daemon, project, commanderID := newGoalCompleteGuardFixture(t, "review-lifecycle")
	goal, err := s.CreateGoal(ctx, project.ID, "reviewed goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	report := approvedGoalReport("review-lifecycle")

	reviewParams, err := json.Marshal(map[string]any{
		"goal_id": goal.ID, "agent_session_id": commanderID,
		"work_done": report.WorkDone, "now_possible": report.NowPossible,
		"how_to_verify": report.HowToVerify, "surprises": report.Surprises,
		"needs_review": report.NeedsReview, "next_goal_ids": report.NextGoalIDs,
	})
	if err != nil {
		t.Fatalf("Marshal goal.review.request params: %v", err)
	}
	if _, err := daemon.dispatch(ctx, rpc.Request{Method: "goal.review.request", Params: reviewParams}); err == nil {
		t.Fatal("goal.review.request without a commander-received delegated goal handoff unexpectedly succeeded")
	} else if !errors.Is(err, store.ErrGoalReviewHandoffIncomplete) {
		t.Fatalf("goal.review.request without a commander-received delegated goal handoff error = %v, want %v", err, store.ErrGoalReviewHandoffIncomplete)
	}

	const handoffID = "goal-complete-review-lifecycle-handoff"
	receiverID := daemonTestSessionID(t, s, "goal-complete-review-lifecycle-receiver")
	dispatchHandoff := func(method string, params map[string]any) {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("Marshal %s params: %v", method, err)
		}
		if _, err := daemon.dispatch(ctx, rpc.Request{Method: method, Params: raw}); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	dispatchHandoff("goal.handoff.request", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "requested_by": commanderID,
		"request_report": "commander delegated the reviewed goal",
	})
	dispatchHandoff("goal.handoff.receive", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "received_by": receiverID,
	})
	dispatchHandoff("goal.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "requested_by": receiverID,
		"review_request_report": "receiver reviewed the delegated goal",
	})
	dispatchHandoff("goal.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "received_by": commanderID,
	})
	raw, err := daemon.dispatch(ctx, rpc.Request{Method: "goal.review.request", Params: reviewParams})
	if err != nil {
		t.Fatalf("goal.review.request: %v", err)
	}
	var review domain.Decision
	if err := json.Unmarshal(raw, &review); err != nil {
		t.Fatalf("decode goal.review.request response %s: %v", raw, err)
	}
	if review.Kind != domain.KindGoalReview || review.Status != domain.DecisionOpen {
		t.Fatalf("goal review = %+v, want open goal review", review)
	}

	if _, err := s.ApproveGoalReview(ctx, review.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	completeParams, err := json.Marshal(map[string]any{
		"goal_id": goal.ID, "agent_session_id": commanderID,
	})
	if err != nil {
		t.Fatalf("Marshal goal.review.complete params: %v", err)
	}
	raw, err = daemon.dispatch(ctx, rpc.Request{
		Method: "goal.review.complete",
		Params: completeParams,
	})
	if err != nil {
		t.Fatalf("goal.review.complete after approval: %v", err)
	}
	var done domain.Goal
	if err := json.Unmarshal(raw, &done); err != nil {
		t.Fatalf("decode goal.review.complete response %s: %v", raw, err)
	}
	if done.Status != domain.GoalDone || done.WorkDone != report.WorkDone || len(done.NextGoals) != len(report.NextGoalIDs) {
		t.Fatalf("completed goal = %+v, want final report and done status", done)
	}
	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetGoalHandoff after goal completion: %v", err)
	}
	if handoff.CompletedReportAt == nil || handoff.CompleteReport != "receiver reviewed the delegated goal" {
		t.Fatalf("completed goal handoff = %+v, want commander finalization with review report", handoff)
	}
}

func newGoalCompleteGuardFixture(t *testing.T, label string) (context.Context, *store.Store, *Daemon, domain.Project, int64) {
	t.Helper()
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project, err := s.CreateProject(ctx, "goal-complete-guard-"+label, t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	sessionID := daemonTestSessionID(t, s, "goal-complete-guard-"+label)
	if err := s.AssociateAgentSessionWithProject(ctx, sessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	// goal.complete authorization requires the dispatching session to hold the project's claim.
	if _, err := s.ClaimProject(ctx, project.ID, sessionID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	return ctx, s, New(s), project, sessionID
}

func goalCompleteParams(t *testing.T, goalID, sessionID int64, report domain.CompletionReport) json.RawMessage {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"goal_id":          goalID,
		"work_done":        report.WorkDone,
		"now_possible":     report.NowPossible,
		"how_to_verify":    report.HowToVerify,
		"surprises":        report.Surprises,
		"needs_review":     report.NeedsReview,
		"agent_session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("Marshal goal.complete params: %v", err)
	}
	return params
}

func approvedGoalReport(label string) domain.CompletionReport {
	return domain.CompletionReport{
		WorkDone:    "AAA-" + label + "-approved-work-done",
		NowPossible: "AAA-" + label + "-approved-now-possible",
		HowToVerify: "AAA-" + label + "-approved-how-to-verify",
		Surprises:   "AAA-" + label + "-approved-surprises",
		NeedsReview: "AAA-" + label + "-approved-needs-review",
		NextGoalIDs: []int64{},
	}
}
