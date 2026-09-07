package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskReviewWorkPersistsReviewerStateAndOneCompletionRedelegateAction(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "review-work", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "review-work goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "review-work-agent", "review-work-key", []string{"completed task", "remaining task"}, []string{"verify completed", "verify remaining"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "review-work-commander", os.Getpid())
	subcommanderID := registerNamedTestAgentSession(t, s, "review-work-subcommander", os.Getpid())
	executorID := registerNamedTestAgentSession(t, s, "review-work-executor", os.Getpid())
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	goalHandoff, err := s.RequestGoalHandoff(ctx, "review-work-goal-handoff", goal.ID, commanderID, "delegate goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goal.ID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	taskHandoff, err := s.RequestTaskHandoff(ctx, "review-work-task-handoff", tasks[0].ID, subcommanderID, "delegate task")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, taskHandoff.ID, tasks[0].ID, executorID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.RequestTaskHandoffReview(ctx, taskHandoff.ID, tasks[0].ID, executorID, "ready for review"); err != nil {
		t.Fatalf("RequestTaskHandoffReview: %v", err)
	}
	requested, err := s.ListOrchestrationReviewWork(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListOrchestrationReviewWork requested: %v", err)
	}
	if len(requested) != 1 || requested[0].State != OrchestrationReviewWorkStateRequested || !requested[0].Active {
		t.Fatalf("requested review work = %#v, want one active requested record", requested)
	}
	goalScope := GoalOrchestrationScopeKey(goal.ID, goalHandoff.ID)
	if requested[0].ExpectedReviewerRole != "subcommander" || requested[0].ReviewerScopeKey != goalScope {
		t.Fatalf("requested reviewer = %#v, want subcommander at %q", requested[0], goalScope)
	}
	if _, err := s.ReceiveTaskHandoffReview(ctx, taskHandoff.ID, tasks[0].ID, subcommanderID); err != nil {
		t.Fatalf("ReceiveTaskHandoffReview: %v", err)
	}
	received, err := s.ListOrchestrationReviewWork(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListOrchestrationReviewWork received: %v", err)
	}
	if len(received) != 1 || received[0].State != OrchestrationReviewWorkStateReceived || !received[0].Active {
		t.Fatalf("received review work = %#v, want one active received record", received)
	}
	completed, err := s.CompleteTaskHandoffByReviewer(ctx, taskHandoff.ID, tasks[0].ID, subcommanderID, "review accepted")
	if err != nil {
		t.Fatalf("CompleteTaskHandoffByReviewer: %v", err)
	}
	if completed.CompletedReportAt == nil {
		t.Fatal("completed handoff has no completion timestamp")
	}
	pending, err := s.ListPendingOrchestrationReviewWork(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListPendingOrchestrationReviewWork: %v", err)
	}
	if len(pending) != 1 || pending[0].State != OrchestrationReviewWorkStateCompleted || pending[0].Active {
		t.Fatalf("pending review work = %#v, want one settled completion action", pending)
	}
	action := pending[0]
	if action.ActionRole != "subcommander" || action.ActionScopeKey != goalScope || action.ActionTaskID == nil || *action.ActionTaskID != tasks[1].ID {
		t.Fatalf("completion action = %#v, want owning subcommander and next task %d", action, tasks[1].ID)
	}
	if !strings.Contains(action.ActionInstruction, "handoff") || !strings.Contains(action.ActionInstruction, "monitor") {
		t.Fatalf("completion instruction = %q, want next handoff and monitor launch", action.ActionInstruction)
	}
	if strings.Contains(strings.ToLower(action.ActionInstruction), "auto-complete") || strings.Contains(strings.ToLower(action.ActionInstruction), "executor authority") {
		t.Fatalf("completion instruction grants forbidden authority: %q", action.ActionInstruction)
	}
	var nextStatus string
	if err := s.DB().QueryRowContext(ctx, "SELECT status FROM tasks WHERE id = ?", tasks[1].ID).Scan(&nextStatus); err != nil {
		t.Fatalf("read next task status: %v", err)
	}
	if nextStatus != "todo" {
		t.Fatalf("next task status = %q, want todo", nextStatus)
	}
	for attempt := 0; attempt < 2; attempt++ {
		reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: project.ID, GoalID: goal.ID})
		if err != nil {
			t.Fatalf("ReconcileWorkflow attempt %d: %v", attempt+1, err)
		}
		if len(reconciliation.ReviewWork) != 1 {
			t.Fatalf("reconciliation review work attempt %d = %#v, want one durable completion action", attempt+1, reconciliation.ReviewWork)
		}
		if reconciliation.ReviewWork[0].ReviewWorkID != action.ReviewWorkID || reconciliation.ReviewWork[0].SettlementGeneration != action.SettlementGeneration {
			t.Fatalf("reconciliation review work attempt %d = %#v, want stable review-work identity", attempt+1, reconciliation.ReviewWork[0])
		}
	}
}

func TestGoalAndPlanReviewWorkPersistsRecipientRolesAndResolves(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "goal-plan-review-work", filepath.Join(t.TempDir(), "project"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "goal-plan review work goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "goal-plan-review-work-commander", os.Getpid())
	subcommanderID := registerNamedTestAgentSession(t, s, "goal-plan-review-work-subcommander", os.Getpid())
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	goalHandoff, err := s.RequestGoalHandoff(ctx, "goal-plan-review-work-goal-handoff", goal.ID, commanderID, "delegate goal")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goal.ID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	goalScope := GoalOrchestrationScopeKey(goal.ID, goalHandoff.ID)

	if _, err := s.RequestGoalHandoffReview(ctx, goalHandoff.ID, goal.ID, subcommanderID, "goal ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	assertReviewWork := func(kind, handoffID, state string, active bool, expectedRole, reviewerScope, actionRole, actionScope string) OrchestrationReviewWork {
		t.Helper()
		work, err := s.ListOrchestrationReviewWork(ctx, project.ID)
		if err != nil {
			t.Fatalf("ListOrchestrationReviewWork: %v", err)
		}
		for _, item := range work {
			if item.Kind == kind && item.HandoffID == handoffID && item.State == state {
				if item.Active != active || item.ExpectedReviewerRole != expectedRole || item.ReviewerScopeKey != reviewerScope || item.ActionRole != actionRole || item.ActionScopeKey != actionScope {
					t.Fatalf("review work = %#v, want active=%v expected role=%q reviewer scope=%q action role=%q action scope=%q", item, active, expectedRole, reviewerScope, actionRole, actionScope)
				}
				return item
			}
		}
		t.Fatalf("review work %s/%s/%s not found in %#v", kind, handoffID, state, work)
		return OrchestrationReviewWork{}
	}
	assertReviewWork("goal", goalHandoff.ID, OrchestrationReviewWorkStateRequested, true, "commander", ProjectOrchestrationScopeKey(project.ID), "", "")
	if _, err := s.ReceiveGoalHandoffReview(ctx, goalHandoff.ID, goal.ID, commanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	assertReviewWork("goal", goalHandoff.ID, OrchestrationReviewWorkStateReceived, true, "commander", ProjectOrchestrationScopeKey(project.ID), "", "")
	if _, err := s.RejectGoalHandoffReview(ctx, goalHandoff.ID, goal.ID, commanderID, "revise goal"); err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
	assertReviewWork("goal", goalHandoff.ID, OrchestrationReviewWorkStateRejected, false, "commander", ProjectOrchestrationScopeKey(project.ID), "subcommander", goalScope)
	if _, err := s.RequestGoalHandoffReview(ctx, goalHandoff.ID, goal.ID, subcommanderID, "goal revised"); err != nil {
		t.Fatalf("re-request goal review: %v", err)
	}
	if _, err := s.ReceiveGoalHandoffReview(ctx, goalHandoff.ID, goal.ID, commanderID); err != nil {
		t.Fatalf("receive re-requested goal review: %v", err)
	}
	if _, err := s.CompleteGoalHandoffByReviewer(ctx, goalHandoff.ID, goal.ID, commanderID, "goal accepted"); err != nil {
		t.Fatalf("CompleteGoalHandoffByReviewer: %v", err)
	}
	assertReviewWork("goal", goalHandoff.ID, OrchestrationReviewWorkStateCompleted, false, "commander", ProjectOrchestrationScopeKey(project.ID), "", "")

	secondGoalHandoff, err := s.RequestGoalHandoff(ctx, "goal-plan-review-work-second-goal-handoff", goal.ID, commanderID, "delegate goal again")
	if err != nil {
		t.Fatalf("Request second GoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, secondGoalHandoff.ID, goal.ID, subcommanderID); err != nil {
		t.Fatalf("Receive second GoalHandoff: %v", err)
	}
	goalScope = GoalOrchestrationScopeKey(goal.ID, secondGoalHandoff.ID)

	const planHandoffID = "goal-plan-review-work-plan-handoff"
	if _, err := s.RequestPlanHandoffReview(ctx, planHandoffID, goal.ID, subcommanderID, "plan ready"); err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	assertReviewWork("plan", planHandoffID, OrchestrationReviewWorkStateRequested, true, "commander", ProjectOrchestrationScopeKey(project.ID), "", "")
	if _, err := s.ReceivePlanHandoffReview(ctx, planHandoffID, goal.ID, commanderID); err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if _, err := s.RejectPlanHandoffReview(ctx, planHandoffID, goal.ID, commanderID, "revise plan"); err != nil {
		t.Fatalf("RejectPlanHandoffReview: %v", err)
	}
	assertReviewWork("plan", planHandoffID, OrchestrationReviewWorkStateRejected, false, "commander", ProjectOrchestrationScopeKey(project.ID), "subcommander", goalScope)
	if _, err := s.RequestPlanHandoffReview(ctx, planHandoffID, goal.ID, subcommanderID, "plan revised"); err != nil {
		t.Fatalf("re-request plan review: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, planHandoffID, goal.ID, commanderID); err != nil {
		t.Fatalf("receive re-requested plan review: %v", err)
	}
	if _, err := s.CompletePlanHandoff(ctx, planHandoffID, goal.ID, commanderID, "plan accepted"); err != nil {
		t.Fatalf("CompletePlanHandoff: %v", err)
	}
	planCompletion := assertReviewWork("plan", planHandoffID, OrchestrationReviewWorkStateCompleted, false, "commander", ProjectOrchestrationScopeKey(project.ID), "subcommander", goalScope)
	if planCompletion.SettlementGeneration == "" || planCompletion.ActionTaskID != nil ||
		!strings.Contains(planCompletion.ActionInstruction, "resume the approved plan") ||
		!strings.Contains(planCompletion.ActionInstruction, "do not auto-complete") {
		t.Fatalf("plan completion action = %#v, want generation-bound plan resumption without task completion", planCompletion)
	}
	pending, err := s.ListPendingOrchestrationReviewWork(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListPendingOrchestrationReviewWork: %v", err)
	}
	foundPlanAction := false
	for _, item := range pending {
		if item.Kind == "plan" && item.HandoffID == planHandoffID && item.State == OrchestrationReviewWorkStateCompleted {
			foundPlanAction = item.ActionRole == "subcommander" && item.ActionScopeKey == goalScope && item.ActionInstruction == planCompletion.ActionInstruction
			break
		}
	}
	if !foundPlanAction {
		t.Fatalf("pending review work = %#v, want completed plan resumption action", pending)
	}
}
