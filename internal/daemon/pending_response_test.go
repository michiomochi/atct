package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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
	goal, err := s.CreateGoal(ctx, project.ID, "goal\n\ndescription", "human")
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
		Options:  []domain.Option{{Label: "A"}}, AgentSessionID: daemonTestSessionID(t, s, "answer-run"),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: decision.ID, AnswerLabel: "A", AnswerText: "Use A"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	claimSessionID := daemonTestSessionID(t, s, "claim-run")
	if err := s.AssociateAgentSessionWithProject(ctx, claimSessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"task_id": tasks[0].ID, "agent_session_id": claimSessionID, "include_unapplied_answers": true,
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
		t.Fatalf("unmarshal task.claim response %v: %v", raw, err)
	}
	if len(response.UnappliedDecisions) != 1 || response.UnappliedDecisions[0].DecisionID != decision.ID {
		t.Fatalf("unapplied_decisions = %#v, want %v", response.UnappliedDecisions, decision.ID)
	}

	got, err := s.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered || got.AppliedAt != nil {
		t.Fatalf("decision after notification = status %v applied_at %v, want answered and unapplied", got.Status, got.AppliedAt)
	}
}

func TestGoalListNotificationIsProjectScoped(t *testing.T) {
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	projectRoot := t.TempDir()
	otherRoot := t.TempDir()
	project := createPendingResponseProject(t, s, projectRoot, "project")
	otherProject := createPendingResponseProject(t, s, otherRoot, "other")
	goal, err := s.CreateGoal(ctx, project.ID, "goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, otherProject.ID, "other-goal\n\ndescription", "human")
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
	querySessionID := daemonTestSessionID(t, s, "query-run")

	params, err := json.Marshal(map[string]any{
		"cwd": projectRoot, "agent_session_id": querySessionID, "include_unapplied_answers": true,
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
		t.Fatalf("unmarshal goal.list response %v: %v", raw, err)
	}
	if len(response.UnappliedDecisions) != 1 || response.UnappliedDecisions[0].DecisionID != decision.ID {
		t.Fatalf("unapplied_decisions = %#v, want only %v (excluded %v)", response.UnappliedDecisions, decision.ID, otherDecision.ID)
	}
}

func TestDecisionPollNotificationExcludesPolledDecision(t *testing.T) {
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "project")
	goal, err := s.CreateGoal(ctx, project.ID, "goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "declare-1", []string{"poll target", "other task"}, []string{"Complete the task whose decision is being polled.", "Complete the other task independently."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	polled := answerPendingResponseDecision(t, s, goal.ID, tasks[0].ID, "polled decision")
	other := answerPendingResponseDecision(t, s, goal.ID, tasks[1].ID, "other decision")
	pollSessionID := daemonTestSessionID(t, s, "poll-run")

	params, err := json.Marshal(map[string]any{
		"agent_session_id": pollSessionID, "decision_id": polled.ID, "include_unapplied_answers": true,
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
		t.Fatalf("unmarshal decision.poll response %v: %v", raw, err)
	}
	if len(response.UnappliedDecisions) != 1 || response.UnappliedDecisions[0].DecisionID != other.ID {
		t.Fatalf("unapplied_decisions = %#v, want only %v (excluded %v)", response.UnappliedDecisions, other.ID, polled.ID)
	}
}

func TestDecisionAskParkedIncludesClaimableTasks(t *testing.T) {
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "project")
	parkGoal, err := s.CreateGoal(ctx, project.ID, "parked-goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal(parked): %v", err)
	}
	parked, err := s.DeclareTasks(ctx, parkGoal.ID, "agent", "declare-parked", []string{"parked task"}, []string{"Complete the parked task after the human response arrives."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(parked): %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, project.ID, "other-goal\n\ndescription", "human")
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
	otherSessionID := daemonTestSessionID(t, s, "other-run")
	if _, err := s.ClaimTask(ctx, candidates[1].ID, otherSessionID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	parkSessionID := daemonTestSessionID(t, s, "park-run")
	if err := s.AssociateAgentSessionWithProject(ctx, parkSessionID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject(park-run): %v", err)
	}
	params, err := json.Marshal(map[string]any{
		"goal_id": parkGoal.ID, "task_id": parked[0].ID, "question": "Which implementation should be used?",
		"options": []domain.Option{{Label: "A"}}, "agent_session_id": parkSessionID, "wait_ms": 0,
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
		t.Fatalf("unmarshal decision.ask response %v: %v", raw, err)
	}
	if len(response.ClaimableTasks) != 3 {
		t.Fatalf("claimable_tasks = %#v, want three tasks", response.ClaimableTasks)
	}
	wantTitles := []string{"free-1", "free-2", "free-3"}
	for i, want := range wantTitles {
		if response.ClaimableTasks[i].Title != want {
			t.Fatalf("claimable_tasks[%d] = %#v, want title %v", i, response.ClaimableTasks[i], want)
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
		name         string
		method       string
		params       map[string]any
		wantContains []string
	}{
		{
			name:   "task.claim",
			method: "task.claim",
			params: map[string]any{"task_id": f.targetTask.ID, "agent_session_id": f.agentSessionID},
		},
		{
			name:   "task.update",
			method: "task.update",
			params: map[string]any{"task_id": f.targetTask.ID, "status": "done", "agent_session_id": f.agentSessionID},
		},
		{
			name:   "task.declare",
			method: "task.declare",
			params: map[string]any{
				"goal_id": f.targetGoal.ID, "agent": "agent", "idempotency_key": "cross-project",
				"titles": []string{"must be rejected"}, "descriptions": []string{"Complete the task only in the assigned project."}, "agent_session_id": f.agentSessionID,
			},
		},
		{
			name:   "decision.ask",
			method: "decision.ask",
			params: map[string]any{
				"goal_id": f.targetGoal.ID, "task_id": f.targetTask.ID, "question": "must be rejected",
				"options": []domain.Option{{Label: "yes"}}, "agent_session_id": f.agentSessionID, "wait_ms": 0,
			},
		},
		{
			name:   "goal.complete",
			method: "goal.complete",
			params: map[string]any{
				"goal_id": f.targetGoal.ID, "work_done": "done", "now_possible": "now",
				"how_to_verify": "verify", "surprises": "none", "needs_review": "none",
				"next_steps": "none", "agent_session_id": f.agentSessionID,
			},
			wantContains: []string{"goal completion denied", "holds no open goal handoff"},
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
				want := tc.wantContains
				if want == nil {
					want = []string{f.assigned.Name, f.target.Name}
				}
				for _, s := range want {
					if !strings.Contains(err.Error(), s) {
						t.Fatalf("error = %v, want message containing %v", err, s)
					}
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
		t.Fatalf("target goal status after rejected writes = %v, want active", goal.Status)
	}
}

func TestTaskWritesWithoutAgentSessionIDSkipProjectGuard(t *testing.T) {
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
		t.Fatalf("unmarshal task.declare response %v: %v", raw, err)
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
		t.Fatalf("unmarshal task.update response %v: %v", raw, err)
	}
	if updated.Status != domain.TaskDone {
		t.Fatalf("updated task = %#v, want done", updated)
	}
}

func TestTaskUpdateWithoutCommitsPreservesExistingBehavior(t *testing.T) {
	root, _ := createPendingResponseGitRepository(t)
	s := openPendingResponseTestStore(t)
	ctx := context.Background()
	project := createPendingResponseProject(t, s, root, "task-update-no-commits")
	goal, err := s.CreateGoal(ctx, project.ID, "task-update-goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "task-update-no-commits", []string{"task update"}, []string{"Complete the task update."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	d := New(s)

	params, err := json.Marshal(map[string]any{
		"task_id": tasks[0].ID, "status": string(domain.TaskDone),
	})
	if err != nil {
		t.Fatalf("Marshal task.update params: %v", err)
	}
	raw, err := d.dispatch(ctx, rpc.Request{Method: "task.update", Params: params})
	if err != nil {
		t.Fatalf("task.update: %v", err)
	}
	var updated domain.Task
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatalf("unmarshal task.update response %v: %v", raw, err)
	}
	if updated.Status != domain.TaskDone {
		t.Fatalf("updated task = %#v, want done", updated)
	}
	commits, err := s.ListTaskCommits(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("ListTaskCommits: %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("task commits = %#v, want none", commits)
	}
}

func TestTaskUpdateWithUnknownCommitKeepsStatusUnchanged(t *testing.T) {
	root, _ := createPendingResponseGitRepository(t)
	s := openPendingResponseTestStore(t)
	ctx := context.Background()
	project := createPendingResponseProject(t, s, root, "task-update-unknown-commit")
	goal, err := s.CreateGoal(ctx, project.ID, "task-update-goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "task-update-unknown-commit", []string{"task update"}, []string{"Complete the task update."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	d := New(s)

	params, err := json.Marshal(map[string]any{
		"task_id": tasks[0].ID, "status": string(domain.TaskDone),
		"commits": []string{"0000000000000000000000000000000000000000"},
	})
	if err != nil {
		t.Fatalf("Marshal task.update params: %v", err)
	}
	if _, err := d.dispatch(ctx, rpc.Request{Method: "task.update", Params: params}); err == nil {
		t.Fatal("task.update succeeded, want unknown commit error")
	}
	storedTasks, err := s.ListTasks(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(storedTasks) != 1 || storedTasks[0].Status != domain.TaskTodo {
		t.Fatalf("target tasks after rejected commit = %#v, want one todo task", storedTasks)
	}
}

func TestTaskUpdateWithDuplicateCommitLinksOnce(t *testing.T) {
	root, sha := createPendingResponseGitRepository(t)
	s := openPendingResponseTestStore(t)
	ctx := context.Background()
	project := createPendingResponseProject(t, s, root, "task-update-duplicate-commit")
	goal, err := s.CreateGoal(ctx, project.ID, "task-update-goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "task-update-duplicate-commit", []string{"task update"}, []string{"Complete the task update."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	d := New(s)
	params, err := json.Marshal(map[string]any{
		"task_id": tasks[0].ID, "status": string(domain.TaskDone),
		"commits": []string{sha, sha},
	})
	if err != nil {
		t.Fatalf("Marshal task.update params: %v", err)
	}
	if _, err := d.dispatch(ctx, rpc.Request{Method: "task.update", Params: params}); err != nil {
		t.Fatalf("task.update: %v", err)
	}
	commits, err := s.ListTaskCommits(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("ListTaskCommits: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("task commits = %#v, want one commit", commits)
	}
	if commits[0].SHA != sha {
		t.Fatalf("linked commit SHA = %v, want %v", commits[0].SHA, sha)
	}
}

func TestTaskClaimAssignsUnassociatedRunToTargetProject(t *testing.T) {
	f := newProjectScopeFixture(t)
	agentSessionID := daemonTestSessionID(t, f.store, "first-write-run")

	params, err := json.Marshal(map[string]any{
		"task_id":          f.targetTask.ID,
		"agent_session_id": agentSessionID,
	})
	if err != nil {
		t.Fatalf("Marshal task.claim params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.claim", Params: params}); err != nil {
		t.Fatalf("task.claim: %v", err)
	}

	projectID, err := f.store.ProjectIDForAgentSession(f.ctx, agentSessionID)
	if err != nil {
		t.Fatalf("ProjectIDForAgentSession: %v", err)
	}
	if projectID != f.target.ID {
		t.Fatalf("run project_id = %v, want target project %v", projectID, f.target.ID)
	}
}

func TestProjectScopedWritesAllowAssignedProjectAndGoalListReadsOtherProject(t *testing.T) {
	f := newProjectScopeFixture(t)

	params, err := json.Marshal(map[string]any{
		"goal_id": f.assignedGoal.ID, "agent": "agent", "idempotency_key": "assigned-project",
		"titles": []string{"assigned declaration"}, "descriptions": []string{"Complete the declaration in the assigned project."}, "agent_session_id": f.agentSessionID,
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
		t.Fatalf("unmarshal task.declare response %v: %v", raw, err)
	}
	if len(declared) != 1 {
		t.Fatalf("declared tasks = %#v, want one task", declared)
	}

	params, err = json.Marshal(map[string]any{"task_id": declared[0].ID, "agent_session_id": f.agentSessionID})
	if err != nil {
		t.Fatalf("Marshal task.claim params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.claim", Params: params}); err != nil {
		t.Fatalf("task.claim: %v", err)
	}
	params, err = json.Marshal(map[string]any{"task_id": declared[0].ID, "status": "done", "agent_session_id": f.agentSessionID})
	if err != nil {
		t.Fatalf("Marshal task.update params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "task.update", Params: params}); err != nil {
		t.Fatalf("task.update: %v", err)
	}

	params, err = json.Marshal(map[string]any{
		"goal_id": f.assignedGoal.ID, "task_id": declared[0].ID, "question": "assigned question",
		"options": []domain.Option{{Label: "yes"}}, "agent_session_id": f.agentSessionID, "wait_ms": 0,
	})
	if err != nil {
		t.Fatalf("Marshal decision.ask params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "decision.ask", Params: params}); err != nil {
		t.Fatalf("decision.ask: %v", err)
	}

	params, err = json.Marshal(map[string]any{"goal_id": f.completeGoal.ID, "agent_session_id": f.agentSessionID})
	if err != nil {
		t.Fatalf("Marshal goal.claim params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "goal.claim", Params: params}); err != nil {
		t.Fatalf("goal.claim before goal.complete: %v", err)
	}
	params, err = json.Marshal(map[string]any{
		"goal_id": f.completeGoal.ID, "work_done": "done", "now_possible": "now",
		"how_to_verify": "verify", "surprises": "none", "needs_review": "none",
		"next_steps": "none", "agent_session_id": f.agentSessionID,
	})
	if err != nil {
		t.Fatalf("Marshal goal.complete params: %v", err)
	}
	if _, err := f.daemon.dispatch(f.ctx, rpc.Request{Method: "goal.complete", Params: params}); err != nil {
		t.Fatalf("goal.complete: %v", err)
	}

	readSessionID := daemonTestSessionID(t, f.store, "read-run")
	params, err = json.Marshal(map[string]any{"cwd": f.target.RootPath, "agent_session_id": readSessionID})
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
		t.Fatalf("unmarshal goal.list response %v: %v", raw, err)
	}
	if listed.Project.ID != f.target.ID {
		t.Fatalf("goal.list project = %#v, want %v", listed.Project, f.target.ID)
	}
}

type projectScopeFixture struct {
	ctx            context.Context
	store          *store.Store
	daemon         *Daemon
	assigned       domain.Project
	target         domain.Project
	agentSessionID int64
	assignedGoal   domain.Goal
	targetGoal     domain.Goal
	completeGoal   domain.Goal
	targetTask     domain.Task
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
	assignedGoal, err := s.CreateGoal(ctx, assigned.ID, "assigned-goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal(assigned): %v", err)
	}
	targetGoal, err := s.CreateGoal(ctx, target.ID, "target-goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal(target): %v", err)
	}
	completeGoal, err := s.CreateGoal(ctx, assigned.ID, "complete-goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal(complete): %v", err)
	}
	targetTasks, err := s.DeclareTasks(ctx, targetGoal.ID, "agent", "target-initial", []string{"target task"}, []string{"Complete the target task after selecting its project."}, nil)
	if err != nil {
		t.Fatalf("DeclareTasks(target): %v", err)
	}
	agentSessionID := daemonTestSessionID(t, s, "assigned-run")
	if err := s.AssociateAgentSessionWithProject(ctx, agentSessionID, assigned.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	return projectScopeFixture{
		ctx: ctx, store: s, daemon: New(s), assigned: assigned, target: target,
		agentSessionID: agentSessionID, assignedGoal: assignedGoal, targetGoal: targetGoal,
		completeGoal: completeGoal, targetTask: targetTasks[0],
	}
}

type pendingResponseEnvelope struct {
	Data               json.RawMessage               `json:"data"`
	UnappliedDecisions []pendingDecisionNotification `json:"unapplied_decisions"`
	ClaimableTasks     []pendingClaimableTask        `json:"claimable_tasks"`
}

type pendingDecisionNotification struct {
	DecisionID int64  `json:"decision_id"`
	Question   string `json:"question"`
}

type pendingClaimableTask struct {
	ID    int64  `json:"id"`
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

func createPendingResponseGitRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	runGit := func(args ...string) string {
		cmdArgs := append([]string{"-C", root}, args...)
		output, err := exec.Command("git", cmdArgs...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%v", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	runGit("init")
	runGit("config", "user.email", "pending-response-test@example.com")
	runGit("config", "user.name", "Pending Response Test")
	if err := os.WriteFile(filepath.Join(root, "commit.txt"), []byte("pending response test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit("add", "commit.txt")
	runGit("commit", "-m", "pending response test commit")
	return root, runGit("rev-parse", "HEAD")
}

func answerPendingResponseDecision(t *testing.T, s *store.Store, goalID, taskID int64, question string) domain.Decision {
	t.Helper()
	ctx := context.Background()
	decision, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: question,
		Options: []domain.Option{{Label: "yes"}}, AgentSessionID: daemonTestSessionID(t, s, "answer-run"),
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
