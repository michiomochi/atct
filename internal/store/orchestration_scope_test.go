package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/domain"
)

func reconciliationRecoveries(t *testing.T, reconciliation WorkflowReconciliation) []map[string]any {
	t.Helper()
	payload, err := json.Marshal(reconciliation)
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode reconciliation: %v", err)
	}
	raw, ok := fields["recoveries"]
	if !ok {
		return nil
	}
	var recoveries []map[string]any
	if err := json.Unmarshal(raw, &recoveries); err != nil {
		t.Fatalf("decode recoveries: %v; payload=%s", err, payload)
	}
	return recoveries
}

func TestWorkflowReconciliationReportsCommanderRecoveryForMissingMonitor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "missing-monitor-recovery", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "missing monitor recovery goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "missing-monitor-commander", os.Getpid())
	receiverID := registerNamedTestAgentSession(t, s, "missing-monitor-receiver", os.Getpid())
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	handoff, err := s.RequestGoalHandoff(ctx, "missing-monitor-handoff", goal.ID, commanderID, "delegate missing monitor recovery")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goal.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: project.ID, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	recoveries := reconciliationRecoveries(t, reconciliation)
	if len(recoveries) != 1 {
		t.Fatalf("recoveries = %#v, want one monitor_missing envelope", recoveries)
	}
	recovery := recoveries[0]
	if recovery["condition"] != "monitor_missing" || recovery["target_role"] != "commander" {
		t.Fatalf("recovery condition/target = %#v, want monitor_missing/commander", recovery)
	}
	if recovery["scope_key"] != GoalOrchestrationScopeKey(goal.ID, handoff.ID) {
		t.Fatalf("recovery scope_key = %#v, want goal scope", recovery["scope_key"])
	}
	if recovery["generation"] == "" || recovery["expected_role"] != "subcommander" {
		t.Fatalf("recovery identity = %#v, want scope generation and expected subcommander", recovery)
	}
	instruction, ok := recovery["instruction"].(string)
	if !ok || instruction == "" || !strings.Contains(instruction, "atct codex monitor --role subcommander --goal ") {
		t.Fatalf("recovery instruction = %#v, want supported monitored subcommander restart", recovery["instruction"])
	}
}

func TestWorkflowReconciliationRoutesRoleMismatchAndClearsWhenReceiverIsLive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "mismatched-monitor-recovery", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "mismatched monitor recovery goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "mismatched-monitor-commander", os.Getpid())
	receiverID := registerNamedTestAgentSession(t, s, "mismatched-monitor-receiver", os.Getpid())
	foreignID := registerNamedTestAgentSession(t, s, "mismatched-monitor-foreign", os.Getpid())
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	handoff, err := s.RequestGoalHandoff(ctx, "mismatched-monitor-handoff", goal.ID, commanderID, "delegate mismatched monitor recovery")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goal.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	scopeKey := GoalOrchestrationScopeKey(goal.ID, handoff.ID)
	now := time.Now().UTC()
	_, err = s.DB().ExecContext(ctx, `
		INSERT INTO monitor_health (
			monitor_id, agent_key, scope_key, agent_session_id, cwd, role,
			project_id, goal_id, task_id, pid, process_started_at, state,
			reason, transitioned_at, last_seen_at, stopped_at
		) VALUES (?, ?, ?, ?, ?, 'executor', ?, ?, NULL, ?, ?, 'healthy', '', ?, ?, NULL)`,
		"role-mismatched-health", "foreign-executor", scopeKey, foreignID, project.RootPath,
		project.ID, goal.ID, os.Getpid(), now.Add(-time.Minute).Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("insert role-mismatched health: %v", err)
	}

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: project.ID, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow with mismatch: %v", err)
	}
	recoveries := reconciliationRecoveries(t, reconciliation)
	if len(recoveries) != 1 {
		t.Fatalf("mismatch recoveries = %#v, want one recipient_mismatch envelope", recoveries)
	}
	recovery := recoveries[0]
	if recovery["condition"] != "recipient_mismatch" || recovery["target_role"] != "commander" {
		t.Fatalf("mismatch condition/target = %#v, want recipient_mismatch/commander", recovery)
	}
	if recovery["expected_role"] != "subcommander" || recovery["observed_role"] != "executor" {
		t.Fatalf("mismatch roles = %#v, want subcommander/executor", recovery)
	}
	if recovery["observed_agent_key"] != "foreign-executor" || recovery["observed_agent_session_id"] != float64(foreignID) {
		t.Fatalf("mismatch observed identity = %#v, want foreign executor session", recovery)
	}
	instruction, ok := recovery["instruction"].(string)
	if !ok || !strings.Contains(instruction, "reissue or transfer") || !strings.Contains(instruction, handoff.ID) {
		t.Fatalf("mismatch instruction = %#v, want handoff reissue/transfer", recovery["instruction"])
	}

	goalID := goal.ID
	validHealth := MonitorHealth{
		CWD:              project.RootPath,
		Role:             "subcommander",
		ScopeKey:         scopeKey,
		AgentSessionID:   receiverID,
		AgentKey:         "mismatched-monitor-receiver",
		ProjectID:        project.ID,
		GoalID:           &goalID,
		PID:              os.Getpid(),
		ProcessStartedAt: now.Add(-time.Minute),
		State:            "healthy",
		LastSeenAt:       now,
		TransitionedAt:   now,
	}
	validHealth.MonitorID = MonitorHealthID(validHealth.CWD, validHealth.Role, validHealth.ProjectID, validHealth.GoalID, validHealth.TaskID, validHealth.PID, validHealth.ProcessStartedAt, validHealth.ScopeKey)
	if err := s.UpsertMonitorHealth(ctx, validHealth); err != nil {
		t.Fatalf("UpsertMonitorHealth valid receiver: %v", err)
	}
	cleared, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: project.ID, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow with live receiver: %v", err)
	}
	if recoveries := reconciliationRecoveries(t, cleared); len(recoveries) != 0 {
		t.Fatalf("live receiver recoveries = %#v, want none", recoveries)
	}
}

func TestLifecycleOwnsExpectedMonitorScopes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	project, err := s.CreateProject(ctx, "scope-project", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "scope goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "scope-agent", "scope-task", []string{"scope task"}, []string{"verify scope ownership"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	commanderID := registerNamedTestAgentSession(t, s, "scope-commander", os.Getpid())
	subcommanderID := registerNamedTestAgentSession(t, s, "scope-subcommander", os.Getpid())
	executorID := registerNamedTestAgentSession(t, s, "scope-executor", os.Getpid())
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	assertExpectedScope := func(key, role string, sessionID int64, wantActive bool) {
		t.Helper()
		var gotRole, generation string
		var gotSessionID, active int64
		err := s.DB().QueryRowContext(ctx, `
			SELECT role, agent_session_id, source_generation, active
			FROM orchestration_scope
			WHERE scope_key = ?`, key).Scan(&gotRole, &gotSessionID, &generation, &active)
		if err != nil {
			t.Fatalf("read expected scope %q: %v", key, err)
		}
		if gotRole != role || gotSessionID != sessionID || generation == "" || (active == 1) != wantActive {
			t.Fatalf("scope %q = role %q, session %d, generation %q, active %d; want role %q, session %d, active %v", key, gotRole, gotSessionID, generation, active, role, sessionID, wantActive)
		}
	}

	projectKey := "project:" + formatScopeID(project.ID) + ":commander"
	assertExpectedScope(projectKey, "commander", commanderID, true)

	goalHandoff, err := s.RequestGoalHandoff(ctx, "scope-goal-handoff", goal.ID, commanderID, "delegate scope goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goal.ID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	goalKey := "goal:" + formatScopeID(goal.ID) + ":subcommander:" + goalHandoff.ID
	assertExpectedScope(goalKey, "subcommander", subcommanderID, true)

	taskHandoff, err := s.RequestTaskHandoff(ctx, "scope-task-handoff", tasks[0].ID, subcommanderID, "delegate scope task")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, taskHandoff.ID, tasks[0].ID, executorID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	taskKey := "task:" + formatScopeID(tasks[0].ID) + ":executor:" + taskHandoff.ID
	assertExpectedScope(taskKey, "executor", executorID, true)

	if _, err := s.CompleteTaskHandoff(ctx, taskHandoff.ID, tasks[0].ID, "task scope complete"); err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}
	assertExpectedScope(taskKey, "executor", executorID, false)

	if _, err := s.CompleteGoalHandoff(ctx, goalHandoff.ID, goal.ID, "goal scope complete"); err != nil {
		t.Fatalf("CompleteGoalHandoff: %v", err)
	}
	assertExpectedScope(goalKey, "subcommander", subcommanderID, false)

	if err := s.ReleaseProject(ctx, project.ID); err != nil {
		t.Fatalf("ReleaseProject: %v", err)
	}
	assertExpectedScope(projectKey, "commander", commanderID, false)
}

func TestProcessOnlyCodexAndForeignOrStaleHealthCannotMatchExpectedScope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	project, err := s.CreateProject(ctx, "health-scope-project", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "health scope goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "health-scope-commander", os.Getpid())
	receiverID := registerNamedTestAgentSession(t, s, "health-scope-receiver", os.Getpid())
	foreignID := registerNamedTestAgentSession(t, s, "health-scope-foreign", os.Getpid())
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	handoff, err := s.RequestGoalHandoff(ctx, "health-scope-handoff", goal.ID, commanderID, "delegate health scope")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goal.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	registryDir := t.TempDir()
	if err := daemonctl.WriteCodexMonitorRecord(registryDir, daemonctl.CodexMonitorRecord{
		SupervisorPID: os.Getpid(),
		AppServerPID:  0,
		SocketPath:    filepath.Join(daemonctl.CodexMonitorRegistryDir(registryDir), "codex.sock"),
		ProjectPath:   project.RootPath,
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteCodexMonitorRecord: %v", err)
	}

	scopeKey := "goal:" + formatScopeID(goal.ID) + ":subcommander:" + handoff.ID
	now := time.Now().UTC()
	for _, health := range []struct {
		monitorID      string
		agentSessionID int64
		scopeKey       string
		lastSeenAt     time.Time
	}{
		{monitorID: "foreign-health", agentSessionID: foreignID, scopeKey: scopeKey, lastSeenAt: now},
		{monitorID: "stale-health", agentSessionID: receiverID, scopeKey: scopeKey, lastSeenAt: now.Add(-monitorHealthLease - time.Second)},
	} {
		_, err := s.DB().ExecContext(ctx, `
			INSERT INTO monitor_health (
				monitor_id, agent_key, scope_key, agent_session_id, cwd, role,
				project_id, goal_id, task_id, pid, process_started_at, state,
				reason, transitioned_at, last_seen_at, stopped_at
			) VALUES (?, '', ?, ?, ?, 'subcommander', ?, ?, NULL, ?, ?, 'healthy', '', ?, ?, NULL)`,
			health.monitorID, health.scopeKey, health.agentSessionID, project.RootPath,
			project.ID, goal.ID, os.Getpid(), now.Add(-time.Minute).Format(time.RFC3339Nano),
			now.Format(time.RFC3339Nano), health.lastSeenAt.Format(time.RFC3339Nano))
		if err != nil {
			t.Fatalf("insert %s: %v", health.monitorID, err)
		}
	}

	var matching int
	err = s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM monitor_health AS health
		JOIN orchestration_scope AS scope
		  ON scope.scope_key = health.scope_key
		 AND scope.role = health.role
		 AND scope.agent_session_id = health.agent_session_id
		WHERE scope.scope_key = ?
		  AND scope.active = 1
		  AND health.stopped_at IS NULL
		  AND health.last_seen_at >= ?`, scopeKey, now.Add(-monitorHealthLease).Format(time.RFC3339Nano)).Scan(&matching)
	if err != nil {
		t.Fatalf("count matching monitor health: %v", err)
	}
	if matching != 0 {
		t.Fatalf("matching health rows = %d, want none for process-only, foreign, and stale health", matching)
	}
	scopes, err := s.ListActiveOrchestrationScopes(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListActiveOrchestrationScopes before valid health: %v", err)
	}
	missingHealth, err := s.ListMatchingMonitorHealth(ctx, scopes)
	if err != nil {
		t.Fatalf("ListMatchingMonitorHealth without valid health: %v", err)
	}
	if len(missingHealth) != 0 {
		t.Fatalf("matching health without rightful live report = %#v, want none", missingHealth)
	}
	missingReconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: project.ID, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow without rightful live report: %v", err)
	}
	if len(missingReconciliation.ExpectedScopes) != 1 || missingReconciliation.ExpectedScopes[0].ScopeKey != scopeKey {
		t.Fatalf("missing-health reconciliation expected scopes = %#v, want active scope %q", missingReconciliation.ExpectedScopes, scopeKey)
	}
	if len(missingReconciliation.MonitorHealth) != 0 {
		t.Fatalf("missing-health reconciliation monitor health = %#v, want none at the owned boundary", missingReconciliation.MonitorHealth)
	}

	validGoalID := goal.ID
	validHealth := MonitorHealth{
		CWD:              project.RootPath,
		Role:             "subcommander",
		ScopeKey:         scopeKey,
		AgentSessionID:   receiverID,
		ProjectID:        project.ID,
		GoalID:           &validGoalID,
		PID:              os.Getpid(),
		ProcessStartedAt: now.Add(-time.Minute),
		State:            "healthy",
		LastSeenAt:       now,
		TransitionedAt:   now,
	}
	validHealth.MonitorID = MonitorHealthID(validHealth.CWD, validHealth.Role, validHealth.ProjectID, validHealth.GoalID, validHealth.TaskID, validHealth.PID, validHealth.ProcessStartedAt, validHealth.ScopeKey)
	if err := s.UpsertMonitorHealth(ctx, validHealth); err != nil {
		t.Fatalf("UpsertMonitorHealth valid row: %v", err)
	}
	matchedHealth, err := s.ListMatchingMonitorHealth(ctx, scopes)
	if err != nil {
		t.Fatalf("ListMatchingMonitorHealth: %v", err)
	}
	if len(matchedHealth) != 1 || matchedHealth[0].MonitorID != validHealth.MonitorID {
		t.Fatalf("matched monitor health = %#v, want only %q", matchedHealth, validHealth.MonitorID)
	}
	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: project.ID, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	if len(reconciliation.ExpectedScopes) != 1 || reconciliation.ExpectedScopes[0].ScopeKey != scopeKey {
		t.Fatalf("reconciliation expected scopes = %#v, want %q", reconciliation.ExpectedScopes, scopeKey)
	}
	if len(reconciliation.MonitorHealth) != 1 || reconciliation.MonitorHealth[0].MonitorID != validHealth.MonitorID {
		t.Fatalf("reconciliation monitor health = %#v, want only %q", reconciliation.MonitorHealth, validHealth.MonitorID)
	}
}

func TestExpectedMonitorScopeTracksReviewRejectionAndCompletionReopen(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "scope-rejection-project", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "scope rejection goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "scope-rejection-commander", os.Getpid())
	receiverID := registerNamedTestAgentSession(t, s, "scope-rejection-receiver", os.Getpid())
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	handoff, err := s.RequestGoalHandoff(ctx, "scope-rejection-handoff", goal.ID, commanderID, "delegate scope rejection goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goal.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	scopeKey := GoalOrchestrationScopeKey(goal.ID, handoff.ID)
	assertActiveGoalScope := func(wantKey string) {
		t.Helper()
		scopes, err := s.ListActiveOrchestrationScopes(ctx, project.ID)
		if err != nil {
			t.Fatalf("ListActiveOrchestrationScopes: %v", err)
		}
		var goalScopes []OrchestrationScope
		for _, scope := range scopes {
			if scope.Role == "subcommander" && scope.GoalID != nil && *scope.GoalID == goal.ID {
				goalScopes = append(goalScopes, scope)
			}
		}
		if len(goalScopes) != 1 || goalScopes[0].ScopeKey != wantKey || goalScopes[0].AgentSessionID != receiverID {
			t.Fatalf("active goal scopes = %#v, want one scope %q for receiver %d", goalScopes, wantKey, receiverID)
		}
	}
	assertActiveGoalScope(scopeKey)

	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goal.ID, receiverID, "ready for review"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goal.ID, commanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	if _, err := s.RejectGoalHandoffReview(ctx, handoff.ID, goal.ID, commanderID, "add scope coverage"); err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
	assertActiveGoalScope(scopeKey)
	if _, err := s.ReceiveGoalHandoffReviewRejection(ctx, handoff.ID, goal.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}

	if _, err := s.RequestGoalHandoffReview(ctx, handoff.ID, goal.ID, receiverID, "scope coverage added"); err != nil {
		t.Fatalf("second RequestGoalHandoffReview: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, handoff.ID, goal.ID, commanderID); err != nil {
		t.Fatalf("second ReceiveGoalHandoffReview: %v", err)
	}
	if _, err := s.CompleteGoalHandoffByReviewer(ctx, handoff.ID, goal.ID, commanderID, "scope handoff complete"); err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}
	activeAfterCompletion, err := s.ListActiveOrchestrationScopes(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListActiveOrchestrationScopes after completion: %v", err)
	}
	for _, scope := range activeAfterCompletion {
		if scope.ScopeKey == scopeKey {
			t.Fatalf("completed goal scope remained active: %#v", scope)
		}
	}

	decision, err := s.CompleteGoalWithReport(ctx, goal.ID, domain.CompletionReport{
		WorkDone:    "scope rejection work",
		NowPossible: "scope identity can be checked",
		HowToVerify: "run focused scope tests",
		Surprises:   "none",
		NeedsReview: "none",
		NextSteps:   "continue",
	}, receiverID)
	if err != nil {
		t.Fatalf("CompleteGoalWithReport: %v", err)
	}
	if err := s.RejectCompletion(ctx, decision.ID, "add one more scope check"); err != nil {
		t.Fatalf("RejectCompletion: %v", err)
	}
	reopenedID := handoff.ID + "-reopen-" + strconv.FormatInt(decision.ID, 10)
	assertActiveGoalScope(GoalOrchestrationScopeKey(goal.ID, reopenedID))

	allScopes, err := s.ListOrchestrationScopes(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListOrchestrationScopes: %v", err)
	}
	for _, scope := range allScopes {
		if scope.ScopeKey == scopeKey && scope.Active {
			t.Fatalf("original rejected/completed scope is active again: %#v", scope)
		}
	}
}

func formatScopeID(id int64) string {
	return strconv.FormatInt(id, 10)
}
