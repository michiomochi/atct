package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

// TestInboxBatchReadsMatchPerGoalReads checks the one-query inbox reads against
// the per-goal reads they replace, over goals in every status and tasks with
// open and completed handoffs.
func TestInboxBatchReadsMatchPerGoalReads(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ns, err := s.CreateProject(ctx, "inbox-batch", "/repos/inbox-batch")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	active, err := s.CreateGoal(ctx, ns.ID, "Active goal\n\nlong body that is not part of the headline", "human")
	if err != nil {
		t.Fatalf("CreateGoal active: %v", err)
	}
	if _, err := s.UpdateGoalRequestReport(ctx, active.ID, "spec text", "plan text"); err != nil {
		t.Fatalf("UpdateGoalRequestReport: %v", err)
	}
	done, err := s.CreateGoal(ctx, ns.ID, "Done goal\nsecond line", "human")
	if err != nil {
		t.Fatalf("CreateGoal done: %v", err)
	}
	dropped, err := s.CreateGoal(ctx, ns.ID, "Dropped goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal dropped: %v", err)
	}
	if _, err := s.CreateGoal(ctx, ns.ID, "Goal without tasks", "human"); err != nil {
		t.Fatalf("CreateGoal empty: %v", err)
	}

	_, err = s.CreateTasks(ctx, active.ID, "worker", "batch-a", []string{"one", "two", "three"}, []string{"desc one", "desc two", "desc three"})
	if err != nil {
		t.Fatalf("CreateTasks active: %v", err)
	}
	if _, err := s.CreateTasks(ctx, done.ID, "worker", "batch-d", []string{"four"}, []string{"desc four"}); err != nil {
		t.Fatalf("CreateTasks done: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE goals SET status = 'done', work_done = 'w', now_possible = 'n', how_to_verify = 'v', surprises = 's', needs_review = 'r' WHERE id = ?`, done.ID); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE goals SET status = 'dropped' WHERE id = ?`, dropped.ID); err != nil {
		t.Fatalf("mark dropped: %v", err)
	}
	holderGoal, holderID := newTaskRecoveryGoal(t, s, "inbox-batch-holder")
	tasksH, err := s.CreateTasks(ctx, holderGoal, "worker", "batch-h", []string{"open", "completed"}, []string{"desc open", "desc completed"})
	if err != nil {
		t.Fatalf("CreateTasks holder: %v", err)
	}
	if _, err := s.RequestTaskHandoff(ctx, "batch-open", tasksH[0].ID, holderID, "open"); err != nil {
		t.Fatalf("RequestTaskHandoff open: %v", err)
	}
	if _, err := s.RequestTaskHandoff(ctx, "batch-done", tasksH[1].ID, holderID, "completed"); err != nil {
		t.Fatalf("RequestTaskHandoff done: %v", err)
	}
	workerID, err := s.RegisterAgentSession(ctx, 4242)
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, "batch-done", tasksH[1].ID, workerID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	if _, err := s.CompleteTaskHandoff(ctx, "batch-done", tasksH[1].ID, "finished"); err != nil {
		t.Fatalf("CompleteTaskHandoff: %v", err)
	}

	goals, err := s.ListAllGoals(ctx)
	if err != nil {
		t.Fatalf("ListAllGoals: %v", err)
	}
	lightGoals, err := s.ListAllGoalsForInbox(ctx)
	if err != nil {
		t.Fatalf("ListAllGoalsForInbox: %v", err)
	}
	if len(lightGoals) != len(goals) {
		t.Fatalf("ListAllGoalsForInbox returned %d goals, want %d", len(lightGoals), len(goals))
	}
	for i, full := range goals {
		light := lightGoals[i]
		if full.Status == domain.GoalActive {
			if !reflect.DeepEqual(full, light) {
				t.Fatalf("active goal %d differs:\nfull:  %+v\nlight: %+v", full.ID, full, light)
			}
			continue
		}
		if light.ID != full.ID || light.ProjectID != full.ProjectID || light.Status != full.Status || light.CreatedAt != full.CreatedAt || light.UpdatedAt != full.UpdatedAt {
			t.Fatalf("goal %d identity differs:\nfull:  %+v\nlight: %+v", full.ID, full, light)
		}
		if light.Content != domain.Headline(full.Content) {
			t.Fatalf("goal %d headline = %q, want %q", full.ID, light.Content, domain.Headline(full.Content))
		}
		if light.Spec != "" || light.Plan != "" || light.WorkDone != "" || light.Creator != full.Creator {
			t.Fatalf("goal %d carries text it should not: %+v", full.ID, light)
		}
	}

	allTasks, err := s.ListTasksForAllGoals(ctx)
	if err != nil {
		t.Fatalf("ListTasksForAllGoals: %v", err)
	}
	allOpen, err := s.ListOpenTaskHandoffsForAllGoals(ctx)
	if err != nil {
		t.Fatalf("ListOpenTaskHandoffsForAllGoals: %v", err)
	}
	openCount := 0
	for _, g := range goals {
		perGoal, err := s.ListTasks(ctx, g.ID)
		if err != nil {
			t.Fatalf("ListTasks %d: %v", g.ID, err)
		}
		if !reflect.DeepEqual(allTasks[g.ID], perGoal) {
			t.Fatalf("tasks for goal %d differ:\nbatch:    %+v\nper-goal: %+v", g.ID, allTasks[g.ID], perGoal)
		}
		perOpen, err := s.ListOpenTaskHandoffsForGoal(ctx, g.ID)
		if err != nil {
			t.Fatalf("ListOpenTaskHandoffsForGoal %d: %v", g.ID, err)
		}
		for taskID, handoff := range perOpen {
			openCount++
			if got, ok := allOpen[taskID]; !ok || !reflect.DeepEqual(got, handoff) {
				t.Fatalf("open handoff for task %d differs:\nbatch:    %+v\nper-goal: %+v", taskID, got, handoff)
			}
		}
	}
	if len(allOpen) != openCount || openCount != 1 {
		t.Fatalf("open handoffs: batch %d, per-goal %d; want 1 open handoff", len(allOpen), openCount)
	}
	if _, ok := allOpen[tasksH[1].ID]; ok {
		t.Fatalf("completed handoff listed as open")
	}
}
