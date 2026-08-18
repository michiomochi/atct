package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestTaskClaimNotificationDoesNotApplyDecision(t *testing.T) {
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "project")
	goal, err := s.CreateGoal(ctx, project.ID, "goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-1", []string{"task"}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision,
		Question: "Which implementation should be used?",
		Options:  []domain.Option{{Label: "A"}}, RunID: "answer-run",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: decision.ID, AnswerLabel: "A", AnswerText: "Use A"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	if err := s.RegisterRun(ctx, "claim-run"); err != nil {
		t.Fatalf("RegisterRun: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"task_id": tasks[0].ID, "run_id": "claim-run", "include_unapplied_answers": true,
	})
	if err != nil {
		t.Fatalf("Marshal params: %v", err)
	}
	raw, err := New(s).dispatch(ctx, rpc.Request{Method: "task.claim", Params: params})
	if err != nil {
		t.Fatalf("task.claim: %v", err)
	}
	var response pendingResponseEnvelope
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("unmarshal task.claim response %s: %v", raw, err)
	}
	if len(response.UnappliedDecisions) != 1 || response.UnappliedDecisions[0].DecisionID != decision.ID {
		t.Fatalf("unapplied_decisions = %#v, want %s", response.UnappliedDecisions, decision.ID)
	}

	got, err := s.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered || got.AppliedAt != nil {
		t.Fatalf("decision after notification = status %q applied_at %v, want answered and unapplied", got.Status, got.AppliedAt)
	}
}

func TestGoalListNotificationIsProjectScoped(t *testing.T) {
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	projectRoot := t.TempDir()
	otherRoot := t.TempDir()
	project := createPendingResponseProject(t, s, projectRoot, "project")
	otherProject := createPendingResponseProject(t, s, otherRoot, "other")
	goal, err := s.CreateGoal(ctx, project.ID, "goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, otherProject.ID, "other-goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal(other): %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-project", []string{"project task"}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(project): %v", err)
	}
	otherTasks, err := s.DeclareTasks(ctx, otherGoal.ID, "agent", "declare-other", []string{"other task"}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(other): %v", err)
	}
	decision := answerPendingResponseDecision(t, s, goal.ID, tasks[0].ID, "project decision")
	otherDecision := answerPendingResponseDecision(t, s, otherGoal.ID, otherTasks[0].ID, "other project decision")
	if err := s.RegisterRun(ctx, "query-run"); err != nil {
		t.Fatalf("RegisterRun: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"cwd": projectRoot, "run_id": "query-run", "include_unapplied_answers": true,
	})
	if err != nil {
		t.Fatalf("Marshal params: %v", err)
	}
	raw, err := New(s).dispatch(ctx, rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response pendingResponseEnvelope
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("unmarshal goal.list response %s: %v", raw, err)
	}
	if len(response.UnappliedDecisions) != 1 || response.UnappliedDecisions[0].DecisionID != decision.ID {
		t.Fatalf("unapplied_decisions = %#v, want only %s (excluded %s)", response.UnappliedDecisions, decision.ID, otherDecision.ID)
	}
}

func TestDecisionPollNotificationExcludesPolledDecision(t *testing.T) {
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "project")
	goal, err := s.CreateGoal(ctx, project.ID, "goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-1", []string{"poll target", "other task"}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	polled := answerPendingResponseDecision(t, s, goal.ID, tasks[0].ID, "polled decision")
	other := answerPendingResponseDecision(t, s, goal.ID, tasks[1].ID, "other decision")
	if err := s.RegisterRun(ctx, "poll-run"); err != nil {
		t.Fatalf("RegisterRun: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"run_id": "poll-run", "decision_id": polled.ID, "include_unapplied_answers": true,
	})
	if err != nil {
		t.Fatalf("Marshal params: %v", err)
	}
	raw, err := New(s).dispatch(ctx, rpc.Request{Method: "decision.poll", Params: params})
	if err != nil {
		t.Fatalf("decision.poll: %v", err)
	}
	var response pendingResponseEnvelope
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("unmarshal decision.poll response %s: %v", raw, err)
	}
	if len(response.UnappliedDecisions) != 1 || response.UnappliedDecisions[0].DecisionID != other.ID {
		t.Fatalf("unapplied_decisions = %#v, want only %s (excluded %s)", response.UnappliedDecisions, other.ID, polled.ID)
	}
}

func TestDecisionAskParkedIncludesClaimableTasks(t *testing.T) {
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "project")
	parkGoal, err := s.CreateGoal(ctx, project.ID, "parked-goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal(parked): %v", err)
	}
	parked, err := s.DeclareTasks(ctx, parkGoal.ID, "agent", "declare-parked", []string{"parked task"}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(parked): %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, project.ID, "other-goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal(other): %v", err)
	}
	candidates, err := s.DeclareTasks(ctx, otherGoal.ID, "agent", "declare-candidates", []string{
		"free-1", "claimed", "free-2", "free-3", "free-4",
	}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(candidates): %v", err)
	}
	if _, err := s.ClaimTask(ctx, candidates[1].ID, "other-run"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"goal_id": parkGoal.ID, "task_id": parked[0].ID, "question": "Which implementation should be used?",
		"options": []domain.Option{{Label: "A"}}, "run_id": "park-run", "wait_ms": 0,
		"include_unapplied_answers": true,
	})
	if err != nil {
		t.Fatalf("Marshal params: %v", err)
	}
	raw, err := New(s).dispatch(ctx, rpc.Request{Method: "decision.ask", Params: params})
	if err != nil {
		t.Fatalf("decision.ask: %v", err)
	}
	var response pendingResponseEnvelope
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("unmarshal decision.ask response %s: %v", raw, err)
	}
	if len(response.ClaimableTasks) != 3 {
		t.Fatalf("claimable_tasks = %#v, want three tasks", response.ClaimableTasks)
	}
	wantTitles := []string{"free-1", "free-2", "free-3"}
	for i, want := range wantTitles {
		if response.ClaimableTasks[i].Title != want {
			t.Fatalf("claimable_tasks[%d] = %#v, want title %q", i, response.ClaimableTasks[i], want)
		}
	}
	for _, task := range response.ClaimableTasks {
		if task.ID == parked[0].ID || task.ID == candidates[1].ID || task.Title == "free-4" {
			t.Fatalf("claimable_tasks contains excluded task: %#v", task)
		}
	}
}

type pendingResponseEnvelope struct {
	Data               json.RawMessage               `json:"data"`
	UnappliedDecisions []pendingDecisionNotification `json:"unapplied_decisions"`
	ClaimableTasks     []pendingClaimableTask        `json:"claimable_tasks"`
}

type pendingDecisionNotification struct {
	DecisionID string `json:"decision_id"`
	Question   string `json:"question"`
}

type pendingClaimableTask struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func openPendingResponseTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func createPendingResponseProject(t *testing.T, s *store.Store, root, name string) domain.Project {
	t.Helper()
	project, err := s.CreateProject(context.Background(), name, root)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return project
}

func answerPendingResponseDecision(t *testing.T, s *store.Store, goalID, taskID, question string) domain.Decision {
	t.Helper()
	ctx := context.Background()
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: question,
		Options: []domain.Option{{Label: "yes"}}, RunID: "answer-run",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	answered, err := s.AnswerDecision(ctx, store.AnswerInput{
		DecisionID: decision.ID, AnswerLabel: "yes", AnswerText: "yes",
	})
	if err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	return answered
}
