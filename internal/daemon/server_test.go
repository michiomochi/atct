package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestDaemonRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sock := filepath.Join(dir, "atct.sock")
	d := New(s)
	go d.Serve(ctx, sock)

	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	params, _ := json.Marshal(map[string]string{"name": "atct", "root_path": "/repos/atct"})
	req, _ := json.Marshal(rpc.Request{Method: "project.create", Params: params})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp rpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("rpc error: %s", resp.Error)
	}
	if len(resp.Result) == 0 {
		t.Fatal("empty result")
	}
}

func newDaemonConn(t *testing.T) net.Conn {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sock := filepath.Join(dir, "atct.sock")
	go New(s).Serve(ctx, sock)

	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func call(t *testing.T, conn net.Conn, method string, params any) rpc.Response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	req, err := json.Marshal(rpc.Request{Method: method, Params: raw})
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s: %v", method, err)
	}
	var resp rpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", method, err)
	}
	return resp
}

func TestDaemonListsProjects(t *testing.T) {
	conn := newDaemonConn(t)
	created := call(t, conn, "project.create", map[string]string{"name": "atct", "root_path": "/repos/atct"})
	if created.Error != "" {
		t.Fatalf("project.create: %s", created.Error)
	}

	listed := call(t, conn, "project.list", map[string]string{})
	if listed.Error != "" {
		t.Fatalf("project.list: %s", listed.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project.list returned %d projects, want 1", len(projects))
	}
	if projects[0].Name != "atct" {
		t.Fatalf("name = %q, want %q", projects[0].Name, "atct")
	}
}

func TestDaemonAutoRegistersProjectForGoalList(t *testing.T) {
	conn := newDaemonConn(t)
	resp := call(t, conn, "goal.list", map[string]string{"cwd": "/repos/auto-register"})
	if resp.Error != "" {
		t.Fatalf("goal.list: %s", resp.Error)
	}

	var result struct {
		Project domain.Project `json:"project"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal goal.list: %v", err)
	}
	if result.Project.Name != "auto-register" {
		t.Fatalf("project name = %q, want %q", result.Project.Name, "auto-register")
	}
}

func TestDaemonReusesAutoRegisteredProjectForGoalList(t *testing.T) {
	conn := newDaemonConn(t)
	params := map[string]string{"cwd": "/repos/auto-register"}
	for range 2 {
		resp := call(t, conn, "goal.list", params)
		if resp.Error != "" {
			t.Fatalf("goal.list: %s", resp.Error)
		}
	}

	listed := call(t, conn, "project.list", map[string]string{})
	if listed.Error != "" {
		t.Fatalf("project.list: %s", listed.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project.list returned %d projects, want 1", len(projects))
	}
}

func TestDaemonAutoRegistersDuplicateBasenameWithParentName(t *testing.T) {
	conn := newDaemonConn(t)
	created := call(t, conn, "project.create", map[string]string{
		"name":      "atct",
		"root_path": "/repos/old/atct",
	})
	if created.Error != "" {
		t.Fatalf("project.create: %s", created.Error)
	}

	resp := call(t, conn, "goal.list", map[string]string{"cwd": "/repos/new/atct"})
	if resp.Error != "" {
		t.Fatalf("goal.list: %s", resp.Error)
	}
	var result struct {
		Project domain.Project `json:"project"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal goal.list: %v", err)
	}
	if result.Project.Name != "new/atct" {
		t.Fatalf("project name = %q, want %q", result.Project.Name, "new/atct")
	}

	listed := call(t, conn, "project.list", map[string]string{})
	if listed.Error != "" {
		t.Fatalf("project.list: %s", listed.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	var foundOriginal bool
	for _, project := range projects {
		if project.RootPath == "/repos/old/atct" && project.Name == "atct" {
			foundOriginal = true
			break
		}
	}
	if !foundOriginal {
		t.Fatalf("original project name was changed or project was removed: %#v", projects)
	}
}

func TestDaemonAutoRegistersDuplicateBasenameAsSecondProject(t *testing.T) {
	conn := newDaemonConn(t)
	created := call(t, conn, "project.create", map[string]string{
		"name":      "atct",
		"root_path": "/repos/old/atct",
	})
	if created.Error != "" {
		t.Fatalf("project.create: %s", created.Error)
	}

	resp := call(t, conn, "goal.list", map[string]string{"cwd": "/repos/new/atct"})
	if resp.Error != "" {
		t.Fatalf("goal.list: %s", resp.Error)
	}

	listed := call(t, conn, "project.list", map[string]string{})
	if listed.Error != "" {
		t.Fatalf("project.list: %s", listed.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("project.list returned %d projects, want 2", len(projects))
	}
}

func TestDaemonListsProjectsWhenNoneExist(t *testing.T) {
	resp := call(t, newDaemonConn(t), "project.list", map[string]string{})
	if resp.Error != "" {
		t.Fatalf("project.list on an empty store: %s", resp.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(resp.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects, want 0", len(projects))
	}
}

func TestDaemonDerivesProjectNameFromNormalizedRoot(t *testing.T) {
	resp := call(t, newDaemonConn(t), "project.create", map[string]string{
		"root_path": "/repos/atct",
	})
	if resp.Error != "" {
		t.Fatalf("project.create: %s", resp.Error)
	}

	var project domain.Project
	if err := json.Unmarshal(resp.Result, &project); err != nil {
		t.Fatalf("unmarshal project: %v", err)
	}
	if project.Name != "atct" {
		t.Fatalf("name = %q, want %q", project.Name, "atct")
	}
	if project.RootPath != "/repos/atct" {
		t.Fatalf("root_path = %q, want %q", project.RootPath, "/repos/atct")
	}
}

func TestDaemonCreatesGoalForResolvedProject(t *testing.T) {
	conn := newDaemonConn(t)
	projectResp := call(t, conn, "project.create", map[string]string{
		"name":      "atct",
		"root_path": "/repos/atct",
	})
	if projectResp.Error != "" {
		t.Fatalf("project.create: %s", projectResp.Error)
	}
	var project domain.Project
	if err := json.Unmarshal(projectResp.Result, &project); err != nil {
		t.Fatalf("unmarshal project: %v", err)
	}

	goalResp := call(t, conn, "goal.create", map[string]string{
		"cwd":     "/repos/atct",
		"content": "Build the next release\n\nCoordinate the release work",
	})
	if goalResp.Error != "" {
		t.Fatalf("goal.create: %s", goalResp.Error)
	}
	var goal domain.Goal
	if err := json.Unmarshal(goalResp.Result, &goal); err != nil {
		t.Fatalf("unmarshal goal: %v", err)
	}
	if goal.ProjectID != project.ID {
		t.Fatalf("project_id = %q, want %q", goal.ProjectID, project.ID)
	}
	if domain.Headline(goal.Content) != "Build the next release" {
		t.Fatalf("headline = %q, want %q", domain.Headline(goal.Content), "Build the next release")
	}
	if domain.Body(goal.Content) != "Coordinate the release work" {
		t.Fatalf("body = %q, want %q", domain.Body(goal.Content), "Coordinate the release work")
	}
	if goal.Creator != "agent" || goal.Status != domain.GoalProposed {
		t.Fatalf("goal creator/status = %q/%q, want agent/proposed", goal.Creator, goal.Status)
	}
}

func TestDaemonSetsGoalDerivedFromAndDistinguishesErrors(t *testing.T) {
	conn := newDaemonConn(t)
	projectResp := call(t, conn, "project.create", map[string]string{
		"name": "atct", "root_path": "/repos/atct",
	})
	if projectResp.Error != "" {
		t.Fatalf("project.create: %s", projectResp.Error)
	}

	createGoal := func(content string) domain.Goal {
		t.Helper()
		resp := call(t, conn, "goal.create", map[string]string{
			"cwd": "/repos/atct", "content": content, "creator": "human",
		})
		if resp.Error != "" {
			t.Fatalf("goal.create: %s", resp.Error)
		}
		var goal domain.Goal
		if err := json.Unmarshal(resp.Result, &goal); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		return goal
	}
	parent := createGoal("Parent goal")
	child := createGoal("Child goal")

	set := call(t, conn, "goal.set_derived_from", map[string]string{
		"goal_id": child.ID, "derived_from_goal_id": parent.ID,
	})
	if set.Error != "" {
		t.Fatalf("goal.set_derived_from: %s", set.Error)
	}
	var updated domain.Goal
	if err := json.Unmarshal(set.Result, &updated); err != nil {
		t.Fatalf("unmarshal updated goal: %v", err)
	}
	if updated.DerivedFromGoalID != parent.ID {
		t.Fatalf("updated DerivedFromGoalID = %q, want %q", updated.DerivedFromGoalID, parent.ID)
	}

	unknown := call(t, conn, "goal.set_derived_from", map[string]string{
		"goal_id": child.ID, "derived_from_goal_id": "missing-goal-id",
	})
	if !strings.Contains(unknown.Error, "goal not found") {
		t.Fatalf("unknown parent error = %q, want goal not found", unknown.Error)
	}

	self := call(t, conn, "goal.set_derived_from", map[string]string{
		"goal_id": child.ID, "derived_from_goal_id": child.ID,
	})
	if !strings.Contains(self.Error, "cannot be derived from itself") {
		t.Fatalf("self-reference error = %q, want self-reference error", self.Error)
	}
	if unknown.Error == self.Error {
		t.Fatalf("unknown parent and self-reference errors are identical: %q", unknown.Error)
	}
}

func TestDecisionAskDistinguishesOmittedWaitFromExplicitZero(t *testing.T) {
	zeroConn := newDaemonConn(t)
	zeroGoalID, zeroTaskID := createDecisionFixture(t, zeroConn)
	if err := zeroConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set zero read deadline: %v", err)
	}
	started := time.Now()
	zeroResp := call(t, zeroConn, "decision.ask", map[string]any{
		"goal_id":          zeroGoalID,
		"task_id":          zeroTaskID,
		"question":         "Should the run continue?",
		"agent_session_id": "run-zero",
		"wait_ms":          0,
	})
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("explicit wait_ms=0 took %s; want an immediate parked response", elapsed)
	}
	if zeroResp.Error != "" {
		t.Fatalf("decision.ask with wait_ms=0: %s", zeroResp.Error)
	}
	var zeroResult struct {
		Parked     bool   `json:"parked"`
		DecisionID string `json:"decision_id"`
	}
	if err := json.Unmarshal(zeroResp.Result, &zeroResult); err != nil {
		t.Fatalf("unmarshal zero result: %v", err)
	}
	if !zeroResult.Parked || zeroResult.DecisionID == "" {
		t.Fatalf("explicit wait_ms=0 result = %+v, want parked decision", zeroResult)
	}

	omittedConn := newDaemonConn(t)
	omittedGoalID, omittedTaskID := createDecisionFixture(t, omittedConn)
	params, err := json.Marshal(map[string]any{
		"goal_id":          omittedGoalID,
		"task_id":          omittedTaskID,
		"question":         "Should the run continue?",
		"agent_session_id": "run-omitted",
	})
	if err != nil {
		t.Fatalf("marshal omitted params: %v", err)
	}
	req, err := json.Marshal(rpc.Request{Method: "decision.ask", Params: params})
	if err != nil {
		t.Fatalf("marshal omitted request: %v", err)
	}
	if _, err := omittedConn.Write(append(req, '\n')); err != nil {
		t.Fatalf("write omitted request: %v", err)
	}

	responses := make(chan rpc.Response, 1)
	readErrors := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(omittedConn).ReadBytes('\n')
		if err != nil {
			readErrors <- err
			return
		}
		var resp rpc.Response
		if err := json.Unmarshal(line, &resp); err != nil {
			readErrors <- err
			return
		}
		responses <- resp
	}()

	select {
	case resp := <-responses:
		t.Fatalf("omitted wait_ms returned immediately: %+v", resp)
	case err := <-readErrors:
		t.Fatalf("reading omitted wait_ms response before timeout: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	omittedConn.Close()
}

func createDecisionFixture(t *testing.T, conn net.Conn) (string, string) {
	t.Helper()
	projectResp := call(t, conn, "project.create", map[string]string{
		"name":      "atct",
		"root_path": "/repos/atct",
	})
	if projectResp.Error != "" {
		t.Fatalf("project.create: %s", projectResp.Error)
	}
	goalResp := call(t, conn, "goal.create", map[string]string{
		"cwd":     "/repos/atct",
		"content": "Wait semantics",
		"creator": "human",
	})
	if goalResp.Error != "" {
		t.Fatalf("goal.create: %s", goalResp.Error)
	}
	var goal domain.Goal
	if err := json.Unmarshal(goalResp.Result, &goal); err != nil {
		t.Fatalf("unmarshal goal: %v", err)
	}
	taskResp := call(t, conn, "task.declare", map[string]any{
		"goal_id":          goal.ID,
		"agent":            "test-agent",
		"idempotency_key":  "wait-semantics",
		"titles":           []string{"Wait for a decision"},
		"descriptions":     []string{"Complete the task after the decision is answered."},
		"agent_session_id": "fixture-run",
	})
	if taskResp.Error != "" {
		t.Fatalf("task.declare: %s", taskResp.Error)
	}
	var tasks []domain.Task
	if err := json.Unmarshal(taskResp.Result, &tasks); err != nil {
		t.Fatalf("unmarshal tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task.declare returned %d tasks, want 1", len(tasks))
	}
	return goal.ID, tasks[0].ID
}

type goalListFixture struct {
	store         *store.Store
	daemon        *Daemon
	project       domain.Project
	active        []domain.Goal
	proposed      []domain.Goal
	done          []domain.Goal
	dropped       []domain.Goal
	taskGoal      domain.Goal
	emptyTaskGoal domain.Goal
	doneOnlyGoal  domain.Goal
	tasks         []domain.Task
}

type goalListTaskItem struct {
	ID          string            `json:"id"`
	GoalID      string            `json:"goal_id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Status      domain.TaskStatus `json:"status"`
	ClaimedBy   string            `json:"claimed_by"`
	Order       int               `json:"order"`
}

type goalListItem struct {
	ID                string             `json:"id"`
	ProjectID         string             `json:"project_id"`
	DerivedFromGoalID string             `json:"derived_from_goal_id"`
	Content           string             `json:"content"`
	Status            domain.GoalStatus  `json:"status"`
	ClaimedBy         string             `json:"claimed_by"`
	CreatedAt         time.Time          `json:"created_at"`
	Tasks             []goalListTaskItem `json:"tasks"`
}

func newGoalListFixture(t *testing.T) goalListFixture {
	t.Helper()
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "goal-list")

	create := func(content, creator string, derivedFrom ...string) domain.Goal {
		goal, err := s.CreateGoal(ctx, project.ID, content, creator, derivedFrom...)
		if err != nil {
			t.Fatalf("CreateGoal(%q): %v", content, err)
		}
		return goal
	}
	mark := func(goal domain.Goal, status domain.GoalStatus) domain.Goal {
		_, err := s.DB().ExecContext(ctx, "UPDATE goals SET status = ?, work_done = ?, now_possible = ?, how_to_verify = ?, surprises = ?, needs_review = ?, next_steps = ?, result_summary = ? WHERE id = ?", string(status), "recorded work", "recorded now", "recorded verification", "recorded surprises", "recorded review", "recorded next steps", "recorded summary", goal.ID)
		if err != nil {
			t.Fatalf("mark goal %s as %s: %v", goal.ID, status, err)
		}
		goal.Status = status
		goal.WorkDone = "recorded work"
		goal.ResultSummary = "recorded summary"
		return goal
	}

	activeParent := create("active parent", "human")
	activeChild := create("active child", "human", activeParent.ID)
	taskGoal := create("task goal", "human")
	emptyTaskGoal := create("empty task goal", "human")
	doneOnlyGoal := create("done-only task goal", "human")
	proposedOne := create("proposed one", "agent")
	proposedTwo := create("proposed two", "agent")
	doneOne := mark(create("done one", "human"), domain.GoalDone)
	doneTwo := mark(create("done two", "human"), domain.GoalDone)
	droppedOne := mark(create("dropped one", "human"), domain.GoalDropped)
	droppedTwo := mark(create("dropped two", "human"), domain.GoalDropped)

	if _, err := s.DB().ExecContext(ctx, "UPDATE goals SET claimed_by = ? WHERE id = ?", "claimed-agent", activeChild.ID); err != nil {
		t.Fatalf("claim goal %s: %v", activeChild.ID, err)
	}
	activeChild.ClaimedBy = "claimed-agent"

	tasks, err := s.DeclareTasks(ctx, taskGoal.ID, "fixture-agent", "goal-list-tasks",
		[]string{"doing task", "todo task", "done task", "dropped task"},
		[]string{"doing description", "todo description", "done description", "dropped description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, "task-agent"); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if tasks[0], err = s.UpdateTask(ctx, tasks[0].ID, domain.TaskDoing, "task-agent"); err != nil {
		t.Fatalf("UpdateTask doing: %v", err)
	}
	if tasks[2], err = s.UpdateTask(ctx, tasks[2].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask done: %v", err)
	}
	if tasks[3], err = s.UpdateTask(ctx, tasks[3].ID, domain.TaskDropped, ""); err != nil {
		t.Fatalf("UpdateTask dropped: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, doneOnlyGoal.ID, "fixture-agent", "goal-list-done-only",
		[]string{"completed task"}, []string{"completed description"}); err != nil {
		t.Fatalf("DeclareTasks done-only: %v", err)
	}
	doneOnlyTasks, err := s.ListTasks(ctx, doneOnlyGoal.ID)
	if err != nil {
		t.Fatalf("ListTasks done-only: %v", err)
	}
	if len(doneOnlyTasks) != 1 {
		t.Fatalf("done-only goal has %d tasks, want 1", len(doneOnlyTasks))
	}
	if _, err := s.UpdateTask(ctx, doneOnlyTasks[0].ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask done-only: %v", err)
	}

	return goalListFixture{
		store:         s,
		daemon:        New(s),
		project:       project,
		active:        []domain.Goal{activeParent, activeChild, taskGoal, emptyTaskGoal, doneOnlyGoal},
		proposed:      []domain.Goal{proposedOne, proposedTwo},
		done:          []domain.Goal{doneOne, doneTwo},
		dropped:       []domain.Goal{droppedOne, droppedTwo},
		taskGoal:      taskGoal,
		emptyTaskGoal: emptyTaskGoal,
		doneOnlyGoal:  doneOnlyGoal,
		tasks:         tasks,
	}
}

func listGoalsForTest(t *testing.T, fixture goalListFixture) []json.RawMessage {
	t.Helper()
	params, err := json.Marshal(map[string]string{"cwd": fixture.project.RootPath})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response struct {
		Goals []json.RawMessage `json:"goals"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal goal.list response: %v", err)
	}
	return response.Goals
}

func TestGoalListOmitsDoneAndDroppedGoals(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	goals := listGoalsForTest(t, fixture)
	var items []goalListItem
	for _, raw := range goals {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		items = append(items, item)
	}

	for _, omitted := range append(fixture.done, fixture.dropped...) {
		for _, item := range items {
			if item.ID == omitted.ID {
				t.Fatalf("goal.list returned omitted goal %s with status %s", omitted.ID, omitted.Status)
			}
		}
	}
}

func TestGoalListOmitsCompletionDetails(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, raw := range listGoalsForTest(t, fixture) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("unmarshal goal fields: %v", err)
		}
		if _, ok := fields["work_done"]; ok {
			t.Fatalf("goal.list response unexpectedly contains work_done")
		}
		if _, ok := fields["result_summary"]; ok {
			t.Fatalf("goal.list response unexpectedly contains result_summary")
		}
	}
}

func TestGoalListKeepsActiveAndProposedGoals(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	got := make(map[string]bool)
	for _, raw := range listGoalsForTest(t, fixture) {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		got[item.ID] = true
	}
	for _, retained := range append(fixture.active, fixture.proposed...) {
		if !got[retained.ID] {
			t.Fatalf("goal.list omitted retained goal %s with status %s", retained.ID, retained.Status)
		}
	}
}

func TestGoalListKeepsGoalIdentityFields(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	var got goalListItem
	for _, raw := range listGoalsForTest(t, fixture) {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		if item.ID == fixture.active[1].ID {
			got = item
			break
		}
	}
	if got.ID != fixture.active[1].ID {
		t.Fatalf("goal.list omitted active child %s", fixture.active[1].ID)
	}
	if got.DerivedFromGoalID != fixture.active[0].ID {
		t.Fatalf("derived_from_goal_id = %q, want %q", got.DerivedFromGoalID, fixture.active[0].ID)
	}
	if got.Status != domain.GoalActive {
		t.Fatalf("status = %q, want %q", got.Status, domain.GoalActive)
	}
	if got.ClaimedBy != "claimed-agent" {
		t.Fatalf("claimed_by = %q, want %q", got.ClaimedBy, "claimed-agent")
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at is zero")
	}
}

func TestGoalListIncludesTodoAndDoingTasks(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	var got goalListItem
	for _, raw := range listGoalsForTest(t, fixture) {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		if item.ID == fixture.taskGoal.ID {
			got = item
			break
		}
	}
	if got.ID == "" {
		t.Fatalf("goal.list omitted task goal %s", fixture.taskGoal.ID)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("task goal has %d visible tasks, want 2", len(got.Tasks))
	}
	statuses := map[domain.TaskStatus]bool{}
	for _, task := range got.Tasks {
		statuses[task.Status] = true
	}
	for _, status := range []domain.TaskStatus{domain.TaskTodo, domain.TaskDoing} {
		if !statuses[status] {
			t.Fatalf("goal.list omitted visible task status %s", status)
		}
	}
}

func TestGoalListIncludesTaskFields(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	var rawGoal json.RawMessage
	for _, raw := range listGoalsForTest(t, fixture) {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		if item.ID == fixture.taskGoal.ID {
			rawGoal = raw
			break
		}
	}
	if rawGoal == nil {
		t.Fatalf("goal.list omitted task goal %s", fixture.taskGoal.ID)
	}

	var item goalListItem
	if err := json.Unmarshal(rawGoal, &item); err != nil {
		t.Fatalf("unmarshal task goal: %v", err)
	}
	want := map[string]domain.Task{
		fixture.tasks[0].ID: fixture.tasks[0],
		fixture.tasks[1].ID: fixture.tasks[1],
	}
	if len(item.Tasks) != len(want) {
		t.Fatalf("task goal has %d visible tasks, want %d", len(item.Tasks), len(want))
	}
	for _, got := range item.Tasks {
		wantTask, ok := want[got.ID]
		if !ok {
			t.Fatalf("unexpected visible task %s", got.ID)
		}
		if got.GoalID != wantTask.GoalID || got.Title != wantTask.Title || got.Description != wantTask.Description || got.Status != wantTask.Status || got.ClaimedBy != wantTask.ClaimedBy || got.Order != wantTask.Order {
			t.Fatalf("task %s = %+v, want fields from %+v", got.ID, got, wantTask)
		}
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawGoal, &fields); err != nil {
		t.Fatalf("unmarshal task goal fields: %v", err)
	}
	var taskFields []map[string]json.RawMessage
	if err := json.Unmarshal(fields["tasks"], &taskFields); err != nil {
		t.Fatalf("unmarshal task fields: %v", err)
	}
	wantKeys := map[string]bool{
		"id": true, "goal_id": true, "title": true, "description": true,
		"status": true, "claimed_by": true, "order": true,
	}
	for _, task := range taskFields {
		if len(task) != len(wantKeys) {
			t.Fatalf("task response has %d fields, want %d: %v", len(task), len(wantKeys), task)
		}
		for key := range task {
			if !wantKeys[key] {
				t.Fatalf("task response contains unexpected field %q", key)
			}
		}
		for key := range wantKeys {
			if _, ok := task[key]; !ok {
				t.Fatalf("task response omitted field %q", key)
			}
		}
	}
}

func TestGoalListSortsTasksByOrder(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, raw := range listGoalsForTest(t, fixture) {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		if item.ID != fixture.taskGoal.ID {
			continue
		}
		for i := 1; i < len(item.Tasks); i++ {
			if item.Tasks[i].Order < item.Tasks[i-1].Order {
				t.Fatalf("task order at index %d is %d after %d", i, item.Tasks[i].Order, item.Tasks[i-1].Order)
			}
		}
		return
	}
	t.Fatalf("goal.list omitted task goal %s", fixture.taskGoal.ID)
}

func TestGoalListOmitsDoneAndDroppedTasks(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, raw := range listGoalsForTest(t, fixture) {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		if item.ID != fixture.taskGoal.ID {
			continue
		}
		for _, task := range item.Tasks {
			if task.ID == fixture.tasks[2].ID || task.ID == fixture.tasks[3].ID {
				t.Fatalf("goal.list returned non-visible task %s with status %s", task.ID, task.Status)
			}
		}
		if len(item.Tasks) != 2 {
			t.Fatalf("task goal has %d visible tasks, want 2", len(item.Tasks))
		}
		return
	}
	t.Fatalf("goal.list omitted task goal %s", fixture.taskGoal.ID)
}

func TestGoalListKeepsGoalsWithEmptyTaskLists(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	got := make(map[string]goalListItem)
	for _, raw := range listGoalsForTest(t, fixture) {
		var item goalListItem
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("unmarshal goal: %v", err)
		}
		got[item.ID] = item
	}
	for _, goal := range []domain.Goal{fixture.emptyTaskGoal, fixture.doneOnlyGoal} {
		item, ok := got[goal.ID]
		if !ok {
			t.Fatalf("goal.list omitted goal %s", goal.ID)
		}
		if item.Tasks == nil {
			t.Fatalf("goal %s has nil tasks; want an empty array", goal.ID)
		}
		if len(item.Tasks) != 0 {
			t.Fatalf("goal %s has %d visible tasks, want 0", goal.ID, len(item.Tasks))
		}
	}
}

func TestGoalListIncludesBothDecisionResponseKeys(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	decision, err := fixture.store.AskDecision(context.Background(), store.AskInput{
		GoalID:         fixture.taskGoal.ID,
		TaskID:         fixture.tasks[0].ID,
		Kind:           domain.KindDecision,
		Question:       "Which task should proceed?",
		AgentSessionID: "fixture-agent",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := fixture.store.AnswerDecision(context.Background(), store.AnswerInput{
		DecisionID:  decision.ID,
		AnswerLabel: "proceed",
		AnswerText:  "Proceed with the task.",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"cwd":                       fixture.project.RootPath,
		"include_unapplied_answers": true,
	})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal goal.list response: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(response["data"], &data); err != nil {
		t.Fatalf("unmarshal goal.list data: %v", err)
	}
	if _, ok := data["orphaned_decisions"]; !ok {
		t.Fatal("goal.list omitted orphaned_decisions")
	}
	if _, ok := response["unapplied_decisions"]; !ok {
		t.Fatal("goal.list omitted unapplied_decisions")
	}
	var orphaned []json.RawMessage
	if err := json.Unmarshal(data["orphaned_decisions"], &orphaned); err != nil {
		t.Fatalf("unmarshal orphaned_decisions: %v", err)
	}
	if len(orphaned) == 0 {
		t.Fatal("orphaned_decisions is empty")
	}
	var unapplied []json.RawMessage
	if err := json.Unmarshal(response["unapplied_decisions"], &unapplied); err != nil {
		t.Fatalf("unmarshal unapplied_decisions: %v", err)
	}
	if len(unapplied) == 0 {
		t.Fatal("unapplied_decisions is empty")
	}
}

func registerLiveGoalClaimSession(t *testing.T, fixture goalListFixture, sessionID string) {
	t.Helper()
	if err := fixture.store.RegisterAgentSession(context.Background(), sessionID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession(%q): %v", sessionID, err)
	}
}

func claimGoalForTest(t *testing.T, fixture goalListFixture, goalID, sessionID string) (json.RawMessage, error) {
	t.Helper()
	params, err := json.Marshal(map[string]string{
		"goal_id":          goalID,
		"agent_session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("marshal goal.claim params: %v", err)
	}
	return fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.claim", Params: params})
}

func claimProjectForTest(t *testing.T, fixture goalListFixture, projectID, sessionID string) (json.RawMessage, error) {
	t.Helper()
	params, err := json.Marshal(map[string]string{
		"project_id":       projectID,
		"agent_session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("marshal project.claim params: %v", err)
	}
	return fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.claim", Params: params})
}

func updateGoalContentForTest(t *testing.T, fixture goalListFixture, goalID, content, sessionID string) (json.RawMessage, error) {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"goal_id":                   goalID,
		"content":                   content,
		"agent_session_id":          sessionID,
		"include_unapplied_answers": false,
	})
	if err != nil {
		t.Fatalf("marshal goal.update_content params: %v", err)
	}
	return fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.update_content", Params: params})
}

func listGoalForClaimTest(t *testing.T, fixture goalListFixture, goalID, sessionID string) goalListItem {
	t.Helper()
	params, err := json.Marshal(map[string]string{
		"cwd":              fixture.project.RootPath,
		"agent_session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response struct {
		Goals []goalListItem `json:"goals"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal goal.list response: %v", err)
	}
	for _, goal := range response.Goals {
		if goal.ID == goalID {
			return goal
		}
	}
	t.Fatalf("goal.list omitted goal %s", goalID)
	return goalListItem{}
}

func TestGoalUpdateContentRewritesProposedGoal(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const content = "rewritten proposed goal"
	result, err := updateGoalContentForTest(t, fixture, fixture.proposed[0].ID, content, "goal-update-content-run")
	if err != nil {
		t.Fatalf("goal.update_content: %v", err)
	}
	var updated domain.Goal
	if err := json.Unmarshal(result, &updated); err != nil {
		t.Fatalf("unmarshal goal.update_content result: %v", err)
	}
	if updated.Content != content {
		t.Fatalf("content = %q, want %q", updated.Content, content)
	}
	if updated.Status != domain.GoalProposed {
		t.Fatalf("status = %q, want %q", updated.Status, domain.GoalProposed)
	}
}

func TestGoalUpdateContentRejectsActiveGoal(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	if _, err := updateGoalContentForTest(t, fixture, fixture.active[0].ID, "must be rejected", "goal-update-active-run"); !errors.Is(err, ErrGoalNotProposed) {
		t.Fatalf("goal.update_content error = %v, want ErrGoalNotProposed", err)
	}
}

func TestGoalUpdateContentRejectsDoneAndDroppedGoals(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, tc := range []struct {
		name string
		goal domain.Goal
	}{
		{name: "done", goal: fixture.done[0]},
		{name: "dropped", goal: fixture.dropped[0]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := updateGoalContentForTest(t, fixture, tc.goal.ID, "must be rejected", "goal-update-terminal-run"); !errors.Is(err, ErrGoalNotProposed) {
				t.Fatalf("goal.update_content error = %v, want ErrGoalNotProposed", err)
			}
		})
	}
}

func TestGoalUpdateContentKeepsRejectedGoalUnchanged(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, tc := range []struct {
		name string
		goal domain.Goal
	}{
		{name: "active", goal: fixture.active[0]},
		{name: "done", goal: fixture.done[0]},
		{name: "dropped", goal: fixture.dropped[0]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := updateGoalContentForTest(t, fixture, tc.goal.ID, "must not replace original", "goal-update-unchanged-run"); !errors.Is(err, ErrGoalNotProposed) {
				t.Fatalf("goal.update_content error = %v, want ErrGoalNotProposed", err)
			}
			got, err := fixture.store.GetGoal(context.Background(), tc.goal.ID)
			if err != nil {
				t.Fatalf("GetGoal after rejected update: %v", err)
			}
			if got.Content != tc.goal.Content {
				t.Fatalf("content after rejected update = %q, want %q", got.Content, tc.goal.Content)
			}
		})
	}
}

func TestGoalClaimSetsClaimedBy(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionID = "goal-claim-run"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	result, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, sessionID)
	if err != nil {
		t.Fatalf("goal.claim: %v", err)
	}
	var claimed domain.Goal
	if err := json.Unmarshal(result, &claimed); err != nil {
		t.Fatalf("unmarshal goal.claim result: %v", err)
	}
	if claimed.ClaimedBy != sessionID {
		t.Fatalf("claimed_by = %q, want %q", claimed.ClaimedBy, sessionID)
	}
}

func TestGoalClaimAppearsInGoalList(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionID = "goal-list-after-claim-run"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, sessionID); err != nil {
		t.Fatalf("goal.claim: %v", err)
	}
	listed := listGoalForClaimTest(t, fixture, fixture.emptyTaskGoal.ID, sessionID)
	if listed.ClaimedBy != sessionID {
		t.Fatalf("goal.list claimed_by = %q, want %q", listed.ClaimedBy, sessionID)
	}
}

func TestGoalClaimRejectsLiveOtherSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "goal-other-run")
	registerLiveGoalClaimSession(t, fixture, "goal-owner-run")
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-owner-run"); err != nil {
		t.Fatalf("initial goal.claim: %v", err)
	}
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-other-run"); !errors.Is(err, ErrGoalAlreadyClaimed) {
		t.Fatalf("second goal.claim error = %v, want ErrGoalAlreadyClaimed", err)
	}
}

func TestGoalClaimRejectsLiveOtherSessionWithOwnerRegisteredFirst(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "goal-owner-first-run")
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := fixture.store.DB().ExecContext(context.Background(), `UPDATE agent_sessions SET registered_at = ? WHERE id = ?`, old, "goal-owner-first-run"); err != nil {
		t.Fatalf("age owner session: %v", err)
	}
	registerLiveGoalClaimSession(t, fixture, "goal-other-second-run")
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-owner-first-run"); err != nil {
		t.Fatalf("initial goal.claim: %v", err)
	}
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-other-second-run"); !errors.Is(err, ErrGoalAlreadyClaimed) {
		t.Fatalf("second goal.claim error = %v, want ErrGoalAlreadyClaimed", err)
	}
	claimed, err := fixture.store.GetGoal(context.Background(), fixture.emptyTaskGoal.ID)
	if err != nil {
		t.Fatalf("GetGoal after rejected claim: %v", err)
	}
	if claimed.ClaimedBy != "goal-owner-first-run" {
		t.Fatalf("claimed_by after rejected claim = %q, want %q", claimed.ClaimedBy, "goal-owner-first-run")
	}
}

func TestGoalClaimKeepsOwnerAfterRejection(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "goal-rejected-run")
	registerLiveGoalClaimSession(t, fixture, "goal-owner-unchanged-run")
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-owner-unchanged-run"); err != nil {
		t.Fatalf("initial goal.claim: %v", err)
	}
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-rejected-run"); !errors.Is(err, ErrGoalAlreadyClaimed) {
		t.Fatalf("second goal.claim error = %v, want ErrGoalAlreadyClaimed", err)
	}
	claimed, err := fixture.store.GetGoal(context.Background(), fixture.emptyTaskGoal.ID)
	if err != nil {
		t.Fatalf("GetGoal after rejected claim: %v", err)
	}
	if claimed.ClaimedBy != "goal-owner-unchanged-run" {
		t.Fatalf("claimed_by after rejected claim = %q, want %q", claimed.ClaimedBy, "goal-owner-unchanged-run")
	}
}

func TestGoalClaimMissingGoalReturnsNotFound(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	if _, err := claimGoalForTest(t, fixture, "missing-goal-id", "missing-goal-run"); !errors.Is(err, store.ErrGoalNotFound) {
		t.Fatalf("goal.claim error = %v, want store.ErrGoalNotFound", err)
	}
}

func TestProjectClaimSetsClaimedBy(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionID = "project-claim-run"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	result, err := claimProjectForTest(t, fixture, fixture.project.ID, sessionID)
	if err != nil {
		t.Fatalf("project.claim: %v", err)
	}
	var claimed struct {
		ClaimedBy string `json:"claimed_by"`
	}
	if err := json.Unmarshal(result, &claimed); err != nil {
		t.Fatalf("unmarshal project.claim result: %v", err)
	}
	if claimed.ClaimedBy != sessionID {
		t.Fatalf("claimed_by = %q, want %q", claimed.ClaimedBy, sessionID)
	}
}

func TestProjectClaimRejectsLiveOtherSessionViaRPC(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "project-reject-other-run")
	registerLiveGoalClaimSession(t, fixture, "project-reject-owner-run")
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "project-reject-owner-run"); err != nil {
		t.Fatalf("initial project.claim: %v", err)
	}
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "project-reject-other-run"); !errors.Is(err, ErrProjectAlreadyClaimed) {
		t.Fatalf("second project.claim error = %v, want ErrProjectAlreadyClaimed", err)
	}
}

func TestProjectReleaseClearsClaimViaRPC(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionID = "project-release-run"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, sessionID); err != nil {
		t.Fatalf("project.claim: %v", err)
	}
	params, err := json.Marshal(map[string]string{"project_id": fixture.project.ID})
	if err != nil {
		t.Fatalf("marshal project.release params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.release", Params: params}); err != nil {
		t.Fatalf("project.release: %v", err)
	}

	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.list", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("project.list after release: %v", err)
	}
	var projects []struct {
		ID        string `json:"id"`
		ClaimedBy string `json:"claimed_by"`
	}
	if err := json.Unmarshal(result, &projects); err != nil {
		t.Fatalf("unmarshal project.list result: %v", err)
	}
	for _, project := range projects {
		if project.ID == fixture.project.ID {
			if project.ClaimedBy != "" {
				t.Fatalf("claimed_by after project.release = %q, want empty", project.ClaimedBy)
			}
			return
		}
	}
	t.Fatalf("project.list omitted project %s", fixture.project.ID)
}

func TestGoalReleaseClearsClaimViaRPC(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionID = "goal-release-run"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, sessionID); err != nil {
		t.Fatalf("goal.claim: %v", err)
	}
	params, err := json.Marshal(map[string]string{"goal_id": fixture.emptyTaskGoal.ID})
	if err != nil {
		t.Fatalf("marshal goal.release params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.release", Params: params}); err != nil {
		t.Fatalf("goal.release: %v", err)
	}

	listed := listGoalForClaimTest(t, fixture, fixture.emptyTaskGoal.ID, sessionID)
	if listed.ClaimedBy != "" {
		t.Fatalf("goal.list claimed_by after goal.release = %q, want empty", listed.ClaimedBy)
	}
}

func TestGoalClaimRejectsLiveOtherSessionAfterProjectClaim(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "goal-project-owner-run")
	registerLiveGoalClaimSession(t, fixture, "goal-project-other-run")
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "goal-project-owner-run"); err != nil {
		t.Fatalf("project.claim: %v", err)
	}
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-project-owner-run"); err != nil {
		t.Fatalf("initial goal.claim: %v", err)
	}
	if _, err := claimGoalForTest(t, fixture, fixture.emptyTaskGoal.ID, "goal-project-other-run"); !errors.Is(err, ErrGoalAlreadyClaimed) {
		t.Fatalf("second goal.claim error = %v, want ErrGoalAlreadyClaimed", err)
	}
}

func TestTaskClaimStillClaimsTask(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionID = "task-claim-regression-run"
	result, err := func() (json.RawMessage, error) {
		params, err := json.Marshal(map[string]string{
			"task_id":          fixture.tasks[1].ID,
			"agent_session_id": sessionID,
		})
		if err != nil {
			t.Fatalf("marshal task.claim params: %v", err)
		}
		return fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.claim", Params: params})
	}()
	if err != nil {
		t.Fatalf("task.claim: %v", err)
	}
	var claimed domain.Task
	if err := json.Unmarshal(result, &claimed); err != nil {
		t.Fatalf("unmarshal task.claim result: %v", err)
	}
	if claimed.ClaimedBy != sessionID {
		t.Fatalf("task claimed_by = %q, want %q", claimed.ClaimedBy, sessionID)
	}
}

func TestTaskClaimAndReleaseStillWork(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionID = "task-claim-release-regression-run"
	claimParams, err := json.Marshal(map[string]string{
		"task_id":          fixture.tasks[1].ID,
		"agent_session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("marshal task.claim params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.claim", Params: claimParams}); err != nil {
		t.Fatalf("task.claim: %v", err)
	}

	releaseParams, err := json.Marshal(map[string]string{"task_id": fixture.tasks[1].ID})
	if err != nil {
		t.Fatalf("marshal task.release params: %v", err)
	}
	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.release", Params: releaseParams})
	if err != nil {
		t.Fatalf("task.release: %v", err)
	}
	var released domain.Task
	if err := json.Unmarshal(result, &released); err != nil {
		t.Fatalf("unmarshal task.release result: %v", err)
	}
	if released.ClaimedBy != "" {
		t.Fatalf("task claimed_by after task.release = %q, want empty", released.ClaimedBy)
	}
}

func TestReleaseMissingIDsReturnErrorsViaRPC(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, tc := range []struct {
		method string
		key    string
	}{
		{method: "project.release", key: "project_id"},
		{method: "goal.release", key: "goal_id"},
		{method: "task.release", key: "task_id"},
	} {
		params, err := json.Marshal(map[string]string{tc.key: "missing-" + tc.key})
		if err != nil {
			t.Fatalf("marshal %s params: %v", tc.method, err)
		}
		if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: tc.method, Params: params}); err == nil {
			t.Errorf("%s returned nil error for missing ID", tc.method)
		}
	}
}
