package store

import (
	"context"
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

func TestScopedWorkflowReconciliationReturnsCurrentStateAndEvents(t *testing.T) {
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
	if len(reconciliation.OpenDecisions) != 1 || reconciliation.OpenDecisions[0].ID != decision.ID {
		t.Fatalf("reconciled open decisions = %+v", reconciliation.OpenDecisions)
	}
	if reconciliation.CurrentSequence <= 0 || len(reconciliation.Events) == 0 {
		t.Fatalf("reconciled event range = current %d events %+v", reconciliation.CurrentSequence, reconciliation.Events)
	}
}

func TestTaskScopedWorkflowReconciliationExcludesParentHandoffs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "workflow-task-scope")
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

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: goal.ProjectID, TaskID: taskID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	if len(reconciliation.Tasks) != 1 || reconciliation.Tasks[0].ID != taskID {
		t.Fatalf("task-scoped tasks = %+v", reconciliation.Tasks)
	}
	if len(reconciliation.GoalHandoffs) != 0 || len(reconciliation.PlanHandoffs) != 0 {
		t.Fatalf("task-scoped parent handoffs = goal=%+v plan=%+v", reconciliation.GoalHandoffs, reconciliation.PlanHandoffs)
	}
}
