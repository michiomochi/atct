package store

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func TestWithdrawActiveGoalRequiresReason(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	before, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal before withdrawal: %v", err)
	}

	err = s.WithdrawActiveGoal(ctx, goalID, " \t\n")
	if err == nil {
		t.Fatal("WithdrawActiveGoal with blank reason succeeded")
	}

	after, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal after withdrawal: %v", err)
	}
	if after.Status != before.Status || after.ResultSummary != before.ResultSummary || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("goal changed after blank-reason withdrawal: before=%+v after=%+v", before, after)
	}
}

func TestWithdrawActiveGoalRejectsProposedGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "proposed", "/repos/proposed")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "proposed goal", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	before, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal before withdrawal: %v", err)
	}

	err = s.WithdrawActiveGoal(ctx, goal.ID, "no longer needed")
	if !errors.Is(err, ErrGoalNotActive) {
		t.Fatalf("err = %v, want ErrGoalNotActive", err)
	}

	after, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal after withdrawal: %v", err)
	}
	if after.Status != before.Status || after.ResultSummary != before.ResultSummary || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("proposed goal changed: before=%+v after=%+v", before, after)
	}
}

func TestWithdrawActiveGoalKeepsDoneTasks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "withdraw-done", []string{"done", "open"}, []string{"Already done.", "Still open."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, 0); err != nil {
		t.Fatalf("UpdateTask done: %v", err)
	}

	if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	got, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, task := range got {
		if task.ID == tasks[0].ID && task.Status != domain.TaskDone {
			t.Fatalf("done task status = %q, want %q", task.Status, domain.TaskDone)
		}
	}
}

func TestWithdrawActiveGoalDropsOpenTasksAndReleasesClaims(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "withdraw-open", []string{"todo", "doing"}, []string{"Todo work.", "Doing work."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	addTestAgentSession(t, s, "agent-run")
	for _, task := range tasks {
		if _, err := s.ClaimTask(ctx, task.ID, testSessionID("agent-run")); err != nil {
			t.Fatalf("ClaimTask(%d): %v", task.ID, err)
		}
	}
	if _, err := s.UpdateTask(ctx, tasks[1].ID, domain.TaskDoing, 0); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}

	if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	got, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, want := range tasks {
		var found domain.Task
		for _, task := range got {
			if task.ID == want.ID {
				found = task
				break
			}
		}
		if found.ID == 0 {
			t.Fatalf("task %d missing after withdrawal", want.ID)
		}
		if found.Status != domain.TaskDropped {
			t.Errorf("task %d status = %q, want %q", found.ID, found.Status, domain.TaskDropped)
		}
		handoff, err := s.openTaskHandoff(ctx, found.ID)
		if err != nil {
			t.Errorf("task %d openTaskHandoff: %v", found.ID, err)
		} else if handoff != nil {
			t.Errorf("task %d handoff = %#v, want released", found.ID, handoff)
		}
	}
}

func TestWithdrawActiveGoalWithdrawsOpenDecisions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	decision, err := s.AskDecision(ctx, AskInput{
		GoalID:   goalID,
		Kind:     domain.DecisionKind("question"),
		Question: "Should this goal continue?",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	open, err := s.ListOpenDecisions(ctx, goalID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open decisions = %+v, want none", open)
	}
	got, err := s.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionWithdrawn {
		t.Fatalf("decision status = %q, want %q", got.Status, domain.DecisionWithdrawn)
	}
	if got.AnswerText != "stopping this goal" {
		t.Fatalf("decision answer_text = %q, want withdrawal reason", got.AnswerText)
	}
}

func TestWithdrawActiveGoalPublishesGoalWithdrawn(t *testing.T) {
	type withdrawalExpectation struct {
		droppedTaskIDs       []int64
		closedTaskHandoffIDs []string
		withdrawnDecisionIDs []int64
		notDroppedTaskIDs    []int64
	}
	type withdrawCase struct {
		name          string
		openDecisions []string
		setupTasks    func(t *testing.T, s *Store, goalID int64) withdrawalExpectation
	}
	cases := []withdrawCase{
		{
			name:          "no decisions no tasks",
			openDecisions: nil,
		},
		{
			name:          "no decisions with delegated task",
			openDecisions: nil,
			setupTasks: func(t *testing.T, s *Store, goalID int64) withdrawalExpectation {
				t.Helper()
				tasks, err := s.DeclareTasks(context.Background(), goalID, "agent", "withdraw-event", []string{"delegated todo"}, []string{"Still open."})
				if err != nil {
					t.Fatalf("DeclareTasks: %v", err)
				}
				addTestAgentSession(t, s, "withdraw-requester")
				addTestAgentSession(t, s, "withdraw-receiver")
				const handoffID = "withdraw-delegated-todo"
				addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, "withdraw-requester", "withdraw-receiver")
				return withdrawalExpectation{
					droppedTaskIDs:       []int64{tasks[0].ID},
					closedTaskHandoffIDs: []string{handoffID},
				}
			},
		},
		{
			name:          "one decision",
			openDecisions: []string{"Should this goal continue?"},
		},
		{
			name:          "two decisions with delegated task",
			openDecisions: []string{"First question?", "Second question?"},
			setupTasks: func(t *testing.T, s *Store, goalID int64) withdrawalExpectation {
				t.Helper()
				ctx := context.Background()
				tasks, err := s.DeclareTasks(ctx, goalID, "agent", "withdraw-event", []string{"doing task", "already done"}, []string{"Doing work.", "Already done."})
				if err != nil {
					t.Fatalf("DeclareTasks: %v", err)
				}
				if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, 0); err != nil {
					t.Fatalf("UpdateTask doing: %v", err)
				}
				if _, err := s.UpdateTask(ctx, tasks[1].ID, domain.TaskDone, 0); err != nil {
					t.Fatalf("UpdateTask done: %v", err)
				}
				addTestAgentSession(t, s, "withdraw-doing-requester")
				addTestAgentSession(t, s, "withdraw-doing-receiver")
				const handoffID = "withdraw-delegated-doing"
				addTaskHandoffDirect(t, s, handoffID, tasks[0].ID, "withdraw-doing-requester", "withdraw-doing-receiver")
				return withdrawalExpectation{
					droppedTaskIDs:       []int64{tasks[0].ID},
					closedTaskHandoffIDs: []string{handoffID},
					notDroppedTaskIDs:    []int64{tasks[1].ID},
				}
			},
		},
	}

	zeroDecisionCases, nonZeroDecisionCases := 0, 0
	for _, tc := range cases {
		if len(tc.openDecisions) == 0 {
			zeroDecisionCases++
		} else {
			nonZeroDecisionCases++
		}
	}
	if zeroDecisionCases != nonZeroDecisionCases {
		t.Fatalf("decision case balance mismatch: len(openDecisions)==0: %d, len(openDecisions)>0: %d", zeroDecisionCases, nonZeroDecisionCases)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			goalID := newTestGoal(t, s)
			goal, err := s.GetGoal(ctx, goalID)
			if err != nil {
				t.Fatalf("GetGoal: %v", err)
			}
			want := withdrawalExpectation{}
			for _, question := range tc.openDecisions {
				decision, err := s.AskDecision(ctx, AskInput{
					GoalID:   goalID,
					Kind:     domain.DecisionKind("question"),
					Question: question,
				})
				if err != nil {
					t.Fatalf("AskDecision: %v", err)
				}
				want.withdrawnDecisionIDs = append(want.withdrawnDecisionIDs, decision.ID)
			}
			if tc.setupTasks != nil {
				taskWant := tc.setupTasks(t, s, goalID)
				want.droppedTaskIDs = taskWant.droppedTaskIDs
				want.closedTaskHandoffIDs = taskWant.closedTaskHandoffIDs
				want.notDroppedTaskIDs = taskWant.notDroppedTaskIDs
			}

			events, unsubscribe := s.SubscribeEvents()
			defer unsubscribe()
			if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
				t.Fatalf("WithdrawActiveGoal: %v", err)
			}
			withdrawn := waitForGoalWithdrawn(t, events)
			if withdrawn.GoalID != goalID {
				t.Fatalf("GoalID = %d, want %d", withdrawn.GoalID, goalID)
			}
			if withdrawn.ProjectID != goal.ProjectID {
				t.Fatalf("ProjectID = %d, want %d", withdrawn.ProjectID, goal.ProjectID)
			}
			if withdrawn.Reason != "stopping this goal" {
				t.Fatalf("Reason = %q, want %q", withdrawn.Reason, "stopping this goal")
			}
			if !sameInt64IDs(withdrawn.DroppedTaskIDs, want.droppedTaskIDs) {
				t.Fatalf("DroppedTaskIDs = %v, want %v", withdrawn.DroppedTaskIDs, want.droppedTaskIDs)
			}
			if !sameStringIDs(withdrawn.ClosedTaskHandoffIDs, want.closedTaskHandoffIDs) {
				t.Fatalf("ClosedTaskHandoffIDs = %v, want %v", withdrawn.ClosedTaskHandoffIDs, want.closedTaskHandoffIDs)
			}
			if !sameInt64IDs(withdrawn.WithdrawnDecisionIDs, want.withdrawnDecisionIDs) {
				t.Fatalf("WithdrawnDecisionIDs = %v, want %v", withdrawn.WithdrawnDecisionIDs, want.withdrawnDecisionIDs)
			}
			for _, taskID := range want.notDroppedTaskIDs {
				for _, droppedTaskID := range withdrawn.DroppedTaskIDs {
					if droppedTaskID == taskID {
						t.Fatalf("done task %d appeared in DroppedTaskIDs = %v", taskID, withdrawn.DroppedTaskIDs)
					}
				}
			}
		})
	}
}

func waitForGoalWithdrawn(t *testing.T, events <-chan DecisionEvent) GoalWithdrawnEvent {
	t.Helper()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("event subscription closed before goal.withdrawn event")
			}
			if event.Name != EventGoalWithdrawn {
				continue
			}
			withdrawn, ok := event.Data.(GoalWithdrawnEvent)
			if !ok {
				t.Fatalf("event data type = %T, want GoalWithdrawnEvent", event.Data)
			}
			return withdrawn
		case <-timer.C:
			t.Fatal("timed out waiting for goal.withdrawn event")
		}
	}
}

func sameInt64IDs(got, want []int64) bool {
	gotCopy := append([]int64(nil), got...)
	wantCopy := append([]int64(nil), want...)
	sort.Slice(gotCopy, func(i, j int) bool { return gotCopy[i] < gotCopy[j] })
	sort.Slice(wantCopy, func(i, j int) bool { return wantCopy[i] < wantCopy[j] })
	if len(gotCopy) != len(wantCopy) {
		return false
	}
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			return false
		}
	}
	return true
}

func sameStringIDs(got, want []string) bool {
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	if len(gotCopy) != len(wantCopy) {
		return false
	}
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			return false
		}
	}
	return true
}

func TestWithdrawActiveGoalDoesNotPublishHandoffReported(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "withdraw-no-handoff-event", []string{"open task"}, []string{"Still open."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	addTestAgentSession(t, s, "withdraw-no-event-requester")
	addTestAgentSession(t, s, "withdraw-no-event-receiver")
	addTaskHandoffDirect(t, s, "withdraw-no-handoff-reported", tasks[0].ID, "withdraw-no-event-requester", "withdraw-no-event-receiver")

	events, unsubscribe := s.SubscribeEvents()
	defer unsubscribe()
	if err := s.WithdrawActiveGoal(ctx, goalID, "stopping this goal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}
	waitForGoalWithdrawn(t, events)
	expectNoHandoffReported(t, events)
}
