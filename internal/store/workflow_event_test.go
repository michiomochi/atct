package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func TestDecisionTransitionsPublishWithoutDeliveryPersistence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	events, cancel := s.SubscribeEvents()
	defer cancel()

	decision, err := s.AskDecision(ctx, AskInput{
		GoalID:         goalID,
		Kind:           domain.KindDecision,
		Question:       "choose without durable delivery",
		Options:        []domain.Option{{Label: "keep"}, {Label: "change"}},
		AgentSessionID: testSessionID("workflow-event-transition"),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	created := receiveWorkflowEvent(t, events, "decision.created")
	createdDecision, ok := created.Data.(domain.Decision)
	if !ok || createdDecision.ID != decision.ID {
		t.Fatalf("created event data = %#v, want decision %d", created.Data, decision.ID)
	}

	answered, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: decision.ID, AnswerLabel: "keep", AnswerText: "keep it"})
	if err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	answeredEvent := receiveWorkflowEvent(t, events, "decision.answered")
	answeredDecision, ok := answeredEvent.Data.(domain.Decision)
	if !ok || answeredDecision.ID != answered.ID || answeredDecision.Status != domain.DecisionAnswered {
		t.Fatalf("answered event data = %#v, want answered decision %d", answeredEvent.Data, answered.ID)
	}

	for _, table := range []string{"workflow_event_outbox", "project_event_sequences", "watch_delivery_cursors"} {
		assertTableAbsent(t, s.DB(), table)
	}
}

func receiveWorkflowEvent(t *testing.T, events <-chan DecisionEvent, name string) DecisionEvent {
	t.Helper()
	select {
	case event := <-events:
		if event.Name != name {
			t.Fatalf("event name = %q, want %q", event.Name, name)
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", name)
		return DecisionEvent{}
	}
}

func TestScopedWorkflowReconciliationReturnsCanonicalState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	taskID := newTestDecisionTask(t, s, goalID, "workflow-reconcile")
	decision, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision,
		Question: "reconcile this decision", AgentSessionID: testSessionID("workflow-reconcile-agent"),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: goal.ProjectID, GoalID: goalID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	if len(reconciliation.Goals) != 1 || reconciliation.Goals[0].ID != goalID {
		t.Fatalf("reconciled goals = %+v", reconciliation.Goals)
	}
	payload, err := json.Marshal(reconciliation)
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode reconciliation: %v", err)
	}
	for _, field := range []string{"events", "oldest_sequence", "current_sequence", "high_watermark", "cursor"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("reconciliation contains delivery field %q: %s", field, payload)
		}
	}
	var decisions []domain.Decision
	if err := json.Unmarshal(fields["decisions"], &decisions); err != nil {
		t.Fatalf("decode canonical decisions: %v; payload=%s", err, payload)
	}
	if len(decisions) != 1 || decisions[0].ID != decision.ID || decisions[0].Status != domain.DecisionOpen {
		t.Fatalf("reconciled canonical decisions = %+v", decisions)
	}
}

func TestWorkflowReconciliationIncludesTaskCreateHandoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	addLiveProjectClaim(t, s, goalID, "workflow-task-create-commander")
	addTestAgentSession(t, s, "workflow-task-create-subcommander")
	goalHandoff, err := s.RequestGoalHandoff(ctx, "workflow-task-create-goal", goalID, testSessionID("workflow-task-create-commander"), "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goalID, testSessionID("workflow-task-create-subcommander")); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	setPlanReviewGoalArtifacts(t, s, goalID)
	_, err = s.RequestPlanHandoffReview(ctx, "workflow-task-create-plan", goalID, testSessionID("workflow-task-create-subcommander"), "ready")
	if err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO task_create_handoffs (id, goal_id, requested_at) VALUES (?, ?, ?)`, "workflow-task-create", goalID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert task-create handoff: %v", err)
	}

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: goal.ProjectID, GoalID: goalID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	if len(reconciliation.TaskCreateHandoffs) != 1 || reconciliation.TaskCreateHandoffs[0].ID != "workflow-task-create" {
		t.Fatalf("task-create handoffs = %#v, want workflow-task-create", reconciliation.TaskCreateHandoffs)
	}
}

func TestGoalScopedWorkflowReconciliationReturnsCompletedReopenedHandoffAndAppliedDecision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	requesterKey := "workflow-canonical-history-requester"
	receiverKey := "workflow-canonical-history-receiver"
	addLiveProjectClaim(t, s, goalID, requesterKey)
	addTestAgentSession(t, s, receiverKey)
	completedID := "workflow-canonical-history-completed"
	handoff, err := s.RequestGoalHandoff(ctx, completedID, goalID, testSessionID(requesterKey), "historical handoff")
	if err != nil {
		t.Fatalf("RequestGoalHandoff historical: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID(receiverKey)); err != nil {
		t.Fatalf("ReceiveGoalHandoff historical: %v", err)
	}
	if _, err := s.CompleteGoalHandoff(ctx, handoff.ID, goalID, "historical completion"); err != nil {
		t.Fatalf("CompleteGoalHandoff historical: %v", err)
	}
	reopenedID := "workflow-canonical-history-reopened"
	reopened, err := s.RequestGoalHandoff(ctx, reopenedID, goalID, testSessionID(requesterKey), "reopened handoff")
	if err != nil {
		t.Fatalf("RequestGoalHandoff reopened: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, reopened.ID, goalID, testSessionID(receiverKey)); err != nil {
		t.Fatalf("ReceiveGoalHandoff reopened: %v", err)
	}

	decision, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, Kind: domain.KindDecision, Question: "canonical applied decision",
		Options: []domain.Option{
			{Label: "keep", Description: "keep the current state"},
			{Label: "change", Description: "change the current state"},
		},
		AgentSessionID: testSessionID(requesterKey),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: decision.ID, AnswerLabel: "keep", AnswerText: "keep it"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	if applied, err := s.PollDecisions(ctx, testSessionID(requesterKey), decision.ID); err != nil {
		t.Fatalf("PollDecisions: %v", err)
	} else if len(applied) != 1 || applied[0].Status != domain.DecisionApplied {
		t.Fatalf("applied decisions = %+v", applied)
	}

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: goal.ProjectID, GoalID: goalID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	payload, err := json.Marshal(reconciliation)
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode reconciliation: %v", err)
	}
	var decisions []domain.Decision
	if err := json.Unmarshal(fields["decisions"], &decisions); err != nil {
		t.Fatalf("decode canonical decisions: %v; payload=%s", err, payload)
	}
	if len(decisions) != 1 || decisions[0].ID != decision.ID || decisions[0].Status != domain.DecisionApplied {
		t.Fatalf("canonical decision history = %+v", decisions)
	}
	var handoffs []GoalHandoff
	if err := json.Unmarshal(fields["goal_handoffs"], &handoffs); err != nil {
		t.Fatalf("decode canonical goal handoffs: %v; payload=%s", err, payload)
	}
	if len(handoffs) != 2 {
		t.Fatalf("canonical goal handoffs = %+v", handoffs)
	}
	states := make(map[string]bool, len(handoffs))
	for _, handoff := range handoffs {
		states[handoff.ID] = handoff.CompletedReportAt != nil
	}
	if !states[completedID] || states[reopenedID] {
		t.Fatalf("completed/reopened handoff states = %+v", states)
	}
}

func TestTaskScopedWorkflowReconciliationIncludesParentAuthorityAndIsolatesTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "workflow-task-scope")
	declaredTasks, err := s.CreateTasks(ctx, goalID, "test-agent", "workflow-task-scope-other", []string{"other decision task"}, []string{"Create an unrelated task for scope isolation."})
	if err != nil {
		t.Fatalf("CreateTasks other: %v", err)
	}
	var otherTaskID int64
	for _, task := range declaredTasks {
		if task.ID != taskID {
			otherTaskID = task.ID
			break
		}
	}
	if otherTaskID == 0 {
		t.Fatalf("CreateTasks other returned no distinct task: %+v", declaredTasks)
	}
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	requesterKey := "workflow-task-scope-requester"
	requesterID := testSessionID(requesterKey)
	addLiveProjectClaim(t, s, goalID, requesterKey)
	receiverKey := "workflow-task-scope-receiver"
	addTestAgentSession(t, s, receiverKey)
	handoff, err := s.RequestGoalHandoff(ctx, "workflow-task-scope-goal-handoff", goalID, requesterID, "hold the goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, testSessionID(receiverKey)); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	addTestAgentSession(t, s, "workflow-task-scope-target-agent")
	addTestAgentSession(t, s, "workflow-task-scope-other-agent")
	targetDecision, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision,
		Question: "target task decision", Options: []domain.Option{
			{Label: "target-a"}, {Label: "target-b"},
		}, AgentSessionID: testSessionID("workflow-task-scope-target-agent"),
	})
	if err != nil {
		t.Fatalf("AskDecision target: %v", err)
	}
	otherDecision, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: otherTaskID, Kind: domain.KindDecision,
		Question: "other task decision", Options: []domain.Option{
			{Label: "other-a"}, {Label: "other-b"},
		}, AgentSessionID: testSessionID("workflow-task-scope-other-agent"),
	})
	if err != nil {
		t.Fatalf("AskDecision other: %v", err)
	}

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: goal.ProjectID, TaskID: taskID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	if len(reconciliation.Tasks) != 1 || reconciliation.Tasks[0].ID != taskID {
		t.Fatalf("task-scoped tasks = %+v", reconciliation.Tasks)
	}
	if len(reconciliation.GoalHandoffs) != 1 || reconciliation.GoalHandoffs[0].ID != handoff.ID {
		t.Fatalf("task-scoped parent handoffs = %+v", reconciliation.GoalHandoffs)
	}
	payload, err := json.Marshal(reconciliation)
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode reconciliation: %v", err)
	}
	var tasks []domain.Task
	if err := json.Unmarshal(fields["tasks"], &tasks); err != nil {
		t.Fatalf("decode task scope: %v; payload=%s", err, payload)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("task-scoped canonical tasks = %+v", tasks)
	}
	var decisions []domain.Decision
	if err := json.Unmarshal(fields["decisions"], &decisions); err != nil {
		t.Fatalf("decode task decisions: %v; payload=%s", err, payload)
	}
	if len(decisions) != 1 || decisions[0].ID != targetDecision.ID || decisions[0].ID == otherDecision.ID {
		t.Fatalf("task-scoped canonical decisions = %+v", decisions)
	}
}
