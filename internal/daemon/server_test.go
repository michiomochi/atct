package daemon

import (
	"bufio"
	"context"
	"encoding/json"
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
