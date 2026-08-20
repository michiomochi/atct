package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-1", []string{"task"}, []string{"Complete the task before applying its pending decision."}, nil)
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
	if err := s.AssociateRunWithProject(ctx, "claim-run", project.ID); err != nil {
		t.Fatalf("AssociateRunWithProject: %v", err)
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
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-project", []string{"project task"}, []string{"Complete the project task before returning its pending response."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(project): %v", err)
	}
	otherTasks, err := s.DeclareTasks(ctx, otherGoal.ID, "agent", "declare-other", []string{"other task"}, []string{"Complete the task in the other project without mixing responses."}, nil)
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
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-1", []string{"poll target", "other task"}, []string{"Complete the task whose decision is being polled.", "Complete the other task independently."}, nil)
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
	parked, err := s.DeclareTasks(ctx, parkGoal.ID, "agent", "declare-parked", []string{"parked task"}, []string{"Complete the parked task after the human response arrives."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(parked): %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, project.ID, "other-goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal(other): %v", err)
	}
	candidates, err := s.DeclareTasks(ctx, otherGoal.ID, "agent", "declare-candidates", []string{
		"free-1", "claimed", "free-2", "free-3", "free-4",
	}, []string{
		"Complete the first free candidate task.",
		"Complete the candidate task that another run may claim.",
		"Complete the second free candidate task.",
		"Complete the third free candidate task.",
		"Complete the fourth free candidate task.",
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
	if err := s.RegisterRun(ctx, "park-run"); err != nil {
		t.Fatalf("RegisterRun(park-run): %v", err)
	}
	if err := s.AssociateRunWithProject(ctx, "park-run", project.ID); err != nil {
		t.Fatalf("AssociateRunWithProject(park-run): %v", err)
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

func TestProjectScopedWritesRejectOtherProject(t *testing.T) {
	f := newProjectScopeFixture(t)
	cases := []struct {
		name   string
		method string
		params map[string]any
	}{
		{
			name:   "task.claim",
			method: "task.claim",
			params: map[string]any{"task_id": f.targetTask.ID, "run_id": f.runID},
		},
		{
			name:   "task.update",
			method: "task.update",
			params: map[string]any{"task_id": f.targetTask.ID, "status": "done", "run_id": f.runID},
		},
		{
			name:   "task.declare",
			method: "task.declare",
			params: map[string]any{
				"goal_id": f.targetGoal.ID, "agent": "agent", "idempotency_key": "cross-project",
				"titles": []string{"must be rejected"}, "descriptions": []string{"Complete the task only in the assigned project."}, "run_id": f.runID,
			},
		},
		{
			name:   "decision.ask",
			method: "decision.ask",
			params: map[string]any{
				"goal_id": f.targetGoal.ID, "task_id": f.targetTask.ID, "question": "must be rejected",
				"options": []domain.Option{{Label: "yes"}}, "run_id": f.runID, "wait_ms": 0,
			},
		},
		{
			name:   "goal.complete",
			method: "goal.complete",
			params: map[string]any{
				"goal_id": f.targetGoal.ID, "work_done": "done", "now_possible": "now",
				"how_to_verify": "verify", "surprises": "none", "needs_review": "none",
				"next_steps": "none", "run_id": f.runID,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, err := json.Marshal(tc.params)
			if err != nil {
				t.Fatalf("Marshal params: %v", err)
			}
			if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: tc.method, Params: params}); err == nil {
				t.Fatal("dispatch succeeded, want cross-project error")
			} else {
				if !strings.Contains(err.Error(), f.assigned.Name) {
					t.Fatalf("error = %q, want assigned project %q", err, f.assigned.Name)
				}
				if !strings.Contains(err.Error(), f.target.Name) {
					t.Fatalf("error = %q, want target project %q", err, f.target.Name)
				}
			}
		})
	}

	tasks, err := f.store.ListTasks(f.ctx, f.targetGoal.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskTodo {
		t.Fatalf("target tasks after rejected writes = %#v, want one todo task", tasks)
	}
	goal, err := f.store.GetGoal(f.ctx, f.targetGoal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if goal.Status != "active" {
		t.Fatalf("target goal status after rejected writes = %q, want active", goal.Status)
	}
}

func TestTaskWritesWithoutRunIDSkipProjectGuard(t *testing.T) {
	f := newProjectScopeFixture(t)

	params, err := json.Marshal(map[string]any{
		"goal_id": f.assignedGoal.ID, "agent": "agent", "idempotency_key": "without-run-id",
		"titles": []string{"no run id"}, "descriptions": []string{"Complete the task without requiring a run identifier."},
	})
	if err != nil {
		t.Fatalf("Marshal task.declare params: %v", err)
	}
	raw, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.declare", Params: params})
	if err != nil {
		t.Fatalf("task.declare: %v", err)
	}
	var declared []domain.Task
	if err := json.Unmarshal(raw, &declared); err != nil {
		t.Fatalf("unmarshal task.declare response %s: %v", raw, err)
	}
	if len(declared) != 1 {
		t.Fatalf("declared tasks = %#v, want one task", declared)
	}

	params, err = json.Marshal(map[string]any{
		"task_id": declared[0].ID, "status": string(domain.TaskDone),
	})
	if err != nil {
		t.Fatalf("Marshal task.update params: %v", err)
	}
	raw, err = f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.update", Params: params})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	var updated domain.Task
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatalf("unmarshal task.update response %s: %v", raw, err)
	}
	if updated.Status != domain.TaskDone {
		t.Fatalf("updated task = %#v, want done", updated)
	}
}

func TestTaskClaimAssignsUnassociatedRunToTargetProject(t *testing.T) {
	f := newProjectScopeFixture(t)
	runID := "first-write-run"
	if err := f.store.RegisterRun(f.ctx, runID); err != nil {
		t.Fatalf("RegisterRun: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"task_id": f.targetTask.ID,
		"run_id":  runID,
	})
	if err != nil {
		t.Fatalf("Marshal task.claim params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.claim", Params: params}); err != nil {
		t.Fatalf("task.claim: %v", err)
	}

	projectID, err := f.store.ProjectIDForRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("ProjectIDForRun: %v", err)
	}
	if projectID != f.target.ID {
		t.Fatalf("run project_id = %q, want target project %q", projectID, f.target.ID)
	}
}

func TestProjectScopedWritesAllowAssignedProjectAndGoalListReadsOtherProject(t *testing.T) {
	f := newProjectScopeFixture(t)

	params, err := json.Marshal(map[string]any{
		"goal_id": f.assignedGoal.ID, "agent": "agent", "idempotency_key": "assigned-project",
		"titles": []string{"assigned declaration"}, "descriptions": []string{"Complete the declaration in the assigned project."}, "run_id": f.runID,
	})
	if err != nil {
		t.Fatalf("Marshal task.declare params: %v", err)
	}
	raw, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.declare", Params: params})
	if err != nil {
		t.Fatalf("task.declare: %v", err)
	}
	var declared []domain.Task
	if err := json.Unmarshal(raw, &declared); err != nil {
		t.Fatalf("unmarshal task.declare response %s: %v", raw, err)
	}
	if len(declared) != 1 {
		t.Fatalf("declared tasks = %#v, want one task", declared)
	}

	params, err = json.Marshal(map[string]any{"task_id": declared[0].ID, "run_id": f.runID})
	if err != nil {
		t.Fatalf("Marshal task.claim params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.claim", Params: params}); err != nil {
		t.Fatalf("task.claim: %v", err)
	}
	params, err = json.Marshal(map[string]any{"task_id": declared[0].ID, "status": "done", "run_id": f.runID})
	if err != nil {
		t.Fatalf("Marshal task.update params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.update", Params: params}); err != nil {
		t.Fatalf("task.update: %v", err)
	}

	params, err = json.Marshal(map[string]any{
		"goal_id": f.assignedGoal.ID, "task_id": declared[0].ID, "question": "assigned question",
		"options": []domain.Option{{Label: "yes"}}, "run_id": f.runID, "wait_ms": 0,
	})
	if err != nil {
		t.Fatalf("Marshal decision.ask params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "decision.ask", Params: params}); err != nil {
		t.Fatalf("decision.ask: %v", err)
	}

	params, err = json.Marshal(map[string]any{
		"goal_id": f.completeGoal.ID, "work_done": "done", "now_possible": "now",
		"how_to_verify": "verify", "surprises": "none", "needs_review": "none",
		"next_steps": "none", "run_id": f.runID,
	})
	if err != nil {
		t.Fatalf("Marshal goal.complete params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "goal.complete", Params: params}); err != nil {
		t.Fatalf("goal.complete: %v", err)
	}

	if err := f.store.RegisterRun(f.ctx, "read-run"); err != nil {
		t.Fatalf("RegisterRun(read-run): %v", err)
	}
	params, err = json.Marshal(map[string]any{"cwd": f.target.RootPath, "run_id": "read-run"})
	if err != nil {
		t.Fatalf("Marshal goal.list params: %v", err)
	}
	raw, err = f.daemon.dispatch(f.ctx, rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var listed struct {
		Project domain.Project `json:"project"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("unmarshal goal.list response %s: %v", raw, err)
	}
	if listed.Project.ID != f.target.ID {
		t.Fatalf("goal.list project = %#v, want %q", listed.Project, f.target.ID)
	}
}

type projectScopeFixture struct {
	ctx          context.Context
	store        *store.Store
	daemon       *Daemon
	assigned     domain.Project
	target       domain.Project
	runID        string
	assignedGoal domain.Goal
	targetGoal   domain.Goal
	completeGoal domain.Goal
	targetTask   domain.Task
}

func newProjectScopeFixture(t *testing.T) projectScopeFixture {
	t.Helper()
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	assigned, err := s.CreateProject(ctx, "assigned-project", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject(assigned): %v", err)
	}
	target, err := s.CreateProject(ctx, "target-project", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject(target): %v", err)
	}
	assignedGoal, err := s.CreateGoal(ctx, assigned.ID, "assigned-goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal(assigned): %v", err)
	}
	targetGoal, err := s.CreateGoal(ctx, target.ID, "target-goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal(target): %v", err)
	}
	completeGoal, err := s.CreateGoal(ctx, assigned.ID, "complete-goal", "description")
	if err != nil {
		t.Fatalf("CreateGoal(complete): %v", err)
	}
	targetTasks, err := s.DeclareTasks(ctx, targetGoal.ID, "agent", "target-initial", []string{"target task"}, []string{"Complete the target task after selecting its project."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(target): %v", err)
	}
	if err := s.RegisterRun(ctx, "assigned-run"); err != nil {
		t.Fatalf("RegisterRun: %v", err)
	}
	if err := s.AssociateRunWithProject(ctx, "assigned-run", assigned.ID); err != nil {
		t.Fatalf("AssociateRunWithProject: %v", err)
	}
	return projectScopeFixture{
		ctx: ctx, store: s, daemon: New(s), assigned: assigned, target: target,
		runID: "assigned-run", assignedGoal: assignedGoal, targetGoal: targetGoal,
		completeGoal: completeGoal, targetTask: targetTasks[0],
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
