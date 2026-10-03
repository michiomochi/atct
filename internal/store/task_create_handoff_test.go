package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
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
	setPlanReviewGoalArtifacts(t, s, goalID)
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

	handoff, err := s.GetTaskCreateHandoffForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal: %v", err)
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
	tasks, err := s.CreateTasksForHandoff(ctx, handoff.ID, subcommanderID, goalID, "agent", "implementation", []string{"implement one", "implement two"}, []string{"first implementation task", "second implementation task"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("created tasks = %+v, want two new tasks", tasks)
	}
	if tasks[0].Created == nil || !*tasks[0].Created {
		t.Fatalf("first CreateTasks created = %#v, want true", tasks[0].Created)
	}
	if tasks[0].ID == unrelated[0].ID {
		t.Fatalf("CreateTasks returned unrelated task %d", unrelated[0].ID)
	}
	handoff, err = s.GetTaskCreateHandoffForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal after create: %v", err)
	}
	if handoff.CompletedAt == nil || handoff.CompletedBy != subcommanderID {
		t.Fatalf("task create handoff = %+v, want completed by receiver without task handoffs", handoff)
	}
	retry, err := s.CreateTasksForHandoff(ctx, handoff.ID, subcommanderID, goalID, "agent", "implementation", []string{"implement one", "implement two"}, []string{"first implementation task", "second implementation task"})
	if err != nil {
		t.Fatalf("CreateTasks retry: %v", err)
	}
	if len(retry) != 2 || retry[0].ID != tasks[0].ID || retry[1].ID != tasks[1].ID {
		t.Fatalf("CreateTasks retry = %+v, want only request tasks %+v", retry, tasks)
	}
	if retry[0].Created == nil || *retry[0].Created || retry[1].Created == nil || *retry[1].Created {
		t.Fatalf("retry CreateTasks created = %#v, %#v; want false", retry[0].Created, retry[1].Created)
	}
	reducedRetry, err := s.CreateTasksForHandoff(ctx, handoff.ID, subcommanderID, goalID, "agent", "implementation", []string{"implement one"}, []string{"first implementation task"})
	if err != nil {
		t.Fatalf("CreateTasks reduced retry: %v", err)
	}
	if len(reducedRetry) != 2 || reducedRetry[0].ID != tasks[0].ID || reducedRetry[1].ID != tasks[1].ID {
		t.Fatalf("CreateTasks reduced retry = %+v, want original request tasks %+v", reducedRetry, tasks)
	}
	if reducedRetry[0].Created == nil || *reducedRetry[0].Created || reducedRetry[1].Created == nil || *reducedRetry[1].Created {
		t.Fatalf("reduced retry created = %#v, %#v; want false", reducedRetry[0].Created, reducedRetry[1].Created)
	}
	if _, err := s.CreateTasksForHandoff(ctx, handoff.ID, subcommanderID, goalID, "agent", "other", []string{"other"}, []string{"different retry key"}); err == nil {
		t.Fatal("CreateTasks accepted a different key after completion")
	}

	// A revised plan creates a new task-create attempt. The completed first
	// attempt remains history and must not block the current open attempt.
	revisedPlan, err := s.RequestPlanHandoffReview(ctx, "task-create-plan-revised", goalID, subcommanderID, "revised plan")
	if err != nil {
		t.Fatalf("RequestPlanHandoffReview revised: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, revisedPlan.ID, goalID, commanderID); err != nil {
		t.Fatalf("ReceivePlanHandoffReview revised: %v", err)
	}
	if _, err := s.CompletePlanHandoff(ctx, revisedPlan.ID, goalID, commanderID, "revised plan accepted"); err != nil {
		t.Fatalf("CompletePlanHandoff revised: %v", err)
	}
	attempts, err := s.ListTaskCreateHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTaskCreateHandoffs: %v", err)
	}
	if len(attempts) != 2 || attempts[0].CompletedAt == nil || attempts[1].CompletedAt != nil {
		t.Fatalf("task-create attempts = %+v, want completed history plus open replacement", attempts)
	}
	current, err := s.GetTaskCreateHandoffForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal current: %v", err)
	}
	if current.ID != attempts[1].ID {
		t.Fatalf("current task-create attempt = %+v, want open attempt %+v", current, attempts[1])
	}
	if _, err := s.ReceiveTaskCreateHandoff(ctx, attempts[1].ID, subcommanderID); err != nil {
		t.Fatalf("ReceiveTaskCreateHandoff revised: %v", err)
	}
	revisedTasks, err := s.CreateTasksForHandoff(ctx, attempts[1].ID, subcommanderID, goalID, "agent", "revised-implementation", []string{"revised implementation"}, []string{"implement the revised plan"})
	if err != nil {
		t.Fatalf("CreateTasksForHandoff revised: %v", err)
	}
	if len(revisedTasks) != 1 || revisedTasks[0].Created == nil || !*revisedTasks[0].Created {
		t.Fatalf("revised tasks = %+v, want one newly created task", revisedTasks)
	}
}

// rejectSetup returns a store whose goal has an accepted plan and a completed
// task-create handoff, with the goal review requested and received by the
// commander (ready to reject).
type rejectFixture struct {
	s                  *Store
	goalID             int64
	commanderID, subID int64
	goalHandoffID      string
	reviewCount        int
}

func newRejectFixture(t *testing.T) *rejectFixture {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	f := &rejectFixture{s: s, goalID: newTestGoal(t, s), commanderID: testSessionID("reject-commander"), subID: testSessionID("reject-sub")}
	addLiveProjectClaim(t, s, f.goalID, "reject-commander")
	registerNamedTestAgentSession(t, s, "reject-sub", os.Getpid())
	gh, err := s.RequestGoalHandoff(ctx, "reject-goal", f.goalID, f.commanderID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	f.goalHandoffID = gh.ID
	if _, err := s.ReceiveGoalHandoff(ctx, gh.ID, f.goalID, f.subID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	setPlanReviewGoalArtifacts(t, s, f.goalID)
	plan, err := s.RequestPlanHandoffReview(ctx, "reject-plan", f.goalID, f.subID, "plan")
	if err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, plan.ID, f.goalID, f.commanderID); err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if _, err := s.CompletePlanHandoff(ctx, plan.ID, f.goalID, f.commanderID, "accepted"); err != nil {
		t.Fatalf("CompletePlanHandoff: %v", err)
	}
	h, err := s.GetTaskCreateHandoffForGoal(ctx, f.goalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal: %v", err)
	}
	if _, err := s.ReceiveTaskCreateHandoff(ctx, h.ID, f.subID); err != nil {
		t.Fatalf("ReceiveTaskCreateHandoff: %v", err)
	}
	if _, err := s.CreateTasksForHandoff(ctx, h.ID, f.subID, f.goalID, "agent", "first", []string{"one"}, []string{"first task"}); err != nil {
		t.Fatalf("CreateTasksForHandoff: %v", err)
	}
	return f
}

// requestAndReject runs one review request -> receive -> reject cycle.
func (f *rejectFixture) requestAndReject(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	f.reviewCount++
	if _, err := f.s.RequestGoalHandoffReview(ctx, f.goalHandoffID, f.goalID, f.subID, "ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := f.s.ReceiveGoalHandoffReview(ctx, f.goalHandoffID, f.goalID, f.commanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	if _, err := f.s.RejectGoalHandoffReview(ctx, f.goalHandoffID, f.goalID, f.commanderID, "needs more tasks"); err != nil {
		t.Fatalf("RejectGoalHandoffReview: %v", err)
	}
}

func (f *rejectFixture) openHandoffs(t *testing.T) []TaskCreateHandoff {
	t.Helper()
	all, err := f.s.ListTaskCreateHandoffs(context.Background(), f.goalID)
	if err != nil {
		t.Fatalf("ListTaskCreateHandoffs: %v", err)
	}
	var open []TaskCreateHandoff
	for _, h := range all {
		if h.CompletedAt == nil && h.RecoveredAt == nil {
			open = append(open, h)
		}
	}
	return open
}

func TestGoalHandoffRejectionReissuesTaskCreateHandoff(t *testing.T) {
	f := newRejectFixture(t)
	ctx := context.Background()
	if open := f.openHandoffs(t); len(open) != 0 {
		t.Fatalf("open before reject = %+v, want none", open)
	}
	f.requestAndReject(t)
	open := f.openHandoffs(t)
	if len(open) != 1 || open[0].RequestedBy != f.commanderID || open[0].ReceivedBy != 0 {
		t.Fatalf("open after reject = %+v, want one commander-requested unreceived handoff", open)
	}
	if _, err := f.s.ReceiveTaskCreateHandoff(ctx, open[0].ID, f.subID); err != nil {
		t.Fatalf("ReceiveTaskCreateHandoff: %v", err)
	}
	tasks, err := f.s.CreateTasksForHandoff(ctx, open[0].ID, f.subID, f.goalID, "agent", "second", []string{"two"}, []string{"second task"})
	if err != nil || len(tasks) != 1 || tasks[0].Created == nil || !*tasks[0].Created {
		t.Fatalf("CreateTasksForHandoff = %+v, %v; want one new task", tasks, err)
	}
}

func TestGoalHandoffReRequestSupersedesUnreceivedTaskCreateHandoff(t *testing.T) {
	f := newRejectFixture(t)
	ctx := context.Background()
	f.requestAndReject(t)
	first := f.openHandoffs(t)[0]
	if _, err := f.s.ReceiveGoalHandoffReviewRejection(ctx, f.goalHandoffID, f.goalID, f.subID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}
	f.requestAndReject(t) // re-request supersedes first, reject issues a fresh one
	got, err := f.s.getTaskCreateHandoff(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RecoveredAt == nil || !strings.Contains(got.RecoveryReport, "superseded") {
		t.Fatalf("first handoff = %+v, want superseded", got)
	}
	open := f.openHandoffs(t)
	if len(open) != 1 || open[0].ID == first.ID {
		t.Fatalf("open = %+v, want exactly one fresh handoff", open)
	}
}

func TestGoalHandoffReRequestKeepsReceivedTaskCreateHandoff(t *testing.T) {
	f := newRejectFixture(t)
	ctx := context.Background()
	f.requestAndReject(t)
	first := f.openHandoffs(t)[0]
	if _, err := f.s.ReceiveGoalHandoffReviewRejection(ctx, f.goalHandoffID, f.goalID, f.subID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReviewRejection: %v", err)
	}
	if _, err := f.s.ReceiveTaskCreateHandoff(ctx, first.ID, f.subID); err != nil {
		t.Fatalf("ReceiveTaskCreateHandoff: %v", err)
	}
	f.requestAndReject(t) // must not close the received handoff nor add another
	open := f.openHandoffs(t)
	if len(open) != 1 || open[0].ID != first.ID || open[0].ReceivedBy != f.subID || open[0].RecoveredAt != nil {
		t.Fatalf("open = %+v, want the received handoff %s untouched", open, first.ID)
	}
	all, _ := f.s.ListTaskCreateHandoffs(ctx, f.goalID)
	if len(all) != 2 {
		t.Fatalf("handoffs = %d, want 2 (completed + reused)", len(all))
	}
}

func TestMissingTaskCreateHandoffIsNotFound(t *testing.T) {
	f := newRejectFixture(t)
	ctx := context.Background()
	check := func(name string, err error) {
		t.Helper()
		if err == nil || !errors.Is(err, ErrTaskCreateHandoffState) || !errors.Is(err, sql.ErrNoRows) || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("%s error = %v, want ErrTaskCreateHandoffState + sql.ErrNoRows + not found", name, err)
		}
	}
	_, err := f.s.ReceiveTaskCreateHandoff(ctx, "bogus", f.subID)
	check("ReceiveTaskCreateHandoff", err)
	_, err = f.s.CreateTasksForHandoff(ctx, "bogus", f.subID, f.goalID, "agent", "k", []string{"t"}, []string{"d"})
	check("CreateTasksForHandoff", err)
}
