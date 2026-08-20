package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// Success criterion 1: reach registration, decomposition, decision, approval, and completion.
func TestFullGoalLifecycle(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	g, err := s.CreateGoal(ctx, ns.ID, "Build an MCP server", "eight tools")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	tasks, err := s.DeclareTasks(ctx, g.ID, "codex", "k1", []string{"Design", "Implement", "Test"}, []string{"Define the server behavior and data flow.", "Implement the server behavior in Go.", "Run the tests that verify the server behavior."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}

	d, err := s.AskDecision(ctx, store.AskInput{
		GoalID: g.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision,
		Question: "Is SQLite acceptable for the store?",
		Options: []domain.Option{
			{Label: "sqlite", Description: "SQLite", Consequence: "No external server required"},
		},
		RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	if _, err := s.AnswerDecision(ctx, store.AnswerInput{
		DecisionID: d.ID, AnswerLabel: "sqlite",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	applied, err := s.PollDecisions(ctx, "run-1", "")
	if err != nil {
		t.Fatalf("PollDecisions: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("polled %d decisions, want 1", len(applied))
	}

	for _, tk := range tasks {
		if _, err := s.UpdateTask(ctx, tk.ID, domain.TaskDone); err != nil {
			t.Fatalf("UpdateTask done (%s): %v", tk.Title, err)
		}
	}

	comp, err := s.CompleteGoalWithReport(ctx, g.ID, domain.CompletionReport{
		WorkDone:    "All tasks complete. SQLite was selected.",
		NowPossible: "The goal can be approved.",
		HowToVerify: "Inspect the completed task statuses.",
		Surprises:   "なし",
		NeedsReview: "なし",
		NextSteps:   "なし",
	}, "run-1")
	if err != nil {
		t.Fatalf("CompleteGoal: %v", err)
	}
	done, err := s.ApproveCompletion(ctx, comp.ID)
	if err != nil {
		t.Fatalf("ApproveCompletion: %v", err)
	}
	if done.Status != domain.GoalDone {
		t.Fatalf("goal status = %q, want %q", done.Status, domain.GoalDone)
	}
}

// Success criterion 2: an answer survives a session change and is recovered by the next run.
func TestAnswerSurvivesSessionChange(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "atct.db")

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ns, _ := s.CreateProject(ctx, "atct", "/repos/atct")
	g, _ := s.CreateGoal(ctx, ns.ID, "goal", "")
	// An active decision has to name the task it is holding up.
	tasks, err := s.DeclareTasks(ctx, g.ID, "agent", "batch-1", []string{"do the thing"}, []string{"Complete the requested work and verify its observable result."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	d, err := s.AskDecision(ctx, store.AskInput{
		GoalID: g.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision,
		Question: "What should we do?", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{
		DecisionID: d.ID, AnswerLabel: "A",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	s.Close() // Simulate the run-1 session dying

	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	unapplied, err := reopened.ListUnappliedDecisions(ctx)
	if err != nil {
		t.Fatalf("ListUnappliedDecisions: %v", err)
	}
	if len(unapplied) != 1 || unapplied[0].ID != d.ID {
		t.Fatalf("unapplied = %+v, want the decision from the dead session", unapplied)
	}
	if unapplied[0].AnswerLabel != "A" {
		t.Fatalf("answer lost across sessions: %q", unapplied[0].AnswerLabel)
	}
}
