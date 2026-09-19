package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

const staleApprovalReasonForTest = "automatic withdrawal: goal approval was stale for 14 days with no recorded task or handoff activity"

func TestReconcileStaleGoalApprovalsWithdrawsUntouchedOldProposal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goal, approval := newAgentProposal(t, s)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-14*24*time.Hour - time.Hour)
	setProposalActivity(t, s, goal.ID, approval.ID, old, old)

	events, unsubscribe := s.SubscribeEvents()
	defer unsubscribe()
	count, err := s.ReconcileStaleGoalApprovals(ctx, now)
	if err != nil || count != 1 {
		t.Fatalf("ReconcileStaleGoalApprovals() = (%d, %v), want (1, nil)", count, err)
	}

	gotGoal, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	gotApproval, err := s.GetDecision(ctx, approval.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if gotGoal.Status != domain.GoalDropped || gotApproval.Status != domain.DecisionWithdrawn {
		t.Fatalf("statuses = (%q, %q), want (dropped, withdrawn)", gotGoal.Status, gotApproval.Status)
	}
	if gotGoal.ResultSummary != staleApprovalReasonForTest || gotApproval.AnswerText != staleApprovalReasonForTest {
		t.Fatalf("withdrawal reasons = (%q, %q), want %q", gotGoal.ResultSummary, gotApproval.AnswerText, staleApprovalReasonForTest)
	}

	withdrawn := readGoalWithdrawnEvents(t, events)
	if len(withdrawn) != 1 || withdrawn[0].GoalID != goal.ID || !sameInt64IDs(withdrawn[0].WithdrawnDecisionIDs, []int64{approval.ID}) {
		t.Fatalf("goal withdrawal events = %+v, want one event for goal %d and approval %d", withdrawn, goal.ID, approval.ID)
	}
	count, err = s.ReconcileStaleGoalApprovals(ctx, now.Add(time.Hour))
	if err != nil || count != 0 {
		t.Fatalf("second ReconcileStaleGoalApprovals() = (%d, %v), want (0, nil)", count, err)
	}
	if got := readGoalWithdrawnEvents(t, events); len(got) != 0 {
		t.Fatalf("second reconciliation published goal withdrawal events = %+v, want none", got)
	}
}

func TestReconcileStaleGoalApprovalsKeepsRecentProposal(t *testing.T) {
	testReconcileKeepsProposal(t, func(t *testing.T, s *Store, goalID, approvalID int64, now time.Time) {
		setProposalActivity(t, s, goalID, approvalID, now.Add(-time.Hour), now.Add(-time.Hour))
	})
}

func TestReconcileStaleGoalApprovalsKeepsProposalWithTaskInAnyStatus(t *testing.T) {
	for _, status := range []domain.TaskStatus{domain.TaskTodo, domain.TaskDoing, domain.TaskReview, domain.TaskDone, domain.TaskDropped} {
		t.Run(string(status), func(t *testing.T) {
			testReconcileKeepsProposal(t, func(t *testing.T, s *Store, goalID, approvalID int64, now time.Time) {
				ctx := context.Background()
				old := now.Add(-14*24*time.Hour - time.Hour)
				_, err := s.DB().ExecContext(ctx, `
					INSERT INTO tasks (goal_id, title, description, status, agent, sort_order, declare_key, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				`, goalID, "recorded task", "task evidence", string(status), "agent", 0, "stale-approval-task", old.Format(time.RFC3339Nano), old.Format(time.RFC3339Nano))
				if err != nil {
					t.Fatalf("insert task: %v", err)
				}
				setProposalActivity(t, s, goalID, approvalID, old, old)
			})
		})
	}
}

func TestReconcileStaleGoalApprovalsKeepsProposalWithCompletedHandoff(t *testing.T) {
	testReconcileKeepsProposal(t, func(t *testing.T, s *Store, goalID, approvalID int64, now time.Time) {
		ctx := context.Background()
		old := now.Add(-14*24*time.Hour - time.Hour)
		_, err := s.DB().ExecContext(ctx, `
			INSERT INTO goal_handoffs (id, goal_id, requested_at, completed_report_at, complete_report)
			VALUES (?, ?, ?, ?, ?)
		`, "completed-stale-approval-handoff", goalID, old.Format(time.RFC3339Nano), old.Format(time.RFC3339Nano), "completed work")
		if err != nil {
			t.Fatalf("insert completed handoff: %v", err)
		}
		setProposalActivity(t, s, goalID, approvalID, old, old)
	})
}

func TestReconcileStaleGoalApprovalsKeepsProposalWithAdditionalDecision(t *testing.T) {
	testReconcileKeepsProposal(t, func(t *testing.T, s *Store, goalID, approvalID int64, now time.Time) {
		ctx := context.Background()
		if _, err := s.AskDecision(ctx, AskInput{
			GoalID:   goalID,
			Kind:     domain.KindDecision,
			Question: "Should this proposed goal stay visible?",
		}); err != nil {
			t.Fatalf("AskDecision: %v", err)
		}
		old := now.Add(-14*24*time.Hour - time.Hour)
		setProposalActivity(t, s, goalID, approvalID, old, old)
	}, 2)
}

func TestReconcileStaleGoalApprovalsKeepsRecentlyUpdatedProposal(t *testing.T) {
	testReconcileKeepsProposal(t, func(t *testing.T, s *Store, goalID, approvalID int64, now time.Time) {
		old := now.Add(-14*24*time.Hour - time.Hour)
		setProposalActivity(t, s, goalID, approvalID, old, now.Add(-time.Hour))
	})
}

func TestReconcileStaleGoalApprovalsKeepsHumanCreatedProposal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goal, approval := newAgentProposal(t, s)
	if _, err := s.DB().ExecContext(ctx, `UPDATE goals SET creator = 'human' WHERE id = ?`, goal.ID); err != nil {
		t.Fatalf("change goal creator: %v", err)
	}
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-14*24*time.Hour - time.Hour)
	setProposalActivity(t, s, goal.ID, approval.ID, old, old)

	events, unsubscribe := s.SubscribeEvents()
	defer unsubscribe()
	count, err := s.ReconcileStaleGoalApprovals(ctx, now)
	if err != nil || count != 0 {
		t.Fatalf("ReconcileStaleGoalApprovals() = (%d, %v), want (0, nil)", count, err)
	}
	assertProposalUnchanged(t, s, goal.ID, approval.ID, 1)
	if got := readGoalWithdrawnEvents(t, events); len(got) != 0 {
		t.Fatalf("goal withdrawal events = %+v, want none", got)
	}
}

func TestWithdrawActiveGoalRejectsProposedGoalWithWork(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goal, approval := newAgentProposal(t, s)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO tasks (goal_id, title, description, status, agent, sort_order, declare_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, goal.ID, "recorded task", "task evidence", string(domain.TaskDropped), "agent", 0, "proposed-with-work", time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	err := s.WithdrawActiveGoal(ctx, goal.ID, "no longer needed")
	if !errors.Is(err, ErrGoalHasWork) {
		t.Fatalf("WithdrawActiveGoal error = %v, want ErrGoalHasWork", err)
	}
	assertProposalUnchanged(t, s, goal.ID, approval.ID, 1)
}

func testReconcileKeepsProposal(t *testing.T, setup func(*testing.T, *Store, int64, int64, time.Time), wantDecisionCounts ...int) {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	goal, approval := newAgentProposal(t, s)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	setup(t, s, goal.ID, approval.ID, now)
	events, unsubscribe := s.SubscribeEvents()
	defer unsubscribe()

	count, err := s.ReconcileStaleGoalApprovals(ctx, now)
	if err != nil || count != 0 {
		t.Fatalf("ReconcileStaleGoalApprovals() = (%d, %v), want (0, nil)", count, err)
	}
	wantDecisionCount := 1
	if len(wantDecisionCounts) == 1 {
		wantDecisionCount = wantDecisionCounts[0]
	}
	assertProposalUnchanged(t, s, goal.ID, approval.ID, wantDecisionCount)
	if got := readGoalWithdrawnEvents(t, events); len(got) != 0 {
		t.Fatalf("goal withdrawal events = %+v, want none", got)
	}
}

func newAgentProposal(t *testing.T, s *Store) (domain.Goal, domain.Decision) {
	t.Helper()
	ctx := context.Background()
	project, err := s.CreateProject(ctx, fmt.Sprintf("stale-approval-%s", t.Name()), t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "proposed goal", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	decisions, err := s.ListOpenDecisions(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Kind != domain.KindGoalApproval {
		t.Fatalf("open goal approvals = %+v, want one goal approval", decisions)
	}
	return goal, decisions[0]
}

func setProposalActivity(t *testing.T, s *Store, goalID, approvalID int64, createdAt, updatedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `UPDATE goals SET created_at = ?, updated_at = ? WHERE id = ?`, createdAt.UTC().Format(time.RFC3339Nano), updatedAt.UTC().Format(time.RFC3339Nano), goalID); err != nil {
		t.Fatalf("age goal: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE decisions SET created_at = ? WHERE id = ?`, createdAt.UTC().Format(time.RFC3339Nano), approvalID); err != nil {
		t.Fatalf("age approval: %v", err)
	}
}

func assertProposalUnchanged(t *testing.T, s *Store, goalID, approvalID int64, wantDecisionCount int) {
	t.Helper()
	ctx := context.Background()
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if goal.Status != domain.GoalProposed {
		t.Fatalf("goal status = %q, want proposed", goal.Status)
	}
	approval, err := s.GetDecision(ctx, approvalID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if approval.Status != domain.DecisionOpen {
		t.Fatalf("approval status = %q, want open", approval.Status)
	}
	decisions, err := s.ListDecisionsForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("ListDecisionsForGoal: %v", err)
	}
	if len(decisions) != wantDecisionCount {
		t.Fatalf("decision count = %d, want %d", len(decisions), wantDecisionCount)
	}
}

func readGoalWithdrawnEvents(t *testing.T, events <-chan DecisionEvent) []GoalWithdrawnEvent {
	t.Helper()
	var withdrawn []GoalWithdrawnEvent
	for {
		select {
		case event := <-events:
			if event.Name != EventGoalWithdrawn {
				continue
			}
			got, ok := event.Data.(GoalWithdrawnEvent)
			if !ok {
				t.Fatalf("goal withdrawal event data = %T, want GoalWithdrawnEvent", event.Data)
			}
			withdrawn = append(withdrawn, got)
		default:
			return withdrawn
		}
	}
}
