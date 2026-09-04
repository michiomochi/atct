package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

func TestWorkflowEventOutboxPersistsTaskReviewWithProjectSequence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	taskID := addTestTasks(t, s, 1)[0]
	goalID, err := sqlcgen.New(s.DB()).GetTaskGoalID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskGoalID: %v", err)
	}
	projectID, err := sqlcgen.New(s.DB()).GetTaskProjectID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskProjectID: %v", err)
	}
	requesterID := testSessionID("workflow-event-task-requester")
	receiverID := testSessionID("workflow-event-task-receiver")
	addLiveParentGoalClaim(t, s, taskID, "workflow-event-task-requester")
	addTestAgentSession(t, s, "workflow-event-task-receiver")

	handoff, err := s.RequestTaskHandoff(ctx, "workflow-event-task-handoff", taskID, requesterID, "take this task")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.RequestTaskHandoffReview(ctx, handoff.ID, taskID, receiverID, "ready for review"); err != nil {
		t.Fatalf("RequestTaskHandoffReview: %v", err)
	}

	page, err := s.ListWorkflowEvents(ctx, WorkflowEventQuery{ProjectID: projectID, GoalID: goalID, TaskID: taskID})
	if err != nil {
		t.Fatalf("ListWorkflowEvents: %v", err)
	}
	var found *WorkflowEvent
	for i := range page.Events {
		if page.Events[i].Name == EventTaskHandoffReviewRequest {
			found = &page.Events[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("task review request is missing from outbox: %+v", page.Events)
	}
	if found.ProjectID != projectID || found.GoalID != goalID || found.TaskID != taskID {
		t.Fatalf("task review scope = %+v, want project=%d goal=%d task=%d", found, projectID, goalID, taskID)
	}
	if found.Sequence <= 0 || found.ID != fmt.Sprintf("%d:%d", projectID, found.Sequence) {
		t.Fatalf("task review sequence/id = %d/%q", found.Sequence, found.ID)
	}
}

func TestWorkflowEventOutboxPersistsGoalReviewWithProjectSequence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	requesterID := testSessionID("workflow-event-goal-review-requester")
	receiverID := testSessionID("workflow-event-goal-review-receiver")
	addLiveProjectClaim(t, s, goalID, "workflow-event-goal-review-requester")
	addTestAgentSession(t, s, "workflow-event-goal-review-receiver")
	if completed := completeGoalHandoffReviewForGoalReviewTest(t, s, ctx, "workflow-event-goal-review-handoff", goalID, requesterID, receiverID); completed.CompletedReportAt == nil {
		t.Fatalf("goal handoff = %+v, want completed handoff before goal review", completed)
	}
	review, err := s.RequestGoalReview(ctx, goalID, requesterID, domain.CompletionReport{
		WorkDone:    "workflow review work",
		NowPossible: "workflow review result",
		HowToVerify: "run workflow event tests",
		Surprises:   "none",
		NeedsReview: "none",
		NextSteps:   "merge",
	})
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}

	page, err := s.ListWorkflowEvents(ctx, WorkflowEventQuery{ProjectID: goal.ProjectID, GoalID: goalID})
	if err != nil {
		t.Fatalf("ListWorkflowEvents after request: %v", err)
	}
	var created *WorkflowEvent
	for i := range page.Events {
		if page.Events[i].Name == "decision.created" && page.Events[i].DecisionID == review.ID {
			created = &page.Events[i]
			break
		}
	}
	if created == nil {
		t.Fatalf("goal review creation is missing from outbox: %+v", page.Events)
	}
	if created.ProjectID != goal.ProjectID || created.GoalID != goalID || created.TaskID != 0 {
		t.Fatalf("goal review scope = %+v, want project=%d goal=%d task=0", created, goal.ProjectID, goalID)
	}
	if created.ID != fmt.Sprintf("%d:%d", goal.ProjectID, created.Sequence) {
		t.Fatalf("goal review creation sequence/id = %d/%q", created.Sequence, created.ID)
	}

	if _, err := s.ApproveGoalReview(ctx, review.ID); err != nil {
		t.Fatalf("ApproveGoalReview: %v", err)
	}
	page, err = s.ListWorkflowEvents(ctx, WorkflowEventQuery{ProjectID: goal.ProjectID, GoalID: goalID, AfterSequence: created.Sequence})
	if err != nil {
		t.Fatalf("ListWorkflowEvents after approval: %v", err)
	}
	var approved *WorkflowEvent
	for i := range page.Events {
		if page.Events[i].Name == "decision.approved" && page.Events[i].DecisionID == review.ID {
			approved = &page.Events[i]
			break
		}
	}
	if approved == nil {
		t.Fatalf("goal review approval is missing from outbox: %+v", page.Events)
	}
	if approved.Sequence <= created.Sequence || approved.ID != fmt.Sprintf("%d:%d", goal.ProjectID, approved.Sequence) {
		t.Fatalf("goal review approval sequence/id = %d/%q, creation=%d", approved.Sequence, approved.ID, created.Sequence)
	}
}

func TestWorkflowEventOutboxPersistsPlanReviewWithProjectSequence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	project, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	commanderID := testSessionID("workflow-event-plan-commander")
	subcommanderID := testSessionID("workflow-event-plan-subcommander")
	addLiveProjectClaim(t, s, goalID, "workflow-event-plan-commander")
	addTestAgentSession(t, s, "workflow-event-plan-subcommander")
	handoff, err := s.RequestGoalHandoff(ctx, "workflow-event-plan-goal-handoff", goalID, commanderID, "take the goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	if _, err := s.RequestPlanHandoffReview(ctx, "workflow-event-plan-review", goalID, subcommanderID, "plan is ready"); err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}

	page, err := s.ListWorkflowEvents(ctx, WorkflowEventQuery{ProjectID: project.ProjectID, GoalID: goalID})
	if err != nil {
		t.Fatalf("ListWorkflowEvents: %v", err)
	}
	var found *WorkflowEvent
	for i := range page.Events {
		if page.Events[i].Name == EventPlanHandoffReviewRequest {
			found = &page.Events[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("plan review request is missing from outbox: %+v", page.Events)
	}
	if found.ProjectID != project.ProjectID || found.GoalID != goalID || found.TaskID != 0 {
		t.Fatalf("plan review scope = %+v, want project=%d goal=%d task=0", found, project.ProjectID, goalID)
	}
}

func TestWatchDeliveryCursorIsMonotonic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	initial, err := s.GetWatchDeliveryCursor(ctx, "watcher-1", goal.ProjectID, goalID)
	if err != nil {
		t.Fatalf("GetWatchDeliveryCursor initial: %v", err)
	}
	if initial.Sequence != 0 {
		t.Fatalf("initial cursor sequence = %d, want 0", initial.Sequence)
	}
	sequence, err := s.CurrentProjectEventSequence(ctx, goal.ProjectID)
	if err != nil {
		t.Fatalf("CurrentProjectEventSequence: %v", err)
	}
	if err := s.AdvanceWatchDeliveryCursor(ctx, "watcher-1", goal.ProjectID, goalID, sequence); err != nil {
		t.Fatalf("AdvanceWatchDeliveryCursor: %v", err)
	}
	if err := s.AdvanceWatchDeliveryCursor(ctx, "watcher-1", goal.ProjectID, goalID, sequence-1); err != nil {
		t.Fatalf("AdvanceWatchDeliveryCursor backwards: %v", err)
	}
	current, err := s.GetWatchDeliveryCursor(ctx, "watcher-1", goal.ProjectID, goalID)
	if err != nil {
		t.Fatalf("GetWatchDeliveryCursor current: %v", err)
	}
	if current.Sequence != sequence {
		t.Fatalf("cursor sequence = %d, want %d", current.Sequence, sequence)
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
	declaredTasks, err := s.DeclareTasks(ctx, goalID, "test-agent", "workflow-task-scope-other", []string{"other decision task"}, []string{"Create an unrelated task for scope isolation."})
	if err != nil {
		t.Fatalf("DeclareTasks other: %v", err)
	}
	var otherTaskID int64
	for _, task := range declaredTasks {
		if task.ID != taskID {
			otherTaskID = task.ID
			break
		}
	}
	if otherTaskID == 0 {
		t.Fatalf("DeclareTasks other returned no distinct task: %+v", declaredTasks)
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
