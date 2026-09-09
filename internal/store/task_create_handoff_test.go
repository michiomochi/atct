package store

import (
	"context"
	"os"
	"testing"
)

func TestTaskCreateHandoffFollowsAcceptedPlan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("task-create-commander")
	subcommanderID := testSessionID("task-create-subcommander")
	wrongID := testSessionID("task-create-wrong")
	addLiveProjectClaim(t, s, goalID, "task-create-commander")
	registerNamedTestAgentSession(t, s, "task-create-subcommander", os.Getpid())
	addTestAgentSession(t, s, "task-create-wrong")

	goalHandoff, err := s.RequestGoalHandoff(ctx, "task-create-goal", goalID, commanderID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goalID, subcommanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	plan, err := s.RequestPlanHandoffReview(ctx, "task-create-plan", goalID, subcommanderID, "plan")
	if err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, plan.ID, goalID, commanderID); err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if _, err := s.CompletePlanHandoff(ctx, plan.ID, goalID, commanderID, "accepted"); err != nil {
		t.Fatalf("CompletePlanHandoff: %v", err)
	}

	handoff, err := s.GetTaskCreateHandoffForPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForPlan: %v", err)
	}
	if handoff.GoalID != goalID || handoff.ReceivedBy != 0 || handoff.RequestedBy != commanderID {
		t.Fatalf("task create handoff = %+v, want commander request for goal", handoff)
	}
	if _, err := s.ReceiveTaskCreateHandoff(ctx, handoff.ID, wrongID); err == nil {
		t.Fatal("ReceiveTaskCreateHandoff accepted a foreign session")
	}
	if _, err := s.ReceiveTaskCreateHandoff(ctx, handoff.ID, subcommanderID); err != nil {
		t.Fatalf("ReceiveTaskCreateHandoff: %v", err)
	}
	unrelated, err := s.CreateTasks(ctx, goalID, "other-agent", "unrelated", []string{"unrelated"}, []string{"outside this task-create request"})
	if err != nil {
		t.Fatalf("CreateTasks unrelated: %v", err)
	}
	if _, err := s.CreateTasksForHandoff(ctx, handoff.ID, wrongID, goalID, "agent", "implementation", []string{"implement"}, []string{"implement through the received task-create handoff"}); err == nil {
		t.Fatal("CreateTasks accepted a foreign session")
	}
	tasks, err := s.CreateTasksForHandoff(ctx, handoff.ID, subcommanderID, goalID, "agent", "implementation", []string{"implement"}, []string{"implement through the received task-create handoff"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("created tasks = %+v, want one new task", tasks)
	}
	if tasks[0].Created == nil || !*tasks[0].Created {
		t.Fatalf("first CreateTasks created = %#v, want true", tasks[0].Created)
	}
	if tasks[0].ID == unrelated[0].ID {
		t.Fatalf("CreateTasks returned unrelated task %d", unrelated[0].ID)
	}
	retry, err := s.CreateTasksForHandoff(ctx, handoff.ID, subcommanderID, goalID, "agent", "implementation", []string{"implement"}, []string{"implement through the received task-create handoff"})
	if err != nil {
		t.Fatalf("CreateTasks retry: %v", err)
	}
	if len(retry) != 1 || retry[0].ID != tasks[0].ID {
		t.Fatalf("CreateTasks retry = %+v, want only request task %+v", retry, tasks[0])
	}
	if retry[0].Created == nil || *retry[0].Created {
		t.Fatalf("retry CreateTasks created = %#v, want false", retry[0].Created)
	}
	if _, err := s.CompleteTaskCreateHandoff(ctx, handoff.ID, subcommanderID, "done"); err == nil {
		t.Fatal("CompleteTaskCreateHandoff accepted an undelegated task")
	}
	if _, err := s.RequestTaskHandoff(ctx, "task-create-implementation", tasks[0].ID, subcommanderID, "delegate"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.CompleteTaskCreateHandoff(ctx, handoff.ID, subcommanderID, "done"); err != nil {
		t.Fatalf("CompleteTaskCreateHandoff: %v", err)
	}
}
