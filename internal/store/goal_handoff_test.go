package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

func addLiveGoalClaim(t *testing.T, s *Store, goalID int64, sessionID string) {
	t.Helper()

	ctx := context.Background()
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	agentSessionID := registerNamedTestAgentSession(t, s, sessionID, os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, agentSessionID, goal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject failed: %v", err)
	}
	if _, err := s.ClaimGoal(ctx, goalID, agentSessionID); err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}
}

func addLiveProjectClaim(t *testing.T, s *Store, goalID int64, sessionID string) {
	t.Helper()

	ctx := context.Background()
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	agentSessionID := registerNamedTestAgentSession(t, s, sessionID, os.Getpid())
	if _, err := s.ClaimProject(ctx, goal.ProjectID, agentSessionID); err != nil {
		t.Fatalf("ClaimProject failed: %v", err)
	}
}

func addRequestOnlyGoalHandoff(t *testing.T, s *Store, handoffID string, goalID int64, requestedBy string) {
	t.Helper()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := sqlcgen.New(s.DB()).RequestGoalHandoff(context.Background(), sqlcgen.RequestGoalHandoffParams{
		ID:            handoffID,
		GoalID:        goalID,
		RequestedBy:   sql.NullInt64{Int64: testSessionID(requestedBy), Valid: true},
		RequestedAt:   sql.NullString{String: now, Valid: true},
		RequestReport: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("insert request-only goal handoff failed: %v", err)
	}
}

func addReceiptOnlyGoalHandoff(t *testing.T, s *Store, handoffID string, goalID int64, receivedBy string) {
	t.Helper()

	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO goal_handoffs (id, goal_id, requested_by, received_by, received_at)
		VALUES (?, ?, ?, ?, ?)
	`, handoffID, goalID, testSessionID(receivedBy), testSessionID(receivedBy), now); err != nil {
		t.Fatalf("insert receipt-only goal handoff failed: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM goal_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete receipt-only goal handoff %q: %v", handoffID, err)
		}
	})
}

func addGoalHandoffDirect(t *testing.T, s *Store, handoffID string, goalID int64, requestedBy, receivedBy any) {
	t.Helper()

	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `DROP INDEX IF EXISTS idx_goal_handoffs_open_goal_id`); err != nil {
		t.Fatalf("drop goal handoff uniqueness index: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM goal_handoffs WHERE id = ?`, handoffID); err != nil {
			t.Errorf("delete direct goal handoff %q: %v", handoffID, err)
		}
		if _, err := s.DB().ExecContext(ctx, `
			CREATE UNIQUE INDEX idx_goal_handoffs_open_goal_id
			ON goal_handoffs(goal_id)
			WHERE completed_report_at IS NULL
		`); err != nil {
			t.Errorf("restore goal handoff uniqueness index: %v", err)
		}
	})

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var err error
	if receivedBy == "" {
		_, err = s.DB().ExecContext(ctx, `
			INSERT INTO goal_handoffs (
				id, goal_id, requested_by, requested_at, request_report,
				received_by, received_at, completed_report_at, complete_report
			) VALUES (?, ?, ?, ?, NULL, NULL, NULL, NULL, NULL)
		`, handoffID, goalID, nullableTestSessionRef(requestedBy), now)
	} else {
		_, err = s.DB().ExecContext(ctx, `
			INSERT INTO goal_handoffs (
				id, goal_id, requested_by, requested_at, request_report,
				received_by, received_at, completed_report_at, complete_report
			) VALUES (?, ?, ?, ?, NULL, ?, ?, NULL, NULL)
		`, handoffID, goalID, nullableTestSessionRef(requestedBy), now, nullableTestSessionRef(receivedBy), now)
	}
	if err != nil {
		t.Fatalf("insert direct goal handoff %q failed: %v", handoffID, err)
	}
}

func TestGoalHandoffRequestReceiveAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-handoff-1", goalID, testSessionID("goal-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}
	if handoff.ID == "" || handoff.GoalID != goalID {
		t.Fatalf("unexpected requested handoff: %+v", handoff)
	}
	if handoff.RequestedAt == nil || handoff.ReceivedAt != nil || handoff.CompletedReportAt != nil {
		t.Fatalf("unexpected new handoff state: %+v", handoff)
	}

	unreceived, err := s.GetGoalHandoff(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff before receive failed: %v", err)
	}
	if unreceived.ReceivedAt != nil {
		t.Fatalf("unreceived handoff has received timestamp: %+v", unreceived)
	}

	received, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("goal-receiver"))
	if err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	if received.ID != handoff.ID || received.ReceivedBy != testSessionID("goal-receiver") || received.ReceivedAt == nil {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
	if received.RequestedAt == nil || received.CompletedReportAt != nil {
		t.Fatalf("receive must preserve request and incomplete state: %+v", received)
	}

	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID("goal-receiver"), "ready for review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID("goal-requester")); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview failed: %v", err)
	}
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goalID, testSessionID("goal-requester"), "completed after verifying goal handoff state")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer failed: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.RequestedAt == nil || completed.ReceivedAt == nil {
		t.Fatalf("completion must preserve all prior timestamps: %+v", completed)
	}
}

func TestGoalHandoffDirectCompletionRequiresReviewer(t *testing.T) {
	tests := []struct {
		name           string
		completeByGoal bool
		requestReview  bool
	}{
		{name: "direct before review"},
		{name: "for goal before review", completeByGoal: true},
		{name: "direct after review", requestReview: true},
		{name: "for goal after review", completeByGoal: true, requestReview: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			goalID := newTestGoal(t, s)
			requester := "direct-close-requester"
			receiver := "direct-close-receiver"
			addLiveProjectClaim(t, s, goalID, requester)
			addTestAgentSession(t, s, receiver)

			handoff, err := s.RequestGoalHandoff(ctx, "direct-close-"+strings.ReplaceAll(tc.name, " ", "-"), goalID, testSessionID(requester), "delegate")
			if err != nil {
				t.Fatalf("RequestGoalHandoff: %v", err)
			}
			if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID(receiver)); err != nil {
				t.Fatalf("ReceiveGoalHandoff: %v", err)
			}
			if tc.requestReview {
				if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID(receiver), "ready for review"); err != nil {
					t.Fatalf("RequestGoalHandoffReview: %v", err)
				}
			}

			if tc.completeByGoal {
				_, err = s.CompleteGoalHandoffForGoal(ctx, goalID, "direct completion bypass")
			} else {
				_, err = s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "direct completion bypass")
			}
			if !errors.Is(err, ErrGoalHandoffReviewState) {
				t.Fatalf("direct completion error = %v, want ErrGoalHandoffReviewState", err)
			}

			stored, err := s.GetGoalHandoff(ctx, handoff.ID)
			if err != nil {
				t.Fatalf("GetGoalHandoff: %v", err)
			}
			if stored.CompletedReportAt != nil || stored.CompleteReport != "" {
				t.Fatalf("direct completion changed handoff: %+v", stored)
			}
		})
	}
}

func TestGoalHandoffReviewRejectReceiveLifecyclePreservesGoalClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	requesterID := testSessionID("goal-review-requester")
	receiverID := testSessionID("goal-review-receiver")
	wrongReviewerID := testSessionID("goal-review-wrong-reviewer")
	addLiveProjectClaim(t, s, goalID, "goal-review-requester")
	addTestAgentSession(t, s, "goal-review-receiver")
	addTestAgentSession(t, s, "goal-review-wrong-reviewer")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-review-lifecycle", goalID, requesterID, "take the goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	reviewRequested, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "goal is ready")
	if err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if reviewRequested.ReviewRequestedBy != receiverID || reviewRequested.ReviewRequestedAt == nil || reviewRequested.ReviewRequestReport != "goal is ready" {
		t.Fatalf("unexpected goal review request: %+v", reviewRequested)
	}

	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, wrongReviewerID); err == nil {
		t.Fatal("ReceiveGoalHandoffReview accepted the wrong reviewer")
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, requesterID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	if _, err := s.RejectGoalHandoffReview(ctx, handoff.ID, goalID, wrongReviewerID, "wrong reviewer"); err == nil {
		t.Fatal("RejectGoalHandoffReview accepted the wrong reviewer")
	}

	rejected, err := s.RejectGoalHandoffReview(ctx, handoff.ID, goalID, requesterID, "revise the goal")
	if err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
	if rejected.ReceivedBy != receiverID || rejected.ReviewReceivedBy != 0 || rejected.ReviewReceivedAt != nil || rejected.ReviewRejectReport != "revise the goal" || rejected.ReviewRejectedAt == nil {
		t.Fatalf("goal review rejection did not preserve claim and clear reviewer state: %+v", rejected)
	}

	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "revised goal"); err == nil {
		t.Fatal("RequestGoalHandoffReview accepted a rejection that the original submitter has not received")
	}
	if _, err := s.ReceiveGoalHandoffReviewRejection(ctx, handoff.ID, goalID, wrongReviewerID); err == nil {
		t.Fatal("ReceiveGoalHandoffReviewRejection accepted a foreign session")
	}
	rejectionReceived, err := s.ReceiveGoalHandoffReviewRejection(ctx, handoff.ID, goalID, receiverID)
	if err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}
	if rejectionReceived.ReviewRejectionReceivedBy != receiverID || rejectionReceived.ReviewRejectionReceivedAt == nil {
		t.Fatalf("unexpected goal rejection receipt: %+v", rejectionReceived)
	}

	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "revised goal"); err != nil {
		t.Fatalf("second RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, requesterID); err != nil {
		t.Fatalf("second ReceiveGoalHandoffReview: %v", err)
	}
	if _, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goalID, wrongReviewerID, "approved by wrong reviewer"); err == nil {
		t.Fatal("CompleteGoalHandoffByReviewer accepted the wrong reviewer")
	}
	if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "bypassed review"); err == nil {
		t.Fatal("CompleteGoalHandoff bypassed the recorded reviewer")
	}
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goalID, requesterID, "approved after review")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}
	if completed.CompletedReportAt == nil || completed.CompleteReport != "approved after review" {
		t.Fatalf("unexpected completed goal handoff: %+v", completed)
	}
}

func TestReceiveGoalHandoffReviewAuthorization(t *testing.T) {
	tests := []struct {
		name      string
		requester string
		caller    string
		prepare   func(t *testing.T, s *Store, goalID int64, caller string)
		wantError bool
	}{
		{
			name:      "normal requester success",
			requester: "receive-review-normal-requester",
			caller:    "receive-review-normal-requester",
		},
		{
			name:      "live requester rejects another caller",
			requester: "receive-review-live-requester",
			caller:    "receive-review-third-party",
			prepare: func(t *testing.T, s *Store, _ int64, caller string) {
				addTestAgentSession(t, s, caller)
			},
			wantError: true,
		},
		{
			name:      "current commander succeeds after claim turnover",
			requester: "receive-review-turned-over-requester",
			caller:    "receive-review-current-commander",
			prepare: func(t *testing.T, s *Store, goalID int64, caller string) {
				ctx := context.Background()
				goal, err := s.GetGoal(ctx, goalID)
				if err != nil {
					t.Fatalf("GetGoal: %v", err)
				}
				if err := s.ReleaseProject(ctx, goal.ProjectID); err != nil {
					t.Fatalf("ReleaseProject: %v", err)
				}
				addLiveProjectClaim(t, s, goalID, caller)
			},
		},
		{
			name:      "turnover rejects noncommander",
			requester: "receive-review-noncommander-requester",
			caller:    "receive-review-noncommander",
			prepare: func(t *testing.T, s *Store, goalID int64, caller string) {
				ctx := context.Background()
				goal, err := s.GetGoal(ctx, goalID)
				if err != nil {
					t.Fatalf("GetGoal: %v", err)
				}
				if err := s.ReleaseProject(ctx, goal.ProjectID); err != nil {
					t.Fatalf("ReleaseProject: %v", err)
				}
				addLiveProjectClaim(t, s, goalID, "receive-review-turnover-commander")
				addTestAgentSession(t, s, caller)
			},
			wantError: true,
		},
		{
			name:      "foreign project commander is rejected",
			requester: "receive-review-foreign-requester",
			caller:    "receive-review-foreign-commander",
			prepare: func(t *testing.T, s *Store, _ int64, caller string) {
				ctx := context.Background()
				foreignProject, err := s.CreateProject(ctx, "foreign-review", "/repos/foreign-review")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				foreignGoal, err := s.CreateGoal(ctx, foreignProject.ID, "foreign-goal", "human")
				if err != nil {
					t.Fatalf("CreateGoal: %v", err)
				}
				addLiveProjectClaim(t, s, foreignGoal.ID, caller)
			},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			goalID := newTestGoal(t, s)
			addLiveProjectClaim(t, s, goalID, tc.requester)
			addTestAgentSession(t, s, "receive-review-receiver")

			handoff, err := s.RequestGoalHandoff(ctx, "receive-review-"+tc.name, goalID, testSessionID(tc.requester), "delegate for review")
			if err != nil {
				t.Fatalf("RequestGoalHandoff: %v", err)
			}
			if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("receive-review-receiver")); err != nil {
				t.Fatalf("ReceiveGoalHandoff: %v", err)
			}
			if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID("receive-review-receiver"), "ready for review"); err != nil {
				t.Fatalf("RequestGoalHandoffReview: %v", err)
			}
			if tc.prepare != nil {
				tc.prepare(t, s, goalID, tc.caller)
			}

			before, err := s.GetGoalHandoff(ctx, handoff.ID)
			if err != nil {
				t.Fatalf("GetGoalHandoff before receive: %v", err)
			}
			events, cancel := s.SubscribeEvents()
			defer cancel()

			received, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, testSessionID(tc.caller))
			if tc.wantError {
				if !errors.Is(err, ErrGoalHandoffReviewReviewerMismatch) {
					t.Fatalf("ReceiveGoalHandoffReview error = %v, want ErrGoalHandoffReviewReviewerMismatch", err)
				}
				if received != (GoalHandoff{}) {
					t.Fatalf("failed receive returned handoff = %+v, want zero value", received)
				}
				select {
				case event := <-events:
					t.Fatalf("rejected receive published workflow event: %+v", event)
				default:
				}

				after, err := s.GetGoalHandoff(ctx, handoff.ID)
				if err != nil {
					t.Fatalf("GetGoalHandoff after rejected receive: %v", err)
				}
				if after.ReviewReceivedBy != before.ReviewReceivedBy || (after.ReviewReceivedAt == nil) != (before.ReviewReceivedAt == nil) {
					t.Fatalf("rejected receive changed review receipt: before=%+v after=%+v", before, after)
				}
				return
			}

			if err != nil {
				t.Fatalf("ReceiveGoalHandoffReview: %v", err)
			}
			if received.ReviewReceivedBy != testSessionID(tc.caller) || received.ReviewReceivedAt == nil {
				t.Fatalf("successful receive = %+v, want reviewer %d with timestamp", received, testSessionID(tc.caller))
			}
		})
	}
}

func TestListGoalSessionsIncludesSubcommander(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-sessions-subcommander")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-subcommander"), "atct-goal-sessions-subcommander")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "goal-sessions-goal-handoff", goalID, canonicalID, canonicalID)

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(sessions), sessions)
	}
	if got := sessions[0]; got.SessionKey != "atct-goal-sessions-subcommander" || got.Role != "subcommander" || !got.HandoffOpen {
		t.Fatalf("unexpected subcommander session: %+v", got)
	}
}

func TestListGoalSessionsDeduplicatesExecutorTaskHandoffs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 2)
	var goalID int64
	if err := s.DB().QueryRowContext(ctx, "SELECT goal_id FROM tasks WHERE id = ?", taskIDs[0]).Scan(&goalID); err != nil {
		t.Fatalf("find task goal: %v", err)
	}
	addTestAgentSession(t, s, "goal-sessions-executor")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-executor"), "atct-goal-sessions-executor")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	addTaskHandoffDirect(t, s, "goal-sessions-task-handoff-1", taskIDs[0], canonicalID, canonicalID)
	const secondHandoffID = "goal-sessions-task-handoff-2"
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoffs (
			id, task_id, requested_by, requested_at, request_report,
			received_by, received_at, completed_report_at, complete_report
		) VALUES (?, ?, ?, ?, NULL, ?, ?, NULL, NULL)
	`, secondHandoffID, taskIDs[1], canonicalID, "now", canonicalID, "now"); err != nil {
		t.Fatalf("insert second task handoff: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.DB().ExecContext(ctx, `DELETE FROM task_handoffs WHERE id = ?`, secondHandoffID); err != nil {
			t.Errorf("delete second task handoff %q: %v", secondHandoffID, err)
		}
	})

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want one deduplicated executor: %+v", len(sessions), sessions)
	}
	if got := sessions[0]; got.SessionKey != "atct-goal-sessions-executor" || got.Role != "executor" || !got.HandoffOpen {
		t.Fatalf("unexpected executor session: %+v", got)
	}
}

func TestListGoalSessionsExcludesEmptySessionKeys(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-sessions-empty")
	addGoalHandoffDirect(t, s, "goal-sessions-empty-key-handoff", goalID, "goal-sessions-empty", "goal-sessions-empty")

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got sessions for an empty session key: %+v", sessions)
	}
}

func TestListGoalSessionsIncludesCompletedHandoffs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-sessions-completed")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-completed"), "atct-goal-sessions-completed")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	const handoffID = "goal-sessions-completed-handoff"
	addGoalHandoffDirect(t, s, handoffID, goalID, canonicalID, canonicalID)
	if _, err := s.DB().ExecContext(ctx, "UPDATE goal_handoffs SET completed_report_at = ? WHERE id = ?", "completed", handoffID); err != nil {
		t.Fatalf("complete direct goal handoff: %v", err)
	}

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].HandoffOpen {
		t.Fatalf("completed handoff was not returned as closed: %+v", sessions)
	}
}

func TestListGoalSessionsPrefersSubcommanderRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskIDs := addTestTasks(t, s, 1)
	var goalID int64
	if err := s.DB().QueryRowContext(ctx, "SELECT goal_id FROM tasks WHERE id = ?", taskIDs[0]).Scan(&goalID); err != nil {
		t.Fatalf("find task goal: %v", err)
	}
	addTestAgentSession(t, s, "goal-sessions-dual")

	canonicalID, _, err := s.IdentifyAgentSession(ctx, testSessionID("goal-sessions-dual"), "atct-goal-sessions-dual")
	if err != nil {
		t.Fatalf("IdentifyAgentSession failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "goal-sessions-dual-goal-handoff", goalID, canonicalID, canonicalID)
	addTaskHandoffDirect(t, s, "goal-sessions-dual-task-handoff", taskIDs[0], canonicalID, canonicalID)

	sessions, err := s.ListGoalSessions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalSessions failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Role != "subcommander" {
		t.Fatalf("subcommander role was not preferred: %+v", sessions)
	}
}

func TestGoalHandoffReportsAreStored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-report-requester")
	addTestAgentSession(t, s, "goal-report-receiver")

	requested, err := s.RequestGoalHandoff(ctx, "goal-report-handoff", goalID, testSessionID("goal-report-requester"), "Please take over the goal.")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if requested.RequestReport != "Please take over the goal." {
		t.Fatalf("request report = %q, want request body", requested.RequestReport)
	}

	if _, err := s.ReceiveGoalHandoff(ctx, requested.ID, goalID, testSessionID("goal-report-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, requested.ID, goalID, testSessionID("goal-report-receiver"), "ready for review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, requested.ID, goalID, testSessionID("goal-report-requester")); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, requested.ID, goalID, testSessionID("goal-report-requester"), "I completed the goal and verified it.")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}
	if completed.CompleteReport != "I completed the goal and verified it." {
		t.Fatalf("complete report = %q, want completion body", completed.CompleteReport)
	}
}

func TestCompleteGoalHandoffPublishesReportedEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "publish-goal-requester")
	addTestAgentSession(t, s, "publish-goal-receiver")
	handoffID := "publish-goal-handoff"
	addRequestOnlyGoalHandoff(t, s, handoffID, goalID, "publish-goal-requester")
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("publish-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoffID, goalID, testSessionID("publish-goal-receiver"), "ready for review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoffID, goalID, testSessionID("publish-goal-requester")); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	events, cancel := s.SubscribeEvents()
	defer cancel()

	const report = "goal completion report"
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, handoffID, goalID, testSessionID("publish-goal-requester"), report)
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}
	wakeup := waitForHandoffReported(t, events)

	if completed.ID != handoffID || completed.CompletedReportAt == nil || completed.CompleteReport != report {
		t.Fatalf("completed handoff = %+v, want report %q", completed, report)
	}
	if wakeup.WakeupID == "" || wakeup.ProjectID != goal.ProjectID || wakeup.GoalID != goalID || wakeup.TaskID != 0 || wakeup.HandoffID != handoffID || wakeup.CompleteReport != report {
		t.Fatalf("reported wakeup = %+v, want project=%d goal=%d task=0 handoff=%q report=%q", wakeup, goal.ProjectID, goalID, handoffID, report)
	}
}

func TestCompleteGoalHandoffDoesNotPublishSelfClaimReportedEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "self-claim-goal")
	const handoffID = "self-claim-goal-handoff"
	addGoalHandoffDirect(t, s, handoffID, goalID, "self-claim-goal", "self-claim-goal")

	events, cancel := s.SubscribeEvents()
	defer cancel()
	if _, err := s.CompleteGoalHandoff(ctx, handoffID, goalID, "self claim completed"); err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}
	expectNoHandoffReported(t, events)
}

func TestCompleteGoalHandoffSelfClaimCompletes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "self-claim-completes-goal")
	const handoffID = "self-claim-completes-goal-handoff"
	addGoalHandoffDirect(t, s, handoffID, goalID, "self-claim-completes-goal", "self-claim-completes-goal")

	completed, err := s.CompleteGoalHandoff(ctx, handoffID, goalID, "self claim completed")
	if err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}
	if completed.CompletedReportAt == nil {
		t.Fatalf("completed handoff has no completion time: %+v", completed)
	}
}

func TestCompleteGoalHandoffPublishesUnknownIdentityReportedEvent(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		requestedBy, receivedBy any
	}{
		{name: "unknown requester", requestedBy: nil, receivedBy: "known-goal-receiver"},
		{name: "unknown receiver", requestedBy: "known-goal-requester", receivedBy: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			goalID := newTestGoal(t, s)
			addTestAgentSession(t, s, "known-goal-requester")
			addTestAgentSession(t, s, "known-goal-receiver")
			const handoffID = "unknown-identity-goal-handoff"
			addGoalHandoffDirect(t, s, handoffID, goalID, tc.requestedBy, tc.receivedBy)

			events, cancel := s.SubscribeEvents()
			defer cancel()
			if _, err := s.CompleteGoalHandoff(ctx, handoffID, goalID, goalHandoffReclaimedReport); err != nil {
				t.Fatalf("CompleteGoalHandoff: %v", err)
			}
			waitForHandoffReported(t, events)
		})
	}
}

func TestWithdrawActiveGoalDoesNotPublishReportedTaskHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	tasks, err := s.CreateTasks(ctx, goalID, "withdraw-test", "withdraw-goal", []string{"Open task"}, []string{"Task remains open during withdrawal."})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	addTestAgentSession(t, s, "withdraw-requester")
	addTestAgentSession(t, s, "withdraw-receiver")
	const handoffID = "withdraw-task-handoff"
	addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, "withdraw-requester", "withdraw-receiver")

	events, cancel := s.SubscribeEvents()
	defer cancel()
	if err := s.WithdrawActiveGoal(ctx, goalID, "withdraw the goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	expectNoHandoffReported(t, events)
}

func TestGoalHandoffCompletionRejectsEmptyReport(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-empty-report-requester")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-empty-report-handoff", goalID, testSessionID("goal-empty-report-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff without report: %v", err)
	}
	if handoff.RequestReport != "" {
		t.Fatalf("omitted request report = %q, want empty", handoff.RequestReport)
	}

	_, err = s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "")
	if err == nil {
		t.Fatal("CompleteGoalHandoff unexpectedly accepted an empty complete report")
	}
	if !strings.Contains(err.Error(), "complete_report") {
		t.Fatalf("empty report error = %q, want complete_report", err)
	}
}

func TestGoalHandoffCompletionRejectsWhitespaceOnlyReport(t *testing.T) {
	for _, report := range []string{" ", "　", "\n", "\t"} {
		t.Run(fmt.Sprintf("%q", report), func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			goalID := newTestGoal(t, s)
			addLiveProjectClaim(t, s, goalID, "goal-whitespace-report-requester")
			handoff, err := s.RequestGoalHandoff(ctx, "goal-whitespace-report-handoff", goalID, testSessionID("goal-whitespace-report-requester"), "")
			if err != nil {
				t.Fatalf("RequestGoalHandoff: %v", err)
			}
			if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, report); err == nil {
				t.Fatalf("CompleteGoalHandoff unexpectedly accepted whitespace-only report %q", report)
			}
		})
	}
}

func TestGoalHandoffAllowsSecondHandoffForSameGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-dead-receiver")
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?
	`, 999999, "dead", testSessionID("goal-dead-receiver")); err != nil {
		t.Fatalf("dead receiver session fixture update failed: %v", err)
	}
	expireTestAgentSessionLease(t, s, testSessionID("goal-dead-receiver"))

	first, err := s.RequestGoalHandoff(ctx, "goal-handoff-1", goalID, testSessionID("goal-requester"), "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, testSessionID("goal-dead-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	second, err := s.RequestGoalHandoff(ctx, "goal-handoff-2", goalID, testSessionID("goal-requester"), "")
	if err != nil {
		t.Fatalf("second RequestGoalHandoff failed: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("same goal handoffs must have distinct IDs: %q", first.ID)
	}
	first, err = s.GetGoalHandoff(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff for reclaimed handoff failed: %v", err)
	}
	if first.CompletedReportAt == nil || first.CompleteReport != "セッションが停止した" {
		t.Fatalf("dead receiver handoff was not reclaimed: %+v", first)
	}

	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs failed: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("got %d handoffs, want 2: %+v", len(handoffs), handoffs)
	}
}

func TestGoalHandoffRejectsSecondHandoffForLiveReceiver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-live-receiver-requester")
	goalLiveReceiverID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}

	first, err := s.RequestGoalHandoff(ctx, "goal-live-receiver-handoff-1", goalID, testSessionID("goal-live-receiver-requester"), "")
	if err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, first.ID, goalID, goalLiveReceiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "goal-live-receiver-handoff-2", goalID, testSessionID("goal-live-receiver-requester"), ""); err == nil {
		t.Fatal("RequestGoalHandoff should reject takeover from a live receiver")
	}
}

func TestGoalHandoffReceiveByGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")

	requested, err := s.RequestGoalHandoff(ctx, "goal-receive-by-goal", goalID, testSessionID("goal-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}

	received, err := s.ReceiveGoalHandoffForGoal(ctx, goalID, testSessionID("goal-receiver"))
	if err != nil {
		t.Fatalf("ReceiveGoalHandoffForGoal failed: %v", err)
	}
	if received.ID != requested.ID || received.ReceivedBy != testSessionID("goal-receiver") {
		t.Fatalf("unexpected received handoff: %+v", received)
	}
}

func TestGoalHandoffReceiveRejectsUnrequestedHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	handoffID := "goal-receipt-only"
	addTestAgentSession(t, s, "receiver")
	addReceiptOnlyGoalHandoff(t, s, handoffID, goalID, "receiver")

	_, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, testSessionID("receiver"))
	if !errors.Is(err, ErrGoalHandoffNotFound) {
		t.Fatalf("error = %v, want ErrGoalHandoffNotFound", err)
	}
}

func TestGoalHandoffReceiveRejectsUnknownUUIDNotFound(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	unknownHandoffID := "9dfc8983-f430-4c7f-92a0-3882941dd393"

	_, err := s.ReceiveGoalHandoff(context.Background(), unknownHandoffID, goalID, testSessionID("receiver"))
	if !errors.Is(err, ErrGoalHandoffNotFound) {
		t.Fatalf("error = %v, want ErrGoalHandoffNotFound", err)
	}
}

func TestGoalHandoffCompleteByGoal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "complete-by-goal-requester")

	addRequestOnlyGoalHandoff(t, s, "complete-by-goal", goalID, "complete-by-goal-requester")
	requested, err := s.GetGoalHandoff(ctx, "complete-by-goal")
	if err != nil {
		t.Fatalf("GetGoalHandoff failed: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, requested.ID, goalID, testSessionID("complete-by-goal-requester")); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}

	completed, err := s.CompleteGoalHandoffForGoal(ctx, goalID, "completed by goal ID")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffForGoal failed: %v", err)
	}
	if completed.ID != requested.ID || completed.GoalID != goalID || completed.CompletedReportAt == nil {
		t.Fatalf("unexpected completed handoff: %+v", completed)
	}
	if completed.CompleteReport != "completed by goal ID" {
		t.Fatalf("complete report = %q, want goal-ID completion report", completed.CompleteReport)
	}
}

func TestRejectCompletionReopensCompletedGoalHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	const requester = "completion-reopen-requester"
	const receiver = "completion-reopen-receiver"
	const handoffID = "completion-reopen-handoff"
	addLiveProjectClaim(t, s, goalID, requester)
	addTestAgentSession(t, s, receiver)

	original := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, handoffID, goalID, testSessionID(requester), testSessionID(receiver))
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, handoffID, goalID, testSessionID(requester), "completed before rejection")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}
	original = completed

	decision, err := s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone:    "completion handoff reopening",
		NowPossible: "continue work",
		HowToVerify: "run store tests",
		Surprises:   "none",
		NeedsReview: "no",
		NextSteps:   "continue",
	}, testSessionID(receiver))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	const reason = "不足している検証を追加する"
	if err := s.RejectCompletion(ctx, decision.ID, reason); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}

	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs: %v", err)
	}
	var open []GoalHandoff
	for _, handoff := range handoffs {
		if handoff.CompletedReportAt == nil {
			open = append(open, handoff)
		}
	}
	if len(open) != 1 {
		t.Fatalf("got %d open handoffs, want 1: %+v", len(open), handoffs)
	}
	if want := fmt.Sprintf("%s-reopen-%d", original.ID, decision.ID); open[0].ID != want {
		t.Fatalf("reopened handoff ID = %q, want %q", open[0].ID, want)
	}
	if open[0].RequestedBy != original.RequestedBy || open[0].ReceivedBy != original.ReceivedBy {
		t.Fatalf("reopened handoff ownership = requested_by:%d received_by:%d, want requested_by:%d received_by:%d", open[0].RequestedBy, open[0].ReceivedBy, original.RequestedBy, original.ReceivedBy)
	}
	if open[0].RequestedAt == nil || open[0].ReceivedAt == nil {
		t.Fatalf("reopened handoff is not received: %+v", open[0])
	}
	if open[0].RequestReport != "完了報告が却下されたため handoff を再発行した: "+reason {
		t.Fatalf("reopened handoff request report = %q", open[0].RequestReport)
	}
}

func TestRejectCompletionDoesNotReopenWhenGoalHandoffIsAlreadyOpen(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	const requester = "completion-open-requester"
	const receiver = "completion-open-receiver"
	const handoffID = "completion-open-handoff"
	addLiveProjectClaim(t, s, goalID, requester)
	addTestAgentSession(t, s, receiver)

	requested, err := s.RequestGoalHandoff(ctx, handoffID, goalID, testSessionID(requester), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, requested.ID, goalID, testSessionID(receiver)); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	decision, err := s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone:    "completion with an open handoff",
		NowPossible: "continue work",
		HowToVerify: "run store tests",
		Surprises:   "none",
		NeedsReview: "no",
		NextSteps:   "continue",
	}, testSessionID(receiver))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if err := s.RejectCompletion(ctx, decision.ID, "keep the existing owner"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}

	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs: %v", err)
	}
	if len(handoffs) != 1 || handoffs[0].ID != requested.ID || handoffs[0].CompletedReportAt != nil {
		t.Fatalf("handoffs after rejection = %+v, want the original open handoff only", handoffs)
	}
}

func TestRejectCompletionDoesNotReopenWithoutCompletedGoalHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	const receiver = "completion-no-handoff-receiver"
	addTestAgentSession(t, s, receiver)

	decision, err := s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone:    "completion without a handoff",
		NowPossible: "continue work",
		HowToVerify: "run store tests",
		Surprises:   "none",
		NeedsReview: "no",
		NextSteps:   "continue",
	}, testSessionID(receiver))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if err := s.RejectCompletion(ctx, decision.ID, "report from an unassigned session"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}

	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs: %v", err)
	}
	for _, handoff := range handoffs {
		if handoff.CompletedReportAt == nil {
			t.Fatalf("unexpected open handoff after rejection: %+v", handoff)
		}
	}
}

func TestRejectCompletionDoesNotReopenAnotherSessionsGoalHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	const requester = "completion-other-session-requester"
	const owner = "completion-other-session-owner"
	const reporter = "completion-other-session-reporter"
	const handoffID = "completion-other-session-handoff"
	addLiveProjectClaim(t, s, goalID, requester)
	addTestAgentSession(t, s, owner)
	addTestAgentSession(t, s, reporter)

	completed := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, handoffID, goalID, testSessionID(requester), testSessionID(owner))
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, handoffID, goalID, testSessionID(requester), "completed by the original owner")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}
	if completed.ReceivedBy != testSessionID(owner) {
		t.Fatalf("completed handoff receiver = %d, want owner %d", completed.ReceivedBy, testSessionID(owner))
	}

	before, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs before rejection: %v", err)
	}
	decision, err := s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone:    "completion from another session",
		NowPossible: "continue work",
		HowToVerify: "run store tests",
		Surprises:   "none",
		NeedsReview: "no",
		NextSteps:   "continue",
	}, testSessionID(reporter))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if err := s.RejectCompletion(ctx, decision.ID, "the original owner must keep the handoff"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}

	after, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs after rejection: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("handoff count after rejection = %d, want unchanged count %d: before=%+v after=%+v", len(after), len(before), before, after)
	}
	for _, handoff := range after {
		if handoff.CompletedReportAt == nil {
			t.Fatalf("unexpected open handoff after another session's rejection: %+v", handoff)
		}
	}
}

func TestApproveCompletionDoesNotReopenGoalHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	const requester = "completion-approval-requester"
	const receiver = "completion-approval-receiver"
	addLiveProjectClaim(t, s, goalID, requester)
	addTestAgentSession(t, s, receiver)

	handoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "completion-approval-handoff", goalID, testSessionID(requester), testSessionID(receiver))
	if _, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goalID, testSessionID(requester), "completed before approval"); err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}

	decision, err := s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone:    "completion approved without a handoff",
		NowPossible: "finish the goal",
		HowToVerify: "run store tests",
		Surprises:   "none",
		NeedsReview: "no",
		NextSteps:   "none",
	}, testSessionID(receiver))
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if _, err := s.ApproveCompletion(ctx, decision.ID); err != nil {
		t.Fatalf("ApproveCompletion: %v", err)
	}

	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs: %v", err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("got %d handoffs after approval, want the completed handoff: %+v", len(handoffs), handoffs)
	}
	for _, handoff := range handoffs {
		if handoff.CompletedReportAt == nil {
			t.Fatalf("unexpected open handoff after approval: %+v", handoff)
		}
	}
}

func TestGoalHandoffAmendReportUpdatesCompletedHandoffWithoutChangingCompletionTime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "amend-goal-requester")
	addTestAgentSession(t, s, "amend-goal-receiver")
	handoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "amend-goal-handoff", goalID, testSessionID("amend-goal-requester"), testSessionID("amend-goal-receiver"))
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goalID, testSessionID("amend-goal-requester"), "original report")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}

	amended, err := s.AmendGoalHandoffReport(ctx, handoff.ID, goalID, "amended report")
	if err != nil {
		t.Fatalf("AmendGoalHandoffReport: %v", err)
	}
	if amended.CompleteReport != "amended report" {
		t.Fatalf("complete report = %q, want amended report", amended.CompleteReport)
	}
	if amended.CompletedReportAt == nil || !amended.CompletedReportAt.Equal(*completed.CompletedReportAt) {
		t.Fatalf("completed report timestamp = %v, want %v", amended.CompletedReportAt, completed.CompletedReportAt)
	}
}

func TestGoalHandoffAmendReportRejectsIncompleteHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "amend-incomplete-goal-requester")
	addTestAgentSession(t, s, "amend-incomplete-goal-receiver")
	handoff, err := s.RequestGoalHandoff(ctx, "amend-incomplete-goal-handoff", goalID, testSessionID("amend-incomplete-goal-requester"), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID("amend-incomplete-goal-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	_, err = s.AmendGoalHandoffReport(ctx, handoff.ID, goalID, "replacement report")
	if err == nil || !strings.Contains(err.Error(), "handoff_complete") {
		t.Fatalf("error = %v, want incomplete handoff to name handoff_complete", err)
	}
}

func TestGoalHandoffCompleteByGoalRejectsUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	handoffID := "complete-unreceived-goal"
	addTestAgentSession(t, s, "complete-unreceived-goal-requester")
	addRequestOnlyGoalHandoff(t, s, handoffID, goalID, "complete-unreceived-goal-requester")

	_, err := s.CompleteGoalHandoffForGoal(ctx, goalID, "should not complete")
	if !errors.Is(err, ErrGoalHandoffNotFound) {
		t.Fatalf("error = %v, want ErrGoalHandoffNotFound", err)
	}

	handoff, err := s.GetGoalHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetGoalHandoff failed: %v", err)
	}
	if handoff.CompletedReportAt != nil {
		t.Fatalf("unreceived handoff has completion time: %+v", handoff)
	}
	if handoff.CompleteReport != "" {
		t.Fatalf("unreceived handoff complete report = %q, want empty", handoff.CompleteReport)
	}
}

func TestGoalHandoffCompleteByGoalRejectsMultipleReceivedIncomplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "complete-goal-ambiguous-requester")
	addTestAgentSession(t, s, "complete-goal-ambiguous-receiver")

	addRequestOnlyGoalHandoff(t, s, "complete-goal-ambiguous-1", goalID, "complete-goal-ambiguous-requester")
	if _, err := s.ReceiveGoalHandoff(ctx, "complete-goal-ambiguous-1", goalID, testSessionID("complete-goal-ambiguous-receiver")); err != nil {
		t.Fatalf("ReceiveGoalHandoff failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "complete-goal-ambiguous-2", goalID, "complete-goal-ambiguous-requester", "complete-goal-ambiguous-receiver")

	_, err := s.CompleteGoalHandoffForGoal(ctx, goalID, "ambiguous goal handoff completion fixture")
	if !errors.Is(err, ErrGoalHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrGoalHandoffAmbiguous", err)
	}
}

func TestGoalHandoffReceiveByGoalRejectsMultipleUnreceived(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-requester")
	addTestAgentSession(t, s, "goal-receiver")

	if _, err := s.RequestGoalHandoff(ctx, "goal-ambiguous-1", goalID, testSessionID("goal-requester"), ""); err != nil {
		t.Fatalf("RequestGoalHandoff failed: %v", err)
	}
	addGoalHandoffDirect(t, s, "goal-ambiguous-2", goalID, "goal-requester", "")

	_, err := s.ReceiveGoalHandoffForGoal(ctx, goalID, testSessionID("goal-receiver"))
	if !errors.Is(err, ErrGoalHandoffAmbiguous) {
		t.Fatalf("error = %v, want ErrGoalHandoffAmbiguous", err)
	}
}

func TestGoalHandoffAllowsNewHandoffAfterCompletion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "completed-goal-requester")
	addTestAgentSession(t, s, "completed-goal-receiver")

	first := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "completed-goal-1", goalID, testSessionID("completed-goal-requester"), testSessionID("completed-goal-receiver"))
	if _, err := s.CompleteGoalHandoffByReviewer(ctx, first.ID, goalID, testSessionID("completed-goal-requester"), "done"); err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "completed-goal-2", goalID, testSessionID("completed-goal-requester"), ""); err != nil {
		t.Fatalf("new handoff after completion failed: %v", err)
	}
}

func TestGoalHandoffCompletionDoesNotOverwriteReportedHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "overwrite-goal-requester")
	addTestAgentSession(t, s, "overwrite-goal-receiver")
	handoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "overwrite-goal-handoff", goalID, testSessionID("overwrite-goal-requester"), testSessionID("overwrite-goal-receiver"))
	if _, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goalID, testSessionID("overwrite-goal-requester"), "original report"); err != nil {
		t.Fatalf("first CompleteGoalHandoffByReviewer: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "replacement report"); err == nil {
		t.Fatal("second CompleteGoalHandoff unexpectedly overwrote the completed handoff")
	}
	stored, err := s.GetGoalHandoff(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff: %v", err)
	}
	if stored.CompleteReport != "original report" {
		t.Fatalf("complete report = %q, want original report", stored.CompleteReport)
	}
}

func TestGoalHandoffRejectsMultipleOpenHandoffsInDatabase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "goal-database-requester")
	if _, err := s.RequestGoalHandoff(ctx, "goal-database-handoff-1", goalID, testSessionID("goal-database-requester"), ""); err != nil {
		t.Fatalf("first RequestGoalHandoff failed: %v", err)
	}

	err := sqlcgen.New(s.DB()).RequestGoalHandoff(ctx, sqlcgen.RequestGoalHandoffParams{
		ID:            "goal-database-handoff-2",
		GoalID:        goalID,
		RequestedBy:   sql.NullInt64{Int64: testSessionID("goal-database-requester"), Valid: true},
		RequestedAt:   sql.NullString{String: time.Now().UTC().Format(time.RFC3339Nano), Valid: true},
		RequestReport: sql.NullString{},
	})
	if err == nil {
		t.Fatal("database should reject a second open handoff for the same goal")
	}
}

func TestGoalHandoffRejectsMissingGoal(t *testing.T) {
	s := newTestStore(t)
	addTestAgentSession(t, s, "goal-requester")

	if _, err := s.RequestGoalHandoff(context.Background(), "goal-handoff-missing", 0, testSessionID("goal-requester"), ""); err == nil {
		t.Fatal("RequestGoalHandoff should reject a missing goal")
	}
}

func TestGoalHandoffRequiresRequesterProjectClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	requesterID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession failed: %v", err)
	}
	if _, err := s.ClaimProject(ctx, goal.ProjectID, requesterID); err != nil {
		t.Fatalf("ClaimProject failed: %v", err)
	}

	if _, err := s.RequestGoalHandoff(ctx, "goal-handoff-project-owner", goalID, requesterID, ""); err != nil {
		t.Fatalf("RequestGoalHandoff with requester project claim failed: %v", err)
	}
}

func TestGoalHandoffRejectsUnclaimedGoal(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	addTestAgentSession(t, s, "goal-requester")

	_, err := s.RequestGoalHandoff(context.Background(), "goal-handoff-unclaimed", goalID, testSessionID("goal-requester"), "")
	if !errors.Is(err, ErrGoalHandoffProjectNotHeld) {
		t.Fatalf("error = %v, want ErrGoalHandoffProjectNotHeld", err)
	}
}

func requireGoalReviewHandoffIncomplete(t *testing.T, s *Store, ctx context.Context, goalID, requesterID int64, stage string) {
	t.Helper()
	if _, err := s.RequestGoalReview(ctx, goalID, requesterID, goalReviewRequestTestReport()); !errors.Is(err, ErrGoalReviewHandoffIncomplete) {
		t.Fatalf("RequestGoalReview at %s error = %v, want ErrGoalReviewHandoffIncomplete", stage, err)
	}
}

func goalReviewRequestTestReport() domain.CompletionReport {
	return domain.CompletionReport{
		WorkDone:    "goal review work",
		NowPossible: "goal review result",
		HowToVerify: "run goal review tests",
		Surprises:   "none",
		NeedsReview: "none",
		NextSteps:   "merge",
	}
}

func completeGoalHandoffReviewForGoalReviewTest(t *testing.T, s *Store, ctx context.Context, handoffID string, goalID, requesterID, receiverID int64) GoalHandoff {
	t.Helper()
	handoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, handoffID, goalID, requesterID, receiverID)
	completed, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goalID, requesterID, "commander completed the reviewed goal handoff")
	if err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer %q: %v", handoffID, err)
	}
	return completed
}

func receiveGoalHandoffReviewForGoalReviewTest(t *testing.T, s *Store, ctx context.Context, handoffID string, goalID, requesterID, receiverID int64) GoalHandoff {
	t.Helper()
	handoff, err := s.RequestGoalHandoff(ctx, handoffID, goalID, requesterID, "delegate goal work")
	if err != nil {
		t.Fatalf("RequestGoalHandoff %q: %v", handoffID, err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff %q: %v", handoffID, err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "goal work is ready for commander review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview %q: %v", handoffID, err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, requesterID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview %q: %v", handoffID, err)
	}
	return handoff
}

func TestRequestGoalReviewRejectsWithoutDelegatedGoalHandoff(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	if _, err := s.RequestGoalReview(context.Background(), goalID, testSessionID("goal-review-no-handoff"), goalReviewRequestTestReport()); !errors.Is(err, ErrGoalReviewHandoffIncomplete) {
		t.Fatalf("RequestGoalReview without delegated handoff error = %v, want ErrGoalReviewHandoffIncomplete", err)
	}
}

func TestRequestGoalReviewAllowsReceivedGoalHandoffReview(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	requesterID := testSessionID("goal-review-guard-requester")
	receiverID := testSessionID("goal-review-guard-receiver")
	addLiveProjectClaim(t, s, goalID, "goal-review-guard-requester")
	addTestAgentSession(t, s, "goal-review-guard-receiver")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-review-guard-received", goalID, requesterID, "delegate before review")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	requireGoalReviewHandoffIncomplete(t, s, ctx, goalID, requesterID, "received")

	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "goal work is ready for commander review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	requireGoalReviewHandoffIncomplete(t, s, ctx, goalID, requesterID, "review requested")

	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, requesterID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	review, err := s.RequestGoalReview(ctx, goalID, requesterID, goalReviewRequestTestReport())
	if err != nil {
		t.Fatalf("RequestGoalReview after review receipt: %v", err)
	}
	if review.Status != domain.DecisionOpen {
		t.Fatalf("goal review = %+v, want open review", review)
	}
	stored, err := s.GetGoalHandoff(ctx, handoff.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff after review request: %v", err)
	}
	if stored.CompletedReportAt != nil {
		t.Fatalf("goal handoff = %+v, want open handoff during human review", stored)
	}
}

func TestGoalReviewCompletionUsesRecordedReviewerLineage(t *testing.T) {
	now := time.Now()
	handoff := &GoalHandoff{
		RequestedAt:       &now,
		ReceivedAt:        &now,
		ReviewRequestedAt: &now,
		ReviewReceivedAt:  &now,
		CompletedReportAt: &now,
		ReviewRequestedBy: 2,
		ReceivedBy:        2,
		RequestedBy:       1,
		ReviewReceivedBy:  3,
		CompleteReport:    "accepted",
	}
	if !goalHandoffHasCommanderReviewCompletion(handoff, 3) {
		t.Fatal("recorded reviewer should authorize the completed handoff despite stale requester lineage")
	}
}

func TestGoalReviewUsesLiveReviewerAfterRequesterTurnover(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "lineage-stale-requester")
	requesterID := testSessionID("lineage-stale-requester")
	receiverID := registerNamedTestAgentSession(t, s, "lineage-live-receiver", os.Getpid())
	reviewerID := registerNamedTestAgentSession(t, s, "lineage-current-reviewer", os.Getpid())

	handoff, err := s.RequestGoalHandoff(ctx, "lineage-turnover", goalID, requesterID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, receiverID, "ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE projects SET claimed_by = ? WHERE id = ?`, reviewerID, goal.ProjectID); err != nil {
		t.Fatalf("turn over commander: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, reviewerID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	if _, err := s.RequestGoalReview(ctx, goalID, reviewerID, goalReviewRequestTestReport()); err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
}

func TestRecoverGoalHandoffClearsOnlyDefinitelyStaleReviewer(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	staleCommanderID := registerNamedTestAgentSession(t, s, "recover-goal-stale-commander", os.Getpid())
	freshCommanderID := registerNamedTestAgentSession(t, s, "recover-goal-fresh-commander", os.Getpid())
	subcommanderID := registerNamedTestAgentSession(t, s, "recover-goal-subcommander", os.Getpid())
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE projects SET claimed_by = ? WHERE id = ?`, staleCommanderID, goal.ProjectID); err != nil {
		t.Fatalf("claim project for stale commander: %v", err)
	}
	handoff, err := s.RequestGoalHandoff(ctx, "recover-goal-review", goalID, staleCommanderID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goalID, subcommanderID, "ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, staleCommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE projects SET claimed_by = ? WHERE id = ?`, freshCommanderID, goal.ProjectID); err != nil {
		t.Fatalf("turn over commander: %v", err)
	}
	if _, err := s.RecoverGoalHandoff(ctx, handoff.ID, goalID, freshCommanderID, "live reviewer"); !errors.Is(err, ErrSessionRecoveryNotProven) {
		t.Fatalf("RecoverGoalHandoff with live reviewer error = %v, want ErrSessionRecoveryNotProven", err)
	}
	expireTestSessionLease(t, s, staleCommanderID)

	recovered, err := s.RecoverGoalHandoff(ctx, handoff.ID, goalID, freshCommanderID, "reviewer stopped heartbeating")
	if err != nil {
		t.Fatalf("RecoverGoalHandoff: %v", err)
	}
	if recovered.ReviewReceivedAt != nil || recovered.ReviewReceivedBy != 0 || recovered.ReceivedBy != subcommanderID || recovered.ReviewRequestedBy != subcommanderID || recovered.ReviewRequestReport != "ready" {
		t.Fatalf("recovered goal handoff = %+v, want only reviewer receipt cleared", recovered)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goalID, freshCommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview after recovery: %v", err)
	}
}

func TestRecoverGoalHandoffTerminalizesStaleReceiverAndAllowsReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "recover-goal-receiver-current")
	currentCommanderID := testSessionID("recover-goal-receiver-current")
	staleReceiverID := addStaleRecoverySession(t, s, "recover-goal-receiver-stale")

	handoff, err := s.RequestGoalHandoff(ctx, "recover-goal-receiver-old", goalID, currentCommanderID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, staleReceiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	recovered, err := s.RecoverGoalHandoff(ctx, handoff.ID, goalID, currentCommanderID, "receiver session disappeared")
	if err != nil {
		t.Fatalf("RecoverGoalHandoff: %v", err)
	}
	if recovered.RecoveredAt == nil || recovered.RecoveryReport != "receiver session disappeared" || recovered.ReceivedBy != staleReceiverID || recovered.CompletedReportAt != nil {
		t.Fatalf("recovered goal handoff = %+v, want terminal recovery without normal completion", recovered)
	}
	open, err := s.ListOpenGoalHandoffs(ctx)
	if err != nil {
		t.Fatalf("ListOpenGoalHandoffs: %v", err)
	}
	if _, ok := open[goalID]; ok {
		t.Fatalf("recovered goal handoff remains open: %+v", open[goalID])
	}
	replacement, err := s.RequestGoalHandoff(ctx, "recover-goal-receiver-new", goalID, currentCommanderID, "replacement")
	if err != nil {
		t.Fatalf("RequestGoalHandoff replacement: %v", err)
	}
	if replacement.ID != "recover-goal-receiver-new" {
		t.Fatalf("replacement goal handoff = %+v", replacement)
	}
}

func TestRecoverRequestedGoalHandoffAllowsReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "recover-goal-request-current")
	currentCommanderID := testSessionID("recover-goal-request-current")
	staleRequesterID := addStaleRecoverySession(t, s, "recover-goal-request-stale")
	handoffID := "recover-goal-request-old"
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO goal_handoffs (id, goal_id, requested_by, requested_at, request_report)
		VALUES (?, ?, ?, ?, ?)`, handoffID, goalID, staleRequesterID, time.Now().UTC().Format(time.RFC3339Nano), "delegate"); err != nil {
		t.Fatalf("insert requested goal handoff: %v", err)
	}

	recovered, err := s.RecoverGoalHandoff(ctx, handoffID, goalID, currentCommanderID, "requester session disappeared")
	if err != nil {
		t.Fatalf("RecoverGoalHandoff requested: %v", err)
	}
	if recovered.RecoveredAt == nil || recovered.RequestedBy != staleRequesterID || recovered.ReceivedAt != nil {
		t.Fatalf("requested goal recovery changed request state: %+v", recovered)
	}
	if _, err := s.RequestGoalHandoff(ctx, "recover-goal-request-new", goalID, currentCommanderID, "replacement"); err != nil {
		t.Fatalf("RequestGoalHandoff replacement: %v", err)
	}
}

func TestRejectedGoalReviewReusesReceivedHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	requesterID := testSessionID("goal-review-reject-requester")
	receiverID := testSessionID("goal-review-reject-receiver")
	addLiveProjectClaim(t, s, goalID, "goal-review-reject-requester")
	addTestAgentSession(t, s, "goal-review-reject-receiver")

	original := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "goal-review-reject-original", goalID, requesterID, receiverID)
	if original.CompletedReportAt != nil {
		t.Fatalf("original goal handoff = %+v, want open handoff", original)
	}
	review, err := s.RequestGoalReview(ctx, goalID, requesterID, goalReviewRequestTestReport())
	if err != nil {
		t.Fatalf("RequestGoalReview before rejection: %v", err)
	}
	if err := s.RejectGoalReview(ctx, review.ID, "needs another review"); err != nil {
		t.Fatalf("RejectGoalReview: %v", err)
	}

	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal after rejection: %v", err)
	}
	wantReport := goalReviewRequestTestReport()
	if goal.Status != domain.GoalActive || goal.WorkDone != wantReport.WorkDone || goal.NowPossible != wantReport.NowPossible || goal.HowToVerify != wantReport.HowToVerify || goal.Surprises != wantReport.Surprises || goal.NeedsReview != wantReport.NeedsReview || goal.NextSteps != wantReport.NextSteps || goal.ResultSummary != wantReport.WorkDone {
		t.Fatalf("goal after rejection = %+v, want active with request-time report %+v", goal, wantReport)
	}
	originalAfterReject, err := s.GetGoalHandoff(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff after rejection: %v", err)
	}
	if originalAfterReject.CompletedReportAt != nil {
		t.Fatalf("original handoff after rejection = %+v, want open handoff", originalAfterReject)
	}
	requireGoalReviewHandoffIncomplete(t, s, ctx, goalID, requesterID, "direct retry after human rejection")

	if _, err := s.RejectGoalHandoffReview(ctx, original.ID, goalID, requesterID, "needs another review"); err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReviewRejection(ctx, original.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, original.ID, goalID, receiverID, "revised goal is ready for commander review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview revised: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, original.ID, goalID, requesterID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview revised: %v", err)
	}

	retry, err := s.RequestGoalReview(ctx, goalID, requesterID, goalReviewRequestTestReport())
	if err != nil {
		t.Fatalf("RequestGoalReview after same-handoff resubmission: %v", err)
	}
	if retry.ID == review.ID || retry.Kind != domain.KindGoalReview || retry.Status != domain.DecisionOpen {
		t.Fatalf("retry goal review = %+v, want a new open review", retry)
	}
}

func TestRequestGoalReviewRejectsPlainCompletedGoalHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	requesterID := testSessionID("goal-review-plain-completion-requester")
	receiverID := testSessionID("goal-review-plain-completion-receiver")
	addLiveProjectClaim(t, s, goalID, "goal-review-plain-completion-requester")
	addTestAgentSession(t, s, "goal-review-plain-completion-receiver")

	handoff, err := s.RequestGoalHandoff(ctx, "goal-review-plain-completion", goalID, requesterID, "delegate without review")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, goalHandoffReclaimedReport); err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}

	requireGoalReviewHandoffIncomplete(t, s, ctx, goalID, requesterID, "plain completion without handoff review")
}

func TestCompleteGoalWithReportKeepsKindCompletionIndependentOfGoalHandoffReview(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	requesterID := testSessionID("completion-kind-requester")
	receiverID := testSessionID("completion-kind-receiver")
	addLiveProjectClaim(t, s, goalID, "completion-kind-requester")
	addTestAgentSession(t, s, "completion-kind-receiver")

	handoff, err := s.RequestGoalHandoff(ctx, "completion-kind-open-handoff", goalID, requesterID, "delegate before legacy completion")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	d, err := s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone:    "legacy completion work",
		NowPossible: "legacy completion result",
		HowToVerify: "run the store tests",
		Surprises:   "none",
		NeedsReview: "none",
		NextSteps:   "merge",
	}, requesterID)
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if d.Kind != domain.KindCompletion || d.TaskID != 0 {
		t.Fatalf("completion decision = %+v, want taskless KindCompletion", d)
	}
	stored, err := s.GetDecision(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if stored.Kind != domain.KindCompletion {
		t.Fatalf("stored completion decision kind = %q, want %q", stored.Kind, domain.KindCompletion)
	}
}

func TestRejectedGoalReviewReplacesRequestTimeGoalReportOnSameHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	requesterID := testSessionID("request-time-reject-requester")
	receiverID := testSessionID("request-time-reject-receiver")
	addLiveProjectClaim(t, s, goalID, "request-time-reject-requester")
	addTestAgentSession(t, s, "request-time-reject-receiver")
	originalHandoff := receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "request-time-reject-original-handoff", goalID, requesterID, receiverID)
	firstReport := domain.CompletionReport{
		WorkDone:    "first work",
		NowPossible: "first result",
		HowToVerify: "first verify",
		Surprises:   "first surprise",
		NeedsReview: "first review",
		NextSteps:   "first next",
	}
	first, err := s.RequestGoalReview(ctx, goalID, requesterID, firstReport)
	if err != nil {
		t.Fatalf("RequestGoalReview first: %v", err)
	}
	if err := s.RejectGoalReview(ctx, first.ID, "revise the work"); err != nil {
		t.Fatalf("RejectGoalReview: %v", err)
	}
	goalAfterReject, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal after rejected review: %v", err)
	}
	if goalAfterReject.WorkDone != firstReport.WorkDone || goalAfterReject.NowPossible != firstReport.NowPossible || goalAfterReject.HowToVerify != firstReport.HowToVerify || goalAfterReject.Surprises != firstReport.Surprises || goalAfterReject.NeedsReview != firstReport.NeedsReview || goalAfterReject.NextSteps != firstReport.NextSteps || goalAfterReject.ResultSummary != firstReport.WorkDone {
		t.Fatalf("goal after rejected review = %+v, want request-time report %+v", goalAfterReject, firstReport)
	}
	handoffAfterReject, err := s.GetGoalHandoff(ctx, originalHandoff.ID)
	if err != nil {
		t.Fatalf("GetGoalHandoff after rejection: %v", err)
	}
	if handoffAfterReject.CompletedReportAt != nil {
		t.Fatalf("handoff after rejection = %+v, want open handoff", handoffAfterReject)
	}

	if _, err := s.RejectGoalHandoffReview(ctx, originalHandoff.ID, goalID, requesterID, "revise the work"); err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReviewRejection(ctx, originalHandoff.ID, goalID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}
	if _, err := s.RequestGoalHandoffReview(ctx, originalHandoff.ID, goalID, receiverID, "revised goal ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview revised: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, originalHandoff.ID, goalID, requesterID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview revised: %v", err)
	}
	secondReport := domain.CompletionReport{
		WorkDone:    "second work",
		NowPossible: "second result",
		HowToVerify: "second verify",
		Surprises:   "second surprise",
		NeedsReview: "second review",
		NextSteps:   "second next",
	}
	second, err := s.RequestGoalReview(ctx, goalID, requesterID, secondReport)
	if err != nil {
		t.Fatalf("RequestGoalReview revised: %v", err)
	}
	goalAfterReplacement, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal after revised review: %v", err)
	}
	if second.ID == first.ID || goalAfterReplacement.WorkDone != secondReport.WorkDone || goalAfterReplacement.NowPossible != secondReport.NowPossible || goalAfterReplacement.HowToVerify != secondReport.HowToVerify || goalAfterReplacement.Surprises != secondReport.Surprises || goalAfterReplacement.NeedsReview != secondReport.NeedsReview || goalAfterReplacement.NextSteps != secondReport.NextSteps || goalAfterReplacement.ResultSummary != secondReport.WorkDone {
		t.Fatalf("revised review = %+v, goal = %+v; want distinct review and request-time report %+v", second, goalAfterReplacement, secondReport)
	}
}

func TestCompleteGoalWithReportIgnoresHistoricalGoalReview(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	requesterID := testSessionID("legacy-completion-after-review-requester")
	receiverID := testSessionID("legacy-completion-after-review-receiver")
	addLiveProjectClaim(t, s, goalID, "legacy-completion-after-review-requester")
	addTestAgentSession(t, s, "legacy-completion-after-review-receiver")
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "legacy-completion-after-review-handoff", goalID, requesterID, receiverID)
	if review, err := s.RequestGoalReview(ctx, goalID, requesterID, domain.CompletionReport{
		WorkDone: "review work", NowPossible: "review result", HowToVerify: "review verify",
		Surprises: "review surprise", NeedsReview: "review needs", NextSteps: "review next",
	}); err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	} else if err := s.RejectGoalReview(ctx, review.ID, "keep legacy completion path"); err != nil {
		t.Fatalf("RejectGoalReview: %v", err)
	}

	completion, err := s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone: "legacy work", NowPossible: "legacy result", HowToVerify: "legacy verify",
		Surprises: "legacy surprise", NeedsReview: "legacy review", NextSteps: "legacy next",
	}, requesterID)
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if completion.Kind != domain.KindCompletion {
		t.Fatalf("completion decision kind = %q, want %q", completion.Kind, domain.KindCompletion)
	}
}
