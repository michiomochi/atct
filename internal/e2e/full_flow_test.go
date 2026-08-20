package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

const e2eRoot = "/workspace/atct"

type e2eStack struct {
	dir      string
	db       *store.Store
	socket   string
	agent    *mcpshim.Client
	server   *httptest.Server
	cancel   context.CancelFunc
	serveErr chan error
}

type e2eGoalList struct {
	Project           domain.Project    `json:"project"`
	Goals             []domain.Goal     `json:"goals"`
	AnsweredDecisions []domain.Decision `json:"answered_decisions"`
	OrphanedDecisions []domain.Decision `json:"orphaned_decisions"`
}

type e2eInbox struct {
	OpenDecisions      []domain.Decision `json:"open_decisions"`
	UnappliedDecisions []domain.Decision `json:"unapplied_decisions"`
	ActiveGoals        []domain.Goal     `json:"active_goals"`
	AttentionTasks     []json.RawMessage `json:"attention_tasks"`
}

type e2eGoalDetail struct {
	Goal          domain.Goal       `json:"goal"`
	Now           []json.RawMessage `json:"now"`
	NeedsDecision []json.RawMessage `json:"needs_decision"`
	Next          []json.RawMessage `json:"next"`
}

type parkedDecision struct {
	Parked     bool   `json:"parked"`
	DecisionID string `json:"decision_id"`
}

type sseFrame struct {
	event string
	data  string
}

func newE2EStack(t *testing.T) *e2eStack {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct-e2e")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("store.Open: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stack := &e2eStack{
		dir:      dir,
		db:       db,
		socket:   filepath.Join(dir, "atct.sock"),
		agent:    mcpshim.NewClient(filepath.Join(dir, "atct.sock")),
		cancel:   cancel,
		serveErr: make(chan error, 1),
	}
	go func() {
		stack.serveErr <- daemon.New(db).Serve(ctx, stack.socket)
	}()

	t.Cleanup(func() {
		cancel()
		if stack.server != nil {
			stack.server.CloseClientConnections()
			stack.server.Close()
		}
		select {
		case err := <-stack.serveErr:
			if err != nil {
				t.Errorf("daemon Serve: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("daemon did not stop")
		}
		if err := db.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove temp directory: %v", err)
		}
	})

	waitForUnixSocket(t, stack.socket)
	stack.server = httptest.NewServer(httpapi.New(db).Handler())
	return stack
}

func waitForUnixSocket(t *testing.T, socket string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon socket %s did not become available", socket)
}

func (s *e2eStack) call(method string, params any, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.agent.Call(ctx, method, params, out)
}

func callDaemon(t *testing.T, s *e2eStack, method string, params any, out any) {
	t.Helper()
	if err := s.call(method, params, out); err != nil {
		t.Fatalf("daemon %s: %v", method, err)
	}
}

func createProject(t *testing.T, s *e2eStack) domain.Project {
	t.Helper()
	return createProjectAt(t, s, "atct", e2eRoot)
}

func createProjectAt(t *testing.T, s *e2eStack, name, rootPath string) domain.Project {
	t.Helper()
	var project domain.Project
	callDaemon(t, s, "project.create", map[string]any{
		"name":      name,
		"root_path": rootPath,
	}, &project)
	if project.ID == "" || project.RootPath != rootPath {
		t.Fatalf("project.create returned %+v", project)
	}
	return project
}

func createGoal(t *testing.T, s *e2eStack) domain.Goal {
	t.Helper()
	return createGoalAt(t, s, e2eRoot)
}

func createGoalAt(t *testing.T, s *e2eStack, cwd string) domain.Goal {
	t.Helper()
	var goal domain.Goal
	callDaemon(t, s, "goal.create", map[string]any{
		"cwd":         cwd,
		"title":       "Complete the end-to-end flow",
		"description": "Verify the daemon and human-facing routes together",
		"creator":     "human",
	}, &goal)
	if goal.ID == "" || goal.Status != domain.GoalActive {
		t.Fatalf("goal.create returned %+v", goal)
	}
	return goal
}

func declareTasks(t *testing.T, s *e2eStack, goalID string, titles []string) []domain.Task {
	t.Helper()
	var tasks []domain.Task
	descriptions := make([]string, len(titles))
	for i, title := range titles {
		descriptions[i] = "Complete the task titled " + title + " and verify its result."
	}
	callDaemon(t, s, "task.declare", map[string]any{
		"goal_id":         goalID,
		"agent":           "e2e-agent",
		"idempotency_key": "e2e-declaration",
		"titles":          titles,
		"descriptions":    descriptions,
	}, &tasks)
	if len(tasks) != len(titles) {
		t.Fatalf("task.declare returned %d tasks, want %d", len(tasks), len(titles))
	}
	return tasks
}

func askParked(t *testing.T, s *e2eStack, goalID, taskID, agentSessionID string) parkedDecision {
	t.Helper()
	var result parkedDecision
	callDaemon(t, s, "decision.ask", map[string]any{
		"goal_id":  goalID,
		"task_id":  taskID,
		"question": "Should the agent continue with this task?",
		"options": []domain.Option{{
			Label: "continue", Description: "Continue the task", Consequence: "The run proceeds",
		}},
		"agent_session_id": agentSessionID,
		"wait_ms":          0,
	}, &result)
	if !result.Parked || result.DecisionID == "" {
		t.Fatalf("decision.ask returned %+v, want parked decision", result)
	}
	return result
}

func httpJSON(t *testing.T, s *e2eStack, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal HTTP body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new HTTP request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatalf("HTTP %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read HTTP %s %s: %v", method, path, err)
	}
	return resp.StatusCode, raw
}

func openSSE(t *testing.T, s *e2eStack) (*http.Response, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.server.URL+"/api/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("new SSE request: %v", err)
	}
	resp, err := s.server.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("SSE status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	return resp, bufio.NewReader(resp.Body), cancel
}

func readSSEFrame(reader *bufio.Reader) (sseFrame, error) {
	var frame sseFrame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return sseFrame{}, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case line == "":
			if frame.event != "" || frame.data != "" {
				return frame, nil
			}
		case strings.HasPrefix(line, "event: "):
			frame.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			frame.data += strings.TrimPrefix(line, "data: ")
		}
	}
}

func nextSSEFrame(reader *bufio.Reader) (sseFrame, error) {
	result := make(chan struct {
		frame sseFrame
		err   error
	}, 1)
	go func() {
		frame, err := readSSEFrame(reader)
		result <- struct {
			frame sseFrame
			err   error
		}{frame, err}
	}()
	select {
	case value := <-result:
		return value.frame, value.err
	case <-time.After(3 * time.Second):
		return sseFrame{}, fmt.Errorf("timed out waiting for SSE event")
	}
}

func decodeJSON(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decode JSON %q: %v", raw, err)
	}
}

func containsDecision(decisions []domain.Decision, id string) bool {
	for _, decision := range decisions {
		if decision.ID == id {
			return true
		}
	}
	return false
}

func TestFullFlowThroughDaemonAndHTTP(t *testing.T) {
	stack := newE2EStack(t)
	project := createProject(t, stack)
	// human として作る。agent の提案と承認の経路は goal_approval_test.go が覆う
	goal := createGoal(t, stack)
	if goal.ProjectID != project.ID {
		t.Fatalf("goal project_id = %q, want %q", goal.ProjectID, project.ID)
	}

	var listed e2eGoalList
	callDaemon(t, stack, "goal.list", map[string]any{
		"cwd": e2eRoot, "agent_session_id": "flow-run",
	}, &listed)
	if listed.Project.ID != project.ID || len(listed.Goals) != 1 || listed.Goals[0].ID != goal.ID {
		t.Fatalf("goal.list returned %+v", listed)
	}

	tasks := declareTasks(t, stack, goal.ID, []string{"Prepare the run", "Resolve the question", "Finish the goal"})
	agentSessionID := "flow-run"
	var claimed domain.Task
	callDaemon(t, stack, "task.claim", map[string]any{
		"task_id": tasks[0].ID, "agent_session_id": agentSessionID,
	}, &claimed)
	if claimed.ClaimedBy != agentSessionID || claimed.ClaimedAt == nil {
		t.Fatalf("task.claim returned %+v", claimed)
	}

	parked := askParked(t, stack, goal.ID, tasks[1].ID, agentSessionID)
	status, raw := httpJSON(t, stack, http.MethodGet, "/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/inbox status = %d, body %s", status, raw)
	}
	var inbox e2eInbox
	decodeJSON(t, raw, &inbox)
	if !containsDecision(inbox.OpenDecisions, parked.DecisionID) {
		t.Fatalf("open_decisions does not contain parked decision: %+v", inbox.OpenDecisions)
	}
	if !containsGoal(inbox.ActiveGoals, goal.ID) {
		t.Fatalf("active_goals does not contain goal: %+v", inbox.ActiveGoals)
	}

	status, raw = httpJSON(t, stack, http.MethodPost, "/api/decisions/"+parked.DecisionID+"/answer", map[string]string{
		"answer_label": "continue",
		"answer_text":  "Continue after reviewing the question",
	})
	if status != http.StatusOK {
		t.Fatalf("POST answer status = %d, body %s", status, raw)
	}
	var answered domain.Decision
	decodeJSON(t, raw, &answered)
	if answered.ID != parked.DecisionID || answered.Status != domain.DecisionAnswered {
		t.Fatalf("answer response = %+v", answered)
	}

	var applied []domain.Decision
	callDaemon(t, stack, "decision.poll", map[string]any{
		"agent_session_id": agentSessionID, "decision_id": parked.DecisionID,
	}, &applied)
	if len(applied) != 1 || applied[0].ID != parked.DecisionID || applied[0].Status != domain.DecisionApplied {
		t.Fatalf("decision.poll returned %+v", applied)
	}

	for _, task := range tasks {
		var updated domain.Task
		callDaemon(t, stack, "task.update", map[string]any{
			"task_id": task.ID, "status": string(domain.TaskDone), "agent_session_id": agentSessionID,
		}, &updated)
		if updated.Status != domain.TaskDone {
			t.Fatalf("task.update returned %+v", updated)
		}
		if task.ID == claimed.ID && updated.ClaimedBy != "" {
			t.Fatalf("completed claimed task still held: %+v", updated)
		}
	}

	var completion domain.Decision
	callDaemon(t, stack, "goal.complete", map[string]any{
		"goal_id": goal.ID, "work_done": "The flow completed",
		"now_possible":  "The goal can be approved",
		"how_to_verify": "Check the completion response",
		"surprises":     "なし", "needs_review": "なし", "next_steps": "なし",
		"agent_session_id": agentSessionID,
	}, &completion)
	if completion.Kind != domain.KindCompletion || completion.Status != domain.DecisionOpen {
		t.Fatalf("goal.complete returned %+v", completion)
	}

	status, raw = httpJSON(t, stack, http.MethodGet, "/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/inbox for completion status = %d, body %s", status, raw)
	}
	decodeJSON(t, raw, &inbox)
	if !containsDecision(inbox.OpenDecisions, completion.ID) {
		t.Fatalf("completion decision missing from inbox: %+v", inbox.OpenDecisions)
	}

	response, reader, cancelSSE := openSSE(t, stack)
	defer response.Body.Close()
	defer cancelSSE()
	status, raw = httpJSON(t, stack, http.MethodPost, "/api/decisions/"+completion.ID+"/approve", map[string]string{})
	if status != http.StatusOK {
		t.Fatalf("POST approve status = %d, body %s", status, raw)
	}
	var doneGoal domain.Goal
	decodeJSON(t, raw, &doneGoal)
	if doneGoal.ID != goal.ID || doneGoal.Status != domain.GoalDone {
		t.Fatalf("approve response = %+v", doneGoal)
	}
	frame, err := nextSSEFrame(reader)
	if err != nil {
		t.Fatalf("read approval SSE: %v", err)
	}
	if frame.event != "decision.approved" {
		t.Fatalf("approval SSE event = %q, want decision.approved", frame.event)
	}
	var approved domain.Decision
	decodeJSON(t, []byte(frame.data), &approved)
	if approved.ID != completion.ID || approved.Status != domain.DecisionApplied {
		t.Fatalf("approval SSE data = %+v", approved)
	}

	status, raw = httpJSON(t, stack, http.MethodGet, "/api/goals/"+goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET goal detail status = %d, body %s", status, raw)
	}
	var detail e2eGoalDetail
	decodeJSON(t, raw, &detail)
	if detail.Goal.Status != domain.GoalDone || len(detail.Now) != 0 || len(detail.NeedsDecision) != 0 || len(detail.Next) != 0 {
		t.Fatalf("final goal detail = %+v", detail)
	}
	status, raw = httpJSON(t, stack, http.MethodGet, "/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("final GET /api/inbox status = %d, body %s", status, raw)
	}
	decodeJSON(t, raw, &inbox)
	if containsDecision(inbox.OpenDecisions, completion.ID) || containsDecision(inbox.UnappliedDecisions, completion.ID) {
		t.Fatalf("completion decision remains actionable: %+v", inbox)
	}
}

func TestOpenDecisionBlocksTaskDoneThroughDaemon(t *testing.T) {
	stack := newE2EStack(t)
	createProject(t, stack)
	// human として作る。agent の提案と承認の経路は goal_approval_test.go が覆う
	goal := createGoal(t, stack)
	tasks := declareTasks(t, stack, goal.ID, []string{"Blocked task"})
	askParked(t, stack, goal.ID, tasks[0].ID, "blocked-run")

	var updated domain.Task
	err := stack.call("task.update", map[string]any{
		"task_id": tasks[0].ID, "status": string(domain.TaskDone),
	}, &updated)
	if err == nil || !strings.Contains(err.Error(), "task has an open decision") {
		t.Fatalf("task.update with open decision error = %v, want task has an open decision", err)
	}

	status, raw := httpJSON(t, stack, http.MethodGet, "/api/goals/"+goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET goal detail status = %d, body %s", status, raw)
	}
	var detail e2eGoalDetail
	decodeJSON(t, raw, &detail)
	if len(detail.NeedsDecision) != 1 || len(detail.Now) != 0 || len(detail.Next) != 0 {
		t.Fatalf("goal detail columns = now %d needs_decision %d next %d", len(detail.Now), len(detail.NeedsDecision), len(detail.Next))
	}
	if !containsRawID(detail.NeedsDecision, tasks[0].ID) {
		t.Fatalf("needs_decision does not contain task %s", tasks[0].ID)
	}
}

func TestAnsweredDecisionAppearsUnappliedInInbox(t *testing.T) {
	stack := newE2EStack(t)
	createProject(t, stack)
	// human として作る。agent の提案と承認の経路は goal_approval_test.go が覆う
	goal := createGoal(t, stack)
	tasks := declareTasks(t, stack, goal.ID, []string{"Awaiting answer"})
	decision := askParked(t, stack, goal.ID, tasks[0].ID, "unapplied-run")

	status, raw := httpJSON(t, stack, http.MethodPost, "/api/decisions/"+decision.DecisionID+"/answer", map[string]string{
		"answer_label": "continue",
		"answer_text":  "The answer is ready for the agent",
	})
	if status != http.StatusOK {
		t.Fatalf("POST answer status = %d, body %s", status, raw)
	}

	status, raw = httpJSON(t, stack, http.MethodGet, "/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/inbox status = %d, body %s", status, raw)
	}
	var inbox e2eInbox
	decodeJSON(t, raw, &inbox)
	if !containsDecision(inbox.UnappliedDecisions, decision.DecisionID) {
		t.Fatalf("unapplied_decisions does not contain answered decision: %+v", inbox.UnappliedDecisions)
	}
	if containsDecision(inbox.OpenDecisions, decision.DecisionID) {
		t.Fatalf("answered decision still appears open: %+v", inbox.OpenDecisions)
	}
}

func TestOnlyOneAgentSessionClaimsTaskThroughDaemon(t *testing.T) {
	stack := newE2EStack(t)
	createProject(t, stack)
	// human として作る。agent の提案と承認の経路は goal_approval_test.go が覆う
	goal := createGoal(t, stack)
	tasks := declareTasks(t, stack, goal.ID, []string{"Competing task"})

	type claimResult struct {
		agentSessionID string
		err            error
		task           domain.Task
	}
	results := make(chan claimResult, 2)
	var group sync.WaitGroup
	for _, agentSessionID := range []string{"claim-run-a", "claim-run-b"} {
		group.Add(1)
		go func(agentSessionID string) {
			defer group.Done()
			var task domain.Task
			err := stack.call("task.claim", map[string]any{
				"task_id": tasks[0].ID, "agent_session_id": agentSessionID,
			}, &task)
			results <- claimResult{agentSessionID: agentSessionID, err: err, task: task}
		}(agentSessionID)
	}
	group.Wait()
	close(results)

	winners := 0
	losers := 0
	var winner claimResult
	for result := range results {
		if result.err == nil {
			winners++
			winner = result
		} else {
			losers++
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("claim results: winners=%d losers=%d, want one each", winners, losers)
	}
	if winner.task.ClaimedBy != winner.agentSessionID {
		t.Fatalf("winning claim = %+v", winner.task)
	}
}

func TestSSEPublishesAnsweredDecision(t *testing.T) {
	stack := newE2EStack(t)
	createProject(t, stack)
	goal := createGoal(t, stack)
	tasks := declareTasks(t, stack, goal.ID, []string{"Wait for human"})
	decision := askParked(t, stack, goal.ID, tasks[0].ID, "sse-run")
	response, reader, cancelSSE := openSSE(t, stack)
	defer response.Body.Close()
	defer cancelSSE()

	status, raw := httpJSON(t, stack, http.MethodPost, "/api/decisions/"+decision.DecisionID+"/answer", map[string]string{
		"answer_label": "continue",
		"answer_text":  "SSE should carry this answer",
	})
	if status != http.StatusOK {
		t.Fatalf("POST answer status = %d, body %s", status, raw)
	}
	frame, err := nextSSEFrame(reader)
	if err != nil {
		t.Fatalf("read answer SSE: %v", err)
	}
	if frame.event != "decision.answered" {
		t.Fatalf("answer SSE event = %q, want decision.answered", frame.event)
	}
	var answered domain.Decision
	decodeJSON(t, []byte(frame.data), &answered)
	if answered.ID != decision.DecisionID || answered.Status != domain.DecisionAnswered || answered.AnswerText != "SSE should carry this answer" {
		t.Fatalf("answer SSE data = %+v", answered)
	}
}

func TestSessionCanAdoptAnsweredDecision(t *testing.T) {
	stack := newE2EStack(t)
	createProject(t, stack)
	goal := createGoal(t, stack)
	tasks := declareTasks(t, stack, goal.ID, []string{"Adopt the answered decision"})
	decision := askParked(t, stack, goal.ID, tasks[0].ID, "run-a")

	status, raw := httpJSON(t, stack, http.MethodPost, "/api/decisions/"+decision.DecisionID+"/answer", map[string]string{
		"answer_label": "continue",
		"answer_text":  "Continue in the new session",
	})
	if status != http.StatusOK {
		t.Fatalf("POST answer status = %d, body %s", status, raw)
	}
	var answered domain.Decision
	decodeJSON(t, raw, &answered)
	if answered.ID != decision.DecisionID || answered.Status != domain.DecisionAnswered {
		t.Fatalf("answer response = %+v", answered)
	}

	var listed e2eGoalList
	callDaemon(t, stack, "goal.list", map[string]any{
		"cwd": e2eRoot, "agent_session_id": "run-b",
	}, &listed)
	if !containsDecision(listed.OrphanedDecisions, decision.DecisionID) {
		t.Fatalf("orphaned_decisions does not contain answered decision: %+v", listed.OrphanedDecisions)
	}

	var applied []domain.Decision
	callDaemon(t, stack, "decision.poll", map[string]any{
		"agent_session_id": "run-b", "decision_id": decision.DecisionID,
	}, &applied)
	if len(applied) != 1 || applied[0].ID != decision.DecisionID || applied[0].Status != domain.DecisionApplied {
		t.Fatalf("decision.poll returned %+v", applied)
	}
	if applied[0].AgentSessionID != "run-a" {
		t.Fatalf("adopted decision agent_session_id = %q, want run-a", applied[0].AgentSessionID)
	}

	status, raw = httpJSON(t, stack, http.MethodGet, "/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/inbox status = %d, body %s", status, raw)
	}
	var inbox e2eInbox
	decodeJSON(t, raw, &inbox)
	if len(inbox.UnappliedDecisions) != 0 {
		t.Fatalf("unapplied_decisions = %+v, want empty after adoption", inbox.UnappliedDecisions)
	}
}

func TestGoalListScopesDecisionsToProject(t *testing.T) {
	stack := newE2EStack(t)
	project := createProject(t, stack)
	otherProject := createProjectAt(t, stack, "other", "/workspace/other")
	goal := createGoal(t, stack)
	otherGoal := createGoalAt(t, stack, otherProject.RootPath)
	if goal.ProjectID != project.ID || otherGoal.ProjectID != otherProject.ID {
		t.Fatalf("goal project IDs = %q and %q, want %q and %q", goal.ProjectID, otherGoal.ProjectID, project.ID, otherProject.ID)
	}
	tasks := declareTasks(t, stack, goal.ID, []string{"Adopt the answered decision"})
	otherTasks := declareTasks(t, stack, otherGoal.ID, []string{"Keep the other project isolated"})
	decision := askParked(t, stack, goal.ID, tasks[0].ID, "scope-run-a")
	otherDecision := askParked(t, stack, otherGoal.ID, otherTasks[0].ID, "scope-run-b")

	for _, decisionID := range []string{decision.DecisionID, otherDecision.DecisionID} {
		status, raw := httpJSON(t, stack, http.MethodPost, "/api/decisions/"+decisionID+"/answer", map[string]string{
			"answer_label": "continue",
			"answer_text":  "Keep the decision scoped to its project",
		})
		if status != http.StatusOK {
			t.Fatalf("POST answer status = %d, body %s", status, raw)
		}
	}

	var orphaned e2eGoalList
	callDaemon(t, stack, "goal.list", map[string]any{
		"cwd": e2eRoot, "agent_session_id": "scope-run-new",
	}, &orphaned)
	if !containsDecision(orphaned.OrphanedDecisions, decision.DecisionID) {
		t.Fatalf("orphaned_decisions does not contain own decision: %+v", orphaned.OrphanedDecisions)
	}
	if containsDecision(orphaned.OrphanedDecisions, otherDecision.DecisionID) {
		t.Fatalf("orphaned_decisions contains other project's decision %s: %+v", otherDecision.DecisionID, orphaned.OrphanedDecisions)
	}

	var answered e2eGoalList
	callDaemon(t, stack, "goal.list", map[string]any{
		"cwd": e2eRoot, "agent_session_id": "scope-run-a",
	}, &answered)
	if !containsDecision(answered.AnsweredDecisions, decision.DecisionID) {
		t.Fatalf("answered_decisions does not contain own decision: %+v", answered.AnsweredDecisions)
	}
	if containsDecision(answered.AnsweredDecisions, otherDecision.DecisionID) {
		t.Fatalf("answered_decisions contains other project's decision %s: %+v", otherDecision.DecisionID, answered.AnsweredDecisions)
	}
}

func containsGoal(goals []domain.Goal, id string) bool {
	for _, goal := range goals {
		if goal.ID == id {
			return true
		}
	}
	return false
}

func containsRawID(values []json.RawMessage, id string) bool {
	for _, value := range values {
		var task struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(value, &task) == nil && task.ID == id {
			return true
		}
	}
	return false
}
