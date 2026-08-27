package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
	atctweb "github.com/michiomochi/atct/web"
)

func TestHTTPHandlerServesEmbeddedIndex(t *testing.T) {
	d := newWebTestDaemon(t)

	response := httptest.NewRecorder()
	d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %v, want text/html", response.Header().Get("Content-Type"))
	}
	if !strings.Contains(strings.ToLower(response.Body.String()), "<html") {
		t.Fatal("response does not contain embedded HTML")
	}
}

func TestHTTPHandlerRoutesAPI(t *testing.T) {
	d := newWebTestDaemon(t)

	response := httptest.NewRecorder()
	d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %v, want application/json", response.Header().Get("Content-Type"))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if _, ok := payload["open_decisions"]; !ok {
		t.Fatal("API response is missing open_decisions")
	}
}

func TestHTTPHandlerKeepsSSEOpenUntilDisconnect(t *testing.T) {
	d := newWebTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	response := &notifyingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		wrote:            make(chan struct{}),
	}
	request := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		d.HTTPHandler().ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-response.wrote:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not write its initial response")
	}
	select {
	case <-done:
		t.Fatal("SSE handler closed before the client disconnected")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not close after the client disconnected")
	}
}

func TestHTTPHandlerRoutesEmbeddedDynamicPagesAndFallsBackToRoot(t *testing.T) {
	d := newWebTestDaemon(t)

	wantRoot, err := fs.ReadFile(atctweb.Dist, "dist/index.html")
	if err != nil {
		t.Fatalf("read root index: %v", err)
	}
	wantGoal, err := fs.ReadFile(atctweb.Dist, "dist/goals/_/index.html")
	if err != nil {
		t.Fatalf("read goal history template: %v", err)
	}
	wantTask, err := fs.ReadFile(atctweb.Dist, "dist/tasks/_/index.html")
	if err != nil {
		t.Fatalf("read task history template: %v", err)
	}

	tests := []struct {
		name string
		path string
		want []byte
	}{
		{name: "root", path: "/", want: wantRoot},
		{name: "goal detail", path: "/goals/example", want: wantGoal},
		{name: "task detail", path: "/tasks/example", want: wantTask},
		{name: "unknown path", path: "/nonexistent/xxx", want: wantRoot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if response.Body.String() != string(tt.want) {
				t.Fatalf("route %v returned unexpected embedded page", tt.path)
			}
		})
	}
}

func TestHTTPHandlerReturnsJSON404ForUnknownAPI(t *testing.T) {
	d := newWebTestDaemon(t)

	response := httptest.NewRecorder()
	d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %v, want application/json", response.Header().Get("Content-Type"))
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if payload["error"] == "" {
		t.Fatal("JSON 404 is missing error")
	}
}

func TestHTTPHandlerMCPInitializeReturnsStreamableResponse(t *testing.T) {
	fixture := newMCPHTTPTestServer(t)
	client := newMCPHTTPTestClient(fixture.server.URL + "/mcp")

	payload := client.initialize(t)
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result = %T, want object", payload["result"])
	}
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %#v, want 2025-06-18", result["protocolVersion"])
	}
}

func TestHTTPHandlerMCPListsTwentySixTools(t *testing.T) {
	fixture := newMCPHTTPTestServer(t)
	client := newMCPHTTPTestClient(fixture.server.URL + "/mcp")
	client.initialize(t)
	client.initialized(t)

	payload := client.call(t, "tools/list", map[string]any{})
	result := mcpResult(t, payload)
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list result.tools = %T, want array", result["tools"])
	}
	if len(tools) != 26 {
		t.Fatalf("tools/list returned %d tools, want 26", len(tools))
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			t.Fatalf("tool = %T, want object", rawTool)
		}
		if tool["name"] == "atct_role" {
			return
		}
	}
	t.Fatal("tools/list did not include atct_role")
}

func TestHTTPHandlerMCPTaskHandoffRoutes(t *testing.T) {
	fixture := newMCPHTTPTestServer(t)
	ctx := context.Background()
	project, err := fixture.store.CreateProject(ctx, "atct", filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := fixture.store.CreateGoal(ctx, project.ID, "handoff", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := fixture.store.DeclareTasks(ctx, goal.ID, "commander", "handoff-mcp", []string{
		"delegated task", "claimable task",
	}, []string{
		"A task delegated by the goal owner.",
		"A task used to verify the existing task claim tool.",
	})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	unclaimedGoal, err := fixture.store.CreateGoal(ctx, project.ID, "unclaimed handoff goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal unclaimed: %v", err)
	}
	unclaimedTasks, err := fixture.store.DeclareTasks(ctx, unclaimedGoal.ID, "commander", "unclaimed-handoff-mcp", []string{"unclaimed task"}, []string{"A task in a goal without a claim."})
	if err != nil {
		t.Fatalf("DeclareTasks unclaimed: %v", err)
	}

	client := newMCPHTTPTestClient(fixture.server.URL + "/mcp")
	client.initialize(t)
	client.initialized(t)
	goalClaim := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name":      "atct_goal_claim",
		"arguments": map[string]any{"goal_id": goal.ID},
	}))
	if goalClaim["isError"] == true {
		t.Fatalf("goal claim for task handoff failed: %#v", goalClaim)
	}

	request := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_handoff_request",
		"arguments": map[string]any{
			"handoff_id": "mcp-handoff-1", "task_id": tasks[0].ID,
		},
	}))
	if request["isError"] == true {
		t.Fatalf("handoff request returned an error result: %#v", request)
	}
	requested, err := fixture.store.GetTaskHandoff(ctx, "mcp-handoff-1")
	if err != nil {
		t.Fatalf("GetTaskHandoff after request: %v", err)
	}
	// The HTTP path mints a session per connection, so the requester is not a
	// value the caller chose. It must still be filled in by the shim.
	if requested.RequestedAt == nil || requested.RequestedBy == 0 {
		t.Fatalf("request handoff = %#v, want requested timestamp and an injected requester", requested)
	}

	receive := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_handoff_receive",
		"arguments": map[string]any{
			"handoff_id": "mcp-handoff-1", "task_id": tasks[0].ID,
		},
	}))
	if receive["isError"] == true {
		t.Fatalf("handoff receive returned an error result: %#v", receive)
	}
	received, err := fixture.store.GetTaskHandoff(ctx, "mcp-handoff-1")
	if err != nil {
		t.Fatalf("GetTaskHandoff after receive: %v", err)
	}
	// Both calls ride the same connection, so the shim supplies the same
	// session for each. A receiver that differs from the requester here would
	// mean the injection is per-call rather than per-session.
	if received.ReceivedAt == nil || received.ReceivedBy != requested.RequestedBy {
		t.Fatalf("received handoff = %#v, want received timestamp and receiver", received)
	}

	complete := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_handoff_complete",
		"arguments": map[string]any{
			"handoff_id": "mcp-handoff-1", "task_id": tasks[0].ID, "complete_report": "Verified task handoff completion through the MCP HTTP route.",
		},
	}))
	if complete["isError"] == true {
		t.Fatalf("handoff complete returned an error result: %#v", complete)
	}
	completed, err := fixture.store.GetTaskHandoff(ctx, "mcp-handoff-1")
	if err != nil {
		t.Fatalf("GetTaskHandoff after complete: %v", err)
	}
	if completed.CompletedReportAt == nil {
		t.Fatalf("completed handoff = %#v, want completion timestamp", completed)
	}

	rejected := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_handoff_request",
		"arguments": map[string]any{
			"handoff_id": "mcp-handoff-unclaimed", "task_id": unclaimedTasks[0].ID,
		},
	}))
	if rejected["isError"] != true {
		t.Fatalf("unclaimed handoff request succeeded, want rejection: %#v", rejected)
	}
}

func TestHTTPHandlerMCPGoalHandoffRoutes(t *testing.T) {
	fixture := newMCPHTTPTestServer(t)
	ctx := context.Background()
	project, err := fixture.store.CreateProject(ctx, "atct", filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	claimedGoal, err := fixture.store.CreateGoal(ctx, project.ID, "claimed handoff goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal claimed: %v", err)
	}
	unclaimedProject, err := fixture.store.CreateProject(ctx, "other", filepath.Join(t.TempDir(), "other-repo"))
	if err != nil {
		t.Fatalf("CreateProject unclaimed: %v", err)
	}
	unclaimedGoal, err := fixture.store.CreateGoal(ctx, unclaimedProject.ID, "unclaimed handoff goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal unclaimed: %v", err)
	}
	client := newMCPHTTPTestClient(fixture.server.URL + "/mcp")
	client.initialize(t)
	client.initialized(t)
	projectClaim := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name":      "atct_project_claim",
		"arguments": map[string]any{"project_id": project.ID},
	}))
	if projectClaim["isError"] == true {
		t.Fatalf("project claim for goal handoff failed: %#v", projectClaim)
	}

	request := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_goal_handoff_request",
		"arguments": map[string]any{
			"handoff_id": "mcp-goal-handoff-1", "goal_id": claimedGoal.ID,
		},
	}))
	if request["isError"] == true {
		t.Fatalf("goal handoff request returned an error result: %#v", request)
	}
	requested, err := fixture.store.GetGoalHandoff(ctx, "mcp-goal-handoff-1")
	if err != nil {
		t.Fatalf("GetGoalHandoff after request: %v", err)
	}
	if requested.RequestedAt == nil || requested.RequestedBy == 0 {
		t.Fatalf("request handoff = %#v, want requested timestamp and an injected requester", requested)
	}

	receive := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_goal_handoff_receive",
		"arguments": map[string]any{
			"goal_id": claimedGoal.ID,
		},
	}))
	if receive["isError"] == true {
		t.Fatalf("goal handoff receive returned an error result: %#v", receive)
	}
	received, err := fixture.store.GetGoalHandoff(ctx, "mcp-goal-handoff-1")
	if err != nil {
		t.Fatalf("GetGoalHandoff after receive: %v", err)
	}
	if received.ReceivedAt == nil || received.ReceivedBy != requested.RequestedBy {
		t.Fatalf("received handoff = %#v, want received timestamp and connection session", received)
	}

	complete := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_goal_handoff_complete",
		"arguments": map[string]any{
			"handoff_id": "mcp-goal-handoff-1", "goal_id": claimedGoal.ID, "complete_report": "Verified goal handoff completion through the MCP HTTP route.",
		},
	}))
	if complete["isError"] == true {
		t.Fatalf("goal handoff complete returned an error result: %#v", complete)
	}
	completed, err := fixture.store.GetGoalHandoff(ctx, "mcp-goal-handoff-1")
	if err != nil {
		t.Fatalf("GetGoalHandoff after complete: %v", err)
	}
	if completed.CompletedReportAt == nil {
		t.Fatalf("completed handoff = %#v, want completion timestamp", completed)
	}

	rejected := mcpResult(t, client.call(t, "tools/call", map[string]any{
		"name": "atct_goal_handoff_request",
		"arguments": map[string]any{
			"handoff_id": "mcp-goal-handoff-unclaimed", "goal_id": unclaimedGoal.ID,
		},
	}))
	if rejected["isError"] != true {
		t.Fatalf("unclaimed goal handoff request succeeded, want rejection: %#v", rejected)
	}
}

func TestHTTPHandlerMCPCallsAtctRole(t *testing.T) {
	fixture := newMCPHTTPTestServer(t)
	client := newMCPHTTPTestClient(fixture.server.URL + "/mcp")
	client.initialize(t)
	client.initialized(t)

	payload := client.call(t, "tools/call", map[string]any{
		"name":      "atct_role",
		"arguments": map[string]any{},
	})
	result := mcpResult(t, payload)
	if result["isError"] == true {
		t.Fatalf("atct_role returned an error result: %#v", result)
	}
	if _, ok := result["structuredContent"]; !ok {
		t.Fatalf("atct_role result has no structuredContent: %#v", result)
	}
}

func TestHTTPHandlerMCPUsesDistinctAgentSessionsForClaims(t *testing.T) {
	fixture := newMCPHTTPTestServer(t)
	project, err := fixture.store.CreateProject(context.Background(), "atct", filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	first := newMCPHTTPTestClient(fixture.server.URL + "/mcp")
	second := newMCPHTTPTestClient(fixture.server.URL + "/mcp")
	first.initialize(t)
	first.initialized(t)
	second.initialize(t)
	second.initialized(t)

	ids := agentSessionIDs(t, fixture.store)
	if len(ids) != 2 {
		t.Errorf("agent_sessions count = %d, want 2", len(ids))
	}
	if len(ids) >= 2 {
		if ids[0] == 0 || ids[1] == 0 {
			t.Errorf("agent_sessions contains an empty id: %#v", ids)
		}
		if ids[0] == ids[1] {
			t.Errorf("agent_sessions reused one id: %#v", ids)
		}
	}

	firstResult := mcpResult(t, first.call(t, "tools/call", map[string]any{
		"name": "atct_project_claim",
		"arguments": map[string]any{
			"project_id": project.ID,
		},
	}))
	if firstResult["isError"] == true {
		t.Fatalf("first project claim failed: %#v", firstResult)
	}

	secondResult := mcpResult(t, second.call(t, "tools/call", map[string]any{
		"name": "atct_project_claim",
		"arguments": map[string]any{
			"project_id": project.ID,
		},
	}))
	if secondResult["isError"] != true {
		t.Fatalf("second project claim succeeded, want rejection: %#v", secondResult)
	}
}

type mcpHTTPTestServer struct {
	server *httptest.Server
	store  *store.Store
}

func newMCPHTTPTestServer(t *testing.T) *mcpHTTPTestServer {
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
	socketPath := filepath.Join(dir, "daemon.sock")
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	d := NewWithVersion(s, "test", socketPath)
	go func() { serveDone <- d.Serve(ctx, socketPath) }()
	waitForMCPTestSocket(t, socketPath, serveDone)

	server := httptest.NewServer(d.HTTPHandler())
	t.Cleanup(func() {
		server.Close()
		cancel()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
				t.Errorf("daemon.Serve: %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("daemon.Serve did not stop after cancellation")
		}
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("store.Close: %v", closeErr)
		}
	})
	return &mcpHTTPTestServer{server: server, store: s}
}

func waitForMCPTestSocket(t *testing.T, socketPath string, serveDone <-chan error) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		select {
		case serveErr := <-serveDone:
			t.Fatalf("daemon.Serve exited before socket appeared: %v", serveErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon socket %v did not appear", socketPath)
}

type mcpHTTPTestClient struct {
	endpoint  string
	http      *http.Client
	sessionID string
	nextID    int
}

func newMCPHTTPTestClient(endpoint string) *mcpHTTPTestClient {
	return &mcpHTTPTestClient{
		endpoint: endpoint,
		http:     &http.Client{Timeout: 2 * time.Second},
		nextID:   1,
	}
}

func (c *mcpHTTPTestClient) initialize(t *testing.T) map[string]any {
	t.Helper()
	status, headers, body := c.post(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "atct-test",
			"version": "test",
		},
	}, true)
	if status != http.StatusOK {
		t.Fatalf("initialize status = %d, want %d", status, http.StatusOK)
	}
	if got := headers.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("initialize content type = %v, want text/event-stream", got)
	}
	if c.sessionID == "" {
		t.Fatal("initialize did not return Mcp-Session-Id")
	}
	if !bytes.Contains(body, []byte("event: message")) {
		t.Fatalf("initialize response is not an SSE message: %v", body)
	}
	payload := decodeMCPEvent(t, body)
	if payload["jsonrpc"] != "2.0" {
		t.Fatalf("initialize jsonrpc = %#v, want 2.0", payload["jsonrpc"])
	}
	if _, ok := payload["result"]; !ok {
		t.Fatalf("initialize response has no result: %#v", payload)
	}
	return payload
}

func (c *mcpHTTPTestClient) initialized(t *testing.T) {
	t.Helper()
	status, _, _ := c.post(t, "notifications/initialized", map[string]any{}, false)
	if status != http.StatusAccepted && status != http.StatusOK {
		t.Fatalf("initialized notification status = %d, want 202 or 200", status)
	}
}

func (c *mcpHTTPTestClient) call(t *testing.T, method string, params map[string]any) map[string]any {
	t.Helper()
	status, _, body := c.post(t, method, params, true)
	if status != http.StatusOK {
		t.Fatalf("%v status = %d, want %d", method, status, http.StatusOK)
	}
	return decodeMCPEvent(t, body)
}

func (c *mcpHTTPTestClient) post(t *testing.T, method string, params map[string]any, withID bool) (int, http.Header, []byte) {
	t.Helper()
	requestBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if withID {
		requestBody["id"] = c.nextID
		c.nextID++
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal %v request: %v", method, err)
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new %v request: %v", method, err)
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	response, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("POST %v: %v", method, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %v response: %v", method, err)
	}
	if sessionID := response.Header.Get("Mcp-Session-Id"); sessionID != "" {
		if c.sessionID != "" && c.sessionID != sessionID {
			t.Fatalf("Mcp-Session-Id changed from %v to %v", c.sessionID, sessionID)
		}
		c.sessionID = sessionID
	}
	return response.StatusCode, response.Header, responseBody
}

func decodeMCPEvent(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var data []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(data) == 0 {
		t.Fatalf("SSE response has no data event: %v", body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &payload); err != nil {
		t.Fatalf("decode SSE data: %v (body=%v)", err, body)
	}
	return payload
}

func mcpResult(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	if rpcError, ok := payload["error"]; ok {
		t.Fatalf("MCP request returned JSON-RPC error: %#v", rpcError)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP result = %T, want object: %#v", payload["result"], payload)
	}
	return result
}

func agentSessionIDs(t *testing.T, s *store.Store) []int64 {
	t.Helper()
	rows, err := s.DB().QueryContext(context.Background(), "SELECT * FROM agent_sessions")
	if err != nil {
		t.Fatalf("query agent_sessions: %v", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("agent_sessions columns: %v", err)
	}
	sessionColumn := -1
	for i, column := range columns {
		if strings.EqualFold(column, "id") {
			sessionColumn = i
			break
		}
	}
	if sessionColumn < 0 {
		t.Fatalf("agent_sessions has no session id column: %v", columns)
	}

	var ids []int64
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan agent_sessions: %v", err)
		}
		var id int64
		switch value := values[sessionColumn].(type) {
		case int64:
			id = value
		case string:
			t.Fatalf("agent session id has legacy string type %T: %q", value, value)
		case []byte:
			t.Fatalf("agent session id has legacy blob type %T: %q", value, value)
		default:
			t.Fatalf("agent session id has type %T", value)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate agent_sessions: %v", err)
	}
	return ids
}

func newWebTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s)
}

type notifyingResponseWriter struct {
	*httptest.ResponseRecorder
	wrote chan struct{}
	once  sync.Once
}

func (w *notifyingResponseWriter) notify() {
	w.once.Do(func() { close(w.wrote) })
}

func (w *notifyingResponseWriter) WriteHeader(code int) {
	w.notify()
	w.ResponseRecorder.WriteHeader(code)
}

func (w *notifyingResponseWriter) Write(body []byte) (int, error) {
	w.notify()
	return w.ResponseRecorder.Write(body)
}

func (w *notifyingResponseWriter) Flush() {
	w.notify()
	w.ResponseRecorder.Flush()
}
