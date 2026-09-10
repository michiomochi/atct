package store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestCanRecoverSessionRequiresDefiniteStaleProof(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		pid       int
		startedAt string
		wantProof bool
	}{
		{name: "live identity", pid: os.Getpid(), startedAt: "fake-start-" + strconv.Itoa(os.Getpid())},
		{name: "unknown identity", pid: 0, startedAt: ""},
		{name: "mismatched identity", pid: os.Getpid(), startedAt: "stale-start", wantProof: true},
		{name: "dead process", pid: 999999, startedAt: "dead-start", wantProof: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := testSessionID("recovery-" + tt.name)
			if _, err := s.DB().ExecContext(ctx, `
				INSERT INTO agent_sessions (id, pid, started_at, registered_at)
				VALUES (?, ?, ?, ?)`, id, tt.pid, tt.startedAt, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatalf("insert session: %v", err)
			}

			proof, err := s.CanRecoverSession(ctx, id)
			if (err == nil) != tt.wantProof {
				t.Fatalf("CanRecoverSession error = %v, want proof=%v", err, tt.wantProof)
			}
			if !tt.wantProof {
				return
			}
			if proof.SessionID != id {
				t.Fatalf("proof session_id = %d, want %d", proof.SessionID, id)
			}
		})
	}
}

func TestCanRecoverSessionRejectsIncompleteDiscardRecord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sessionID := testSessionID("recovery-incomplete-discard")
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO agent_sessions (id, pid, started_at, registered_at, discarded_by)
		VALUES (?, ?, ?, ?, ?)`, sessionID, 999999, "stale-start", time.Now().UTC().Format(time.RFC3339Nano), sessionID); err != nil {
		t.Fatalf("insert incomplete discard record: %v", err)
	}

	proof, err := s.CanRecoverSession(ctx, sessionID)
	if !errors.Is(err, ErrSessionRecoveryNotProven) {
		t.Fatalf("CanRecoverSession error = %v, want ErrSessionRecoveryNotProven", err)
	}
	if proof.SessionID != 0 {
		t.Fatalf("incomplete discard proof = %+v, want empty proof", proof)
	}
}

func TestSessionDiscardRequiresApprovedCommanderDecision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "session-discard", "/repos/session-discard")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Discard session", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commander, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(commander): %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, commander, project.ID); err != nil {
		t.Fatalf("Associate commander: %v", err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	target, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(target): %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, target, project.ID); err != nil {
		t.Fatalf("Associate target: %v", err)
	}

	request, err := s.RequestSessionDiscard(ctx, SessionDiscardRequest{
		ProjectID:       project.ID,
		GoalID:          goal.ID,
		TargetSessionID: target,
		RequestedBy:     commander,
		Reason:          "the session disappeared after its monitor crashed",
	})
	if err != nil {
		t.Fatalf("RequestSessionDiscard: %v", err)
	}
	if request.Kind != SessionDiscardDecisionKind || request.Status != "open" {
		t.Fatalf("discard decision = %+v, want open %q", request, SessionDiscardDecisionKind)
	}

	if err := s.DiscardSession(ctx, project.ID, target, request.ID, commander); !errors.Is(err, ErrSessionDiscardPending) {
		t.Fatalf("DiscardSession before approval error = %v, want ErrSessionDiscardPending", err)
	}
	proof, err := s.CanRecoverSession(ctx, target)
	if err == nil || proof.SessionID != 0 {
		t.Fatalf("target became recoverable before approval: proof=%+v err=%v", proof, err)
	}

	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: request.ID, AnswerLabel: "reject", AnswerText: "keep the session"}); err != nil {
		t.Fatalf("AnswerDecision(reject): %v", err)
	}
	if _, err := s.PollDecisions(ctx, commander, request.ID); err != nil {
		t.Fatalf("PollDecisions(reject): %v", err)
	}
	if err := s.DiscardSession(ctx, project.ID, target, request.ID, commander); !errors.Is(err, ErrSessionDiscardPending) {
		t.Fatalf("DiscardSession after rejection error = %v, want ErrSessionDiscardPending", err)
	}

	request, err = s.RequestSessionDiscard(ctx, SessionDiscardRequest{
		ProjectID:       project.ID,
		GoalID:          goal.ID,
		TargetSessionID: target,
		RequestedBy:     commander,
		Reason:          "the session was confirmed lost by the operator",
	})
	if err != nil {
		t.Fatalf("RequestSessionDiscard(approve): %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: request.ID, AnswerLabel: "approve"}); err != nil {
		t.Fatalf("AnswerDecision(approve): %v", err)
	}
	if _, err := s.PollDecisions(ctx, commander, request.ID); err != nil {
		t.Fatalf("PollDecisions(approve): %v", err)
	}
	if err := s.DiscardSession(ctx, project.ID, target, request.ID, commander); err != nil {
		t.Fatalf("DiscardSession(approve): %v", err)
	}

	proof, err = s.CanRecoverSession(ctx, target)
	if err != nil || proof.Kind != RecoveryProofSessionDiscard || proof.DiscardDecisionID != request.ID {
		t.Fatalf("discard proof = %+v, err=%v; want approved discard proof", proof, err)
	}
	if err := s.DiscardSession(ctx, project.ID, target, request.ID, commander); !errors.Is(err, ErrSessionDiscarded) {
		t.Fatalf("duplicate DiscardSession error = %v, want ErrSessionDiscarded", err)
	}
}

func TestIdentifyAgentSessionDoesNotReviveDiscardedSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "session-reattach-discard", "/repos/session-reattach-discard")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "Discarded reattach", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commander, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(commander): %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, commander, project.ID); err != nil {
		t.Fatalf("Associate commander: %v", err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	target, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(target): %v", err)
	}
	if _, _, err := s.IdentifyAgentSession(ctx, target, "discarded-key"); err != nil {
		t.Fatalf("IdentifyAgentSession(target): %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, target, project.ID); err != nil {
		t.Fatalf("Associate target: %v", err)
	}
	request, err := s.RequestSessionDiscard(ctx, SessionDiscardRequest{
		ProjectID:       project.ID,
		GoalID:          goal.ID,
		TargetSessionID: target,
		RequestedBy:     commander,
		Reason:          "the old stable session must stay revoked",
	})
	if err != nil {
		t.Fatalf("RequestSessionDiscard: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: request.ID, AnswerLabel: "approve"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	if _, err := s.PollDecisions(ctx, commander, request.ID); err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if err := s.DiscardSession(ctx, project.ID, target, request.ID, commander); err != nil {
		t.Fatalf("DiscardSession: %v", err)
	}

	replacement, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(replacement): %v", err)
	}
	canonical, reattached, err := s.IdentifyAgentSession(ctx, replacement, "discarded-key")
	if !errors.Is(err, ErrSessionDiscarded) {
		t.Fatalf("IdentifyAgentSession(discarded key) error = %v, want ErrSessionDiscarded", err)
	}
	if canonical != 0 || reattached {
		t.Fatalf("discarded key identify = (%d, %v), want (0, false)", canonical, reattached)
	}
}

func TestDiscardedSessionCannotReceiveTaskHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	requester, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(requester): %v", err)
	}
	receiver, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(receiver): %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_handoffs (id, task_id, requested_by, requested_at)
		VALUES (?, ?, ?, ?)`, "discard-fence", taskID, requester, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert requested handoff: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE agent_sessions
		SET discarded_at = ?, discarded_by = ?, discard_reason = ?
		WHERE id = ?`, time.Now().UTC().Format(time.RFC3339Nano), receiver, "operator confirmed loss", receiver); err != nil {
		t.Fatalf("mark receiver discarded: %v", err)
	}

	if _, err := s.ReceiveTaskHandoff(ctx, "discard-fence", taskID, receiver); !errors.Is(err, ErrSessionDiscarded) {
		t.Fatalf("ReceiveTaskHandoff discarded receiver error = %v, want ErrSessionDiscarded", err)
	}
	handoff, err := s.GetTaskHandoff(ctx, "discard-fence")
	if err != nil {
		t.Fatalf("GetTaskHandoff: %v", err)
	}
	if handoff.ReceivedAt != nil || handoff.ReceivedBy != 0 {
		t.Fatalf("discarded receipt changed handoff: %+v", handoff)
	}
}

func addStaleRecoverySession(t *testing.T, s *Store, label string) int64 {
	t.Helper()
	ctx := context.Background()
	id := testSessionID(label)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO agent_sessions (id, pid, started_at, registered_at)
		VALUES (?, ?, ?, ?)`, id, os.Getpid(), "stale-"+label, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert stale session %q: %v", label, err)
	}
	return id
}

func newTaskRecoveryGoal(t *testing.T, s *Store, label string) (goalID, holderID int64) {
	t.Helper()
	ctx := context.Background()
	goalID = newTestGoal(t, s)
	commanderLabel := label + "-commander"
	holderLabel := label + "-holder"
	addLiveProjectClaim(t, s, goalID, commanderLabel)
	holderID = registerNamedTestAgentSession(t, s, holderLabel, os.Getpid())
	handoff, err := s.RequestGoalHandoff(ctx, label+"-goal-handoff", goalID, testSessionID(commanderLabel), "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, holderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	return goalID, holderID
}

func TestRecoverTaskHandoffTerminalizesStaleOwnerAndAllowsReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID, holderID := newTaskRecoveryGoal(t, s, "task-recovery-received")
	tasks, err := s.CreateTasks(ctx, goalID, "worker", "recovery", []string{"implement"}, []string{"implement the recovery path"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	staleID := addStaleRecoverySession(t, s, "task-recovery-executor")
	handoffID := "task-recovery-received-old"
	addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, holderID, staleID)

	recovered, err := s.RecoverTaskHandoff(ctx, handoffID, tasks[0].ID, holderID, "executor session disappeared")
	if err != nil {
		t.Fatalf("RecoverTaskHandoff: %v", err)
	}
	if recovered.RecoveredAt == nil || recovered.RecoveryReport != "executor session disappeared" {
		t.Fatalf("recovered handoff = %+v, want recovery terminal fields", recovered)
	}
	if recovered.CompletedReportAt != nil || recovered.ReceivedBy != staleID || recovered.RequestedBy != holderID {
		t.Fatalf("recovery changed handoff lifecycle fields: %+v", recovered)
	}

	open, err := s.ListOpenTaskHandoffsForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("ListOpenTaskHandoffsForGoal: %v", err)
	}
	if _, ok := open[tasks[0].ID]; ok {
		t.Fatalf("recovered handoff remains open: %+v", open[tasks[0].ID])
	}
	replacement, err := s.RequestTaskHandoff(ctx, "task-recovery-received-new", tasks[0].ID, holderID, "replacement")
	if err != nil {
		t.Fatalf("RequestTaskHandoff replacement: %v", err)
	}
	if replacement.ID != "task-recovery-received-new" {
		t.Fatalf("replacement handoff = %+v", replacement)
	}

	retry, err := s.RecoverTaskHandoff(ctx, handoffID, tasks[0].ID, holderID, "different retry reason")
	if err != nil {
		t.Fatalf("RecoverTaskHandoff retry: %v", err)
	}
	if retry.RecoveredAt == nil || retry.RecoveryReport != "executor session disappeared" {
		t.Fatalf("retry rewrote recovery report: %+v", retry)
	}
	var tableCount int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'handoff_recoveries'`).Scan(&tableCount); err != nil {
		t.Fatalf("check dedicated recovery table: %v", err)
	}
	if tableCount != 0 {
		t.Fatal("recovery must be recorded only on the terminal handoff")
	}
}

func TestRecoverRequestedTaskHandoffAllowsReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID, holderID := newTaskRecoveryGoal(t, s, "task-recovery-requested")
	tasks, err := s.CreateTasks(ctx, goalID, "worker", "recovery", []string{"implement"}, []string{"implement the recovery path"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	staleID := addStaleRecoverySession(t, s, "task-recovery-requester")
	handoffID := "task-recovery-requested-old"
	addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, staleID, "")

	recovered, err := s.RecoverTaskHandoff(ctx, handoffID, tasks[0].ID, holderID, "requesting session disappeared")
	if err != nil {
		t.Fatalf("RecoverTaskHandoff requested: %v", err)
	}
	if recovered.RecoveredAt == nil || recovered.RequestedBy != staleID || recovered.ReceivedAt != nil {
		t.Fatalf("requested recovery changed request state: %+v", recovered)
	}
	if _, err := s.RequestTaskHandoff(ctx, "task-recovery-requested-new", tasks[0].ID, holderID, "replacement"); err != nil {
		t.Fatalf("RequestTaskHandoff replacement: %v", err)
	}
}

func TestRecoverTaskHandoffClearsStaleReviewReceipt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID, holderID := newTaskRecoveryGoal(t, s, "task-recovery-review")
	tasks, err := s.CreateTasks(ctx, goalID, "worker", "recovery", []string{"implement"}, []string{"implement the recovery path"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	staleReviewer := addStaleRecoverySession(t, s, "task-recovery-reviewer")
	executorID := registerNamedTestAgentSession(t, s, "task-recovery-review-executor", os.Getpid())
	handoffID := "task-recovery-review-old"
	addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, staleReviewer, executorID)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE task_handoffs
		SET review_requested_by = ?, review_requested_at = ?, review_request_report = ?,
		    review_received_by = ?, review_received_at = ?
		WHERE id = ?`, executorID, now, "review this work", staleReviewer, now, handoffID); err != nil {
		t.Fatalf("set review state: %v", err)
	}

	recovered, err := s.RecoverTaskHandoff(ctx, handoffID, tasks[0].ID, holderID, "reviewer session disappeared")
	if err != nil {
		t.Fatalf("RecoverTaskHandoff review: %v", err)
	}
	if recovered.ReviewReceivedAt != nil || recovered.ReviewReceivedBy != 0 || recovered.ReceivedBy != executorID || recovered.ReviewRequestReport != "review this work" {
		t.Fatalf("review recovery changed protected fields: %+v", recovered)
	}

	received, err := s.ReceiveTaskHandoffReview(ctx, handoffID, tasks[0].ID, holderID)
	if err != nil {
		t.Fatalf("ReceiveTaskHandoffReview after recovery: %v", err)
	}
	if received.ReviewReceivedBy != holderID || received.ReviewReceivedAt == nil {
		t.Fatalf("review receive after recovery = %+v", received)
	}
}

func TestRecoverTaskHandoffRejectsLiveOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID, holderID := newTaskRecoveryGoal(t, s, "task-recovery-live")
	tasks, err := s.CreateTasks(ctx, goalID, "worker", "recovery", []string{"implement"}, []string{"implement the recovery path"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	liveID := registerNamedTestAgentSession(t, s, "task-recovery-live-executor", os.Getpid())
	handoffID := "task-recovery-live-old"
	addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, holderID, liveID)

	if _, err := s.RecoverTaskHandoff(ctx, handoffID, tasks[0].ID, holderID, "do not take live work"); !errors.Is(err, ErrSessionRecoveryNotProven) {
		t.Fatalf("RecoverTaskHandoff live owner error = %v, want ErrSessionRecoveryNotProven", err)
	}
	handoff, err := s.GetTaskHandoff(ctx, handoffID)
	if err != nil {
		t.Fatalf("GetTaskHandoff: %v", err)
	}
	if handoff.RecoveredAt != nil {
		t.Fatalf("live-owner recovery changed handoff: %+v", handoff)
	}
}

func TestRecoverTaskCreateHandoffTerminalizesStaleReceiverAndCreatesReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID, holderID := newTaskRecoveryGoal(t, s, "task-create-recovery")
	staleID := addStaleRecoverySession(t, s, "task-create-recovery-receiver")
	handoffID := "task-create-recovery-old"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_create_handoffs (id, goal_id, requested_by, received_by, requested_at, received_at, request_report)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, handoffID, goalID, testSessionID("task-create-recovery-commander"), staleID, now, now, "create implementation tasks"); err != nil {
		t.Fatalf("insert task-create handoff: %v", err)
	}

	recovered, err := s.RecoverTaskCreateHandoff(ctx, handoffID, goalID, holderID, "task-create receiver disappeared")
	if err != nil {
		t.Fatalf("RecoverTaskCreateHandoff: %v", err)
	}
	if recovered.RecoveredAt == nil || recovered.RecoveryReport != "task-create receiver disappeared" || recovered.ReceivedBy != staleID || recovered.ReceivedAt == nil || recovered.RequestedBy != testSessionID("task-create-recovery-commander") {
		t.Fatalf("task-create recovery did not terminalize the old attempt: %+v", recovered)
	}
	attempts, err := s.ListTaskCreateHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTaskCreateHandoffs: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("task-create attempts = %+v, want old history plus replacement", attempts)
	}
	var replacement TaskCreateHandoff
	for _, attempt := range attempts {
		if attempt.ID != handoffID {
			replacement = attempt
		}
	}
	if replacement.ID == "" || replacement.RequestedBy != testSessionID("task-create-recovery-commander") || replacement.RecoveredAt != nil || replacement.CompletedAt != nil {
		t.Fatalf("task-create replacement = %+v", replacement)
	}
	if _, err := s.ReceiveTaskCreateHandoff(ctx, replacement.ID, holderID); err != nil {
		t.Fatalf("ReceiveTaskCreateHandoff replacement: %v", err)
	}
	retry, err := s.RecoverTaskCreateHandoff(ctx, handoffID, goalID, holderID, "different retry reason")
	if err != nil {
		t.Fatalf("RecoverTaskCreateHandoff retry: %v", err)
	}
	if retry.RecoveredAt == nil || retry.RecoveryReport != "task-create receiver disappeared" {
		t.Fatalf("task-create retry rewrote recovery state: %+v", retry)
	}
}

func TestRecoverRequestedTaskCreateHandoffCreatesReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID, holderID := newTaskRecoveryGoal(t, s, "task-create-requested-recovery")
	staleID := addStaleRecoverySession(t, s, "task-create-requested-recovery-requester")
	handoffID := "task-create-requested-recovery-old"
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO task_create_handoffs (id, goal_id, requested_by, requested_at, request_report)
		VALUES (?, ?, ?, ?, ?)`, handoffID, goalID, staleID, time.Now().UTC().Format(time.RFC3339Nano), "create implementation tasks"); err != nil {
		t.Fatalf("insert requested task-create handoff: %v", err)
	}

	recovered, err := s.RecoverTaskCreateHandoff(ctx, handoffID, goalID, holderID, "task-create requester disappeared")
	if err != nil {
		t.Fatalf("RecoverTaskCreateHandoff requested: %v", err)
	}
	if recovered.RecoveredAt == nil || recovered.RequestedBy != staleID || recovered.ReceivedAt != nil {
		t.Fatalf("requested task-create recovery = %+v", recovered)
	}
	attempts, err := s.ListTaskCreateHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTaskCreateHandoffs: %v", err)
	}
	if len(attempts) != 2 || attempts[1].RecoveredAt != nil || attempts[1].RequestedBy != testSessionID("task-create-requested-recovery-commander") {
		t.Fatalf("requested task-create attempts = %+v, want recovered history plus replacement", attempts)
	}
}
