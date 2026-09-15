package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRunCodexMonitorWatchUsesCWDProjectIDForSSE(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gotProjectID string
	var gotReconcileProjectID string
	client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
		body := "[]"
		switch req.URL.Path {
		case "/api/inbox":
			body = `{"unapplied_decisions":[]}`
		case "/api/projects":
			body = `[{"id":7,"root_path":"/project"}]`
		case "/api/events/reconcile":
			gotReconcileProjectID = req.URL.Query().Get("project_id")
			body = `{"decisions":[],"goal_handoffs":[],"plan_handoffs":[],"task_handoffs":[]}`
		case "/api/events":
			gotProjectID = req.URL.Query().Get("project_id")
			cancel()
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}

	if err := runCodexMonitorWatch(ctx, client, []string{"http://daemon"}, "/project/worktree", newCodexMonitorBridge(&fakeCodexTurnStarter{}, "")); err != nil {
		t.Fatalf("runCodexMonitorWatch: %v", err)
	}
	if gotProjectID != "7" {
		t.Fatalf("SSE project_id = %q, want cwd project 7", gotProjectID)
	}
	if gotReconcileProjectID != "7" {
		t.Fatalf("reconcile project_id = %q, want cwd project 7", gotReconcileProjectID)
	}
}

func TestCodexAppServerRPC(t *testing.T) {
	const cwd = "/work/project"
	const selectedThreadID = "thread-new"

	conn := newFakeCodexWebSocket()
	var listCalls int
	conn.onWrite = func(payload []byte) {
		var request map[string]json.RawMessage
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		var method string
		if err := json.Unmarshal(request["method"], &method); err != nil {
			t.Errorf("decode method: %v", err)
			return
		}
		if method == "initialized" {
			return
		}
		id := request["id"]
		switch method {
		case "initialize":
			conn.sendJSON(map[string]any{
				"id":     id,
				"result": map[string]any{"userAgent": "codex-test/1"},
			})
		case "thread/list":
			listCalls++
			threads := []map[string]any{
				{"id": "thread-existing", "cwd": cwd, "source": "cli", "status": map[string]any{"type": "idle"}},
			}
			if listCalls > 1 {
				threads = append(threads, map[string]any{
					"id": selectedThreadID, "cwd": cwd, "source": "cli", "status": map[string]any{"type": "idle"},
				})
			}
			conn.sendJSON(map[string]any{
				"id":     id,
				"result": map[string]any{"data": threads},
			})
		case "thread/resume":
			conn.sendJSON(map[string]any{
				"id":     id,
				"result": map[string]any{"thread": map[string]any{"id": selectedThreadID, "cwd": cwd, "source": "cli"}},
			})
		case "turn/start":
			conn.sendJSON(map[string]any{
				"method": "turn/started",
				"params": map[string]any{"threadId": selectedThreadID, "turn": map[string]any{"id": "turn-1"}},
			})
			conn.sendJSON(map[string]any{
				"id":     id,
				"result": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}},
			})
		default:
			t.Errorf("unexpected RPC method %q", method)
		}
	}

	app := newCodexAppServerWithConn(context.Background(), conn)
	defer app.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := app.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	threads, err := app.ListThreads(ctx, cwd)
	if err != nil {
		t.Fatalf("ListThreads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "thread-existing" {
		t.Fatalf("ListThreads() = %#v, want existing thread", threads)
	}
	baseline := codexThreadIDs(threads)
	thread, err := app.DiscoverThread(ctx, cwd, baseline, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("DiscoverThread() error = %v", err)
	}
	if thread.ID != selectedThreadID {
		t.Fatalf("DiscoverThread() ID = %q, want %q", thread.ID, selectedThreadID)
	}
	if err := app.ResumeThread(ctx, selectedThreadID); err != nil {
		t.Fatalf("ResumeThread() error = %v", err)
	}
	turn, err := app.StartTurn(ctx, selectedThreadID, "atct decision approved (decision_id: d1)")
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if turn.ID != "turn-1" {
		t.Fatalf("StartTurn() ID = %q, want %q", turn.ID, "turn-1")
	}

	notification, err := app.NextNotification(ctx)
	if err != nil {
		t.Fatalf("NextNotification() error = %v", err)
	}
	if notification.Method != "turn/started" {
		t.Fatalf("notification method = %q, want turn/started", notification.Method)
	}

	writes := conn.writesSnapshot()
	if len(writes) != 6 {
		t.Fatalf("WebSocket writes = %d, want initialize, initialized, two lists, resume, turn/start", len(writes))
	}
	assertCodexRequest(t, writes[0], "initialize", true)
	assertCodexRequest(t, writes[1], "initialized", false)
	assertCodexRequest(t, writes[2], "thread/list", true)
	assertCodexRequest(t, writes[3], "thread/list", true)
	assertCodexRequest(t, writes[4], "thread/resume", true)
	assertCodexRequest(t, writes[5], "turn/start", true)

	var listRequest map[string]any
	if err := json.Unmarshal(writes[2], &listRequest); err != nil {
		t.Fatalf("decode thread/list request: %v", err)
	}
	params := listRequest["params"].(map[string]any)
	if got := params["cwd"]; !jsonValuesEqual(got, []any{cwd}) {
		t.Fatalf("thread/list cwd = %#v, want [%q]", got, cwd)
	}
	if got := params["sourceKinds"]; !jsonValuesEqual(got, []any{"cli"}) {
		t.Fatalf("thread/list sourceKinds = %#v, want [cli]", got)
	}

	var resumeRequest map[string]any
	if err := json.Unmarshal(writes[4], &resumeRequest); err != nil {
		t.Fatalf("decode thread/resume request: %v", err)
	}
	if got := resumeRequest["params"].(map[string]any)["threadId"]; got != selectedThreadID {
		t.Fatalf("thread/resume threadId = %#v, want %q", got, selectedThreadID)
	}

	var turnRequest map[string]any
	if err := json.Unmarshal(writes[5], &turnRequest); err != nil {
		t.Fatalf("decode turn/start request: %v", err)
	}
	turnParams := turnRequest["params"].(map[string]any)
	if got := turnParams["threadId"]; got != selectedThreadID {
		t.Fatalf("turn/start threadId = %#v, want %q", got, selectedThreadID)
	}
	if got := turnParams["input"]; !jsonValuesEqual(got, []any{map[string]any{
		"type": "text",
		"text": "atct decision approved (decision_id: d1)",
	}}) {
		t.Fatalf("turn/start input = %#v, want one text item", got)
	}
	if _, ok := turnParams["turn/steer"]; ok {
		t.Fatal("turn/start params unexpectedly contain turn/steer")
	}
}

func TestCodexAppServerAcceptsLargeThreadListResponse(t *testing.T) {
	const cwd = "/work/project"
	socketDir, err := os.MkdirTemp("/tmp", "atct-codex-")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "codex.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen(unix): %v", err)
	}

	const paddingSize = (32 << 10) + 1
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		_, payload, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(payload, &request); err != nil || request.Method != "thread/list" {
			return
		}
		response, err := json.Marshal(map[string]any{
			"id": request.ID,
			"result": map[string]any{
				"data": []map[string]any{{
					"id":     "thread-large",
					"cwd":    cwd,
					"source": "cli",
					"status": map[string]string{"type": "idle"},
				}},
				"padding": strings.Repeat("x", paddingSize),
			},
		})
		if err != nil {
			return
		}
		_ = conn.Write(context.Background(), websocket.MessageText, response)
		_, _, _ = conn.Read(context.Background())
	})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Errorf("HTTP server shutdown: %v", err)
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("HTTP server: %v", err)
		}
	}()

	constructors := []struct {
		name string
		new  func(context.Context, string) (*codexAppServer, error)
	}{
		{name: "newCodexAppServer", new: newCodexAppServer},
		{name: "dialCodexAppServer", new: func(ctx context.Context, socketPath string) (*codexAppServer, error) {
			return dialCodexAppServer(ctx, ctx, socketPath)
		}},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			app, err := constructor.new(ctx, socketPath)
			if err != nil {
				t.Fatalf("create App Server: %v", err)
			}
			defer app.Close()

			threads, err := app.ListThreads(ctx, cwd)
			if err != nil {
				t.Fatalf("ListThreads() error = %v", err)
			}
			if len(threads) != 1 || threads[0].ID != "thread-large" {
				t.Fatalf("ListThreads() = %#v, want thread-large", threads)
			}
		})
	}
}

func TestCodexMonitorActionLineAdmitsFormattedTaskActions(t *testing.T) {
	for _, tc := range []struct {
		line      string
		eventName string
		decision  watchDecision
	}{
		{line: "atct handoff reported: task 846 (handoff handoff-846): verified", eventName: "handoff_reported", decision: watchDecision{TaskID: "846", HandoffID: "handoff-846"}},
		{line: "atct wakeup: task 846 has a stale claim", eventName: "wakeup.claim_stale", decision: watchDecision{TaskID: "846"}},
	} {
		if _, ok := selectWatchAgentAction(tc.line, tc.eventName, tc.decision); !ok {
			t.Fatalf("task transition action line rejected: %q", tc.line)
		}
	}
}

func TestCodexMonitorActionLineAdmitsCanonicalHandoffLifecycle(t *testing.T) {
	for _, tc := range []struct {
		line      string
		eventName string
		decision  watchDecision
	}{
		{line: "atct task handoff requested (task_id: 951, handoff_id: task-951)", eventName: "task.handoff.request", decision: watchDecision{TaskID: "951", HandoffID: "task-951"}},
		{line: "atct task handoff received (task_id: 951, handoff_id: task-951)", eventName: "task.handoff.receive", decision: watchDecision{TaskID: "951", HandoffID: "task-951"}},
		{line: "atct task handoff review requested (task_id: 951, handoff_id: task-951)", eventName: "task.handoff.review.request", decision: watchDecision{TaskID: "951", HandoffID: "task-951"}},
		{line: "atct task handoff review received (task_id: 951, handoff_id: task-951)", eventName: "task.handoff.review.receive", decision: watchDecision{TaskID: "951", HandoffID: "task-951"}},
		{line: "atct task handoff review rejected (task_id: 951, handoff_id: task-951)", eventName: "task.handoff.review.reject", decision: watchDecision{TaskID: "951", HandoffID: "task-951"}},
		{line: "atct task handoff completed (task_id: 951, handoff_id: task-951)", eventName: "task.handoff.complete", decision: watchDecision{TaskID: "951", HandoffID: "task-951"}},
		{line: "atct goal handoff requested (goal_id: 225, handoff_id: goal-225)", eventName: "goal.handoff.request", decision: watchDecision{GoalID: "225", HandoffID: "goal-225"}},
		{line: "atct goal handoff received (goal_id: 225, handoff_id: goal-225)", eventName: "goal.handoff.receive", decision: watchDecision{GoalID: "225", HandoffID: "goal-225"}},
		{line: "atct goal handoff review requested (goal_id: 225, handoff_id: goal-225)", eventName: "goal.handoff.review.request", decision: watchDecision{GoalID: "225", HandoffID: "goal-225"}},
		{line: "atct goal handoff review received (goal_id: 225, handoff_id: goal-225)", eventName: "goal.handoff.review.receive", decision: watchDecision{GoalID: "225", HandoffID: "goal-225"}},
		{line: "atct goal handoff review rejected (goal_id: 225, handoff_id: goal-225)", eventName: "goal.handoff.review.reject", decision: watchDecision{GoalID: "225", HandoffID: "goal-225"}},
		{line: "atct goal handoff completed (goal_id: 225, handoff_id: goal-225)", eventName: "goal.handoff.complete", decision: watchDecision{GoalID: "225", HandoffID: "goal-225"}},
		{line: "atct goal review rejected (goal_id: 225, decision_id: 71): commander should call goal.handoff.review.reject", eventName: "goal.review.reject", decision: watchDecision{GoalID: "225", DecisionID: "71"}},
	} {
		if _, ok := selectWatchAgentAction(tc.line, tc.eventName, tc.decision); !ok {
			t.Fatalf("canonical handoff lifecycle action line rejected: %q", tc.line)
		}
	}
}

func TestCodexMonitorActionLineAdmitsLiveness(t *testing.T) {
	if _, ok := selectWatchAgentAction("atct monitor liveness: recheck task 812", "monitor.liveness", watchDecision{TaskID: "812"}); !ok {
		t.Fatal("liveness action line rejected")
	}
}

func TestCodexScopedLivenessQueuesUntilThreadIsIdle(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)

	line := "atct monitor liveness: recheck task 812"
	if err := bridge.ActionSinkWithContext(context.Background())(watchAgentAction{line: line, eventName: "monitor.liveness"}); err != nil {
		t.Fatalf("ActionSinkWithContext() error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 0 {
		t.Fatalf("turn starts while thread active = %v, want none", got)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("queued liveness actions = %d, want 1", got)
	}

	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-1","turn":{"status":"completed"}}`),
	}); err != nil {
		t.Fatalf("HandleNotification() error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 1 || got[0] != line {
		t.Fatalf("turn starts after idle = %v, want [%q]", got, line)
	}
}

func TestCodexAppServerRespondsToServerApprovalRequest(t *testing.T) {
	conn := newFakeCodexWebSocket()
	app := newCodexAppServerWithConn(context.Background(), conn)
	defer app.Close()

	conn.sendJSON(map[string]any{
		"id":     "approval-1",
		"method": "item/commandExecution/requestApproval",
		"params": map[string]any{"threadId": "thread-1", "turnId": "turn-1"},
	})

	var response map[string]json.RawMessage
	deadline := time.Now().Add(time.Second)
	for {
		writes := conn.writesSnapshot()
		if len(writes) > 0 {
			if err := json.Unmarshal(writes[0], &response); err != nil {
				t.Fatalf("decode server response: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server request produced no response")
		}
		time.Sleep(time.Millisecond)
	}

	if got := string(response["id"]); got != `"approval-1"` {
		t.Fatalf("server response id = %s, want %s", got, `"approval-1"`)
	}
	var result struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(response["result"], &result); err != nil {
		t.Fatalf("decode server response result: %v", err)
	}
	if result.Decision != "decline" {
		t.Fatalf("server response decision = %q, want decline", result.Decision)
	}
	if len(response["error"]) != 0 {
		t.Fatalf("server response unexpectedly contains error: %s", response["error"])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := app.NextNotification(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NextNotification() error = %v, want context deadline", err)
	}
	if err := app.Err(); err != nil {
		t.Fatalf("App Server error = %v, want nil", err)
	}
}

func TestCodexAppServerCompletesOtherServerRequestsSafely(t *testing.T) {
	tests := []struct {
		name          string
		id            json.RawMessage
		method        string
		wantResult    string
		wantErrorCode int
	}{
		{
			name:       "user input",
			id:         json.RawMessage(`41`),
			method:     "item/tool/requestUserInput",
			wantResult: `{"answers":{}}`,
		},
		{
			name:       "elicitation",
			id:         json.RawMessage(`"elicitation-1"`),
			method:     "mcpServer/elicitation/request",
			wantResult: `{"action":"decline","content":null}`,
		},
		{
			name:          "permissions",
			id:            json.RawMessage(`"permissions-1"`),
			method:        "item/permissions/requestApproval",
			wantErrorCode: -32601,
		},
		{
			name:       "legacy patch approval",
			id:         json.RawMessage(`"patch-1"`),
			method:     "applyPatchApproval",
			wantResult: `{"decision":{"denied":{"rejection":"atct codex monitor does not approve legacy requests"}}}`,
		},
		{
			name:       "legacy command approval",
			id:         json.RawMessage(`43`),
			method:     "execCommandApproval",
			wantResult: `{"decision":{"denied":{"rejection":"atct codex monitor does not approve legacy requests"}}}`,
		},
		{
			name:          "unsupported",
			id:            json.RawMessage(`42`),
			method:        "unsupported/serverRequest",
			wantErrorCode: -32601,
		},
	}

	conn := newFakeCodexWebSocket()
	app := newCodexAppServerWithConn(context.Background(), conn)
	defer app.Close()
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn.sendJSON(map[string]any{
				"id":     test.id,
				"method": test.method,
				"params": map[string]any{},
			})
			payload := waitForCodexWrite(t, conn, index+1)
			var response map[string]json.RawMessage
			if err := json.Unmarshal(payload, &response); err != nil {
				t.Fatalf("decode server response: %v", err)
			}
			if got := string(response["id"]); got != string(test.id) {
				t.Fatalf("server response id = %s, want %s", got, test.id)
			}
			if test.wantResult != "" {
				if got := string(response["result"]); got != test.wantResult {
					t.Fatalf("server response result = %s, want %s", got, test.wantResult)
				}
				if len(response["error"]) != 0 {
					t.Fatalf("server response unexpectedly contains error: %s", response["error"])
				}
				return
			}
			if len(response["result"]) != 0 {
				t.Fatalf("server response unexpectedly contains result: %s", response["result"])
			}
			var rpcErr codexRPCError
			if err := json.Unmarshal(response["error"], &rpcErr); err != nil {
				t.Fatalf("decode server response error: %v", err)
			}
			if rpcErr.Code != test.wantErrorCode {
				t.Fatalf("server response error code = %d, want %d", rpcErr.Code, test.wantErrorCode)
			}
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := app.NextNotification(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NextNotification() error = %v, want context deadline", err)
	}
}

func TestCodexAppServerUnixSocketTransport(t *testing.T) {
	var gotNetwork, gotAddress string
	client := newCodexAppServerHTTPClientWithDialer("/tmp/codex.sock", func(_ context.Context, network, address string) (net.Conn, error) {
		gotNetwork = network
		gotAddress = address
		return nil, errors.New("dial stopped by test")
	})

	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	_, err = client.Transport.(*http.Transport).DialContext(context.Background(), "tcp", request.URL.Host)
	if err == nil {
		t.Fatal("DialContext() error = nil, want injected dial error")
	}
	if gotNetwork != "unix" || gotAddress != "/tmp/codex.sock" {
		t.Fatalf("DialContext() = network %q address %q, want unix /tmp/codex.sock", gotNetwork, gotAddress)
	}
}

func TestCodexAppServerLifetimeOutlivesDialContext(t *testing.T) {
	dialCtx, cancelDial := context.WithCancel(context.Background())
	conn := newFakeCodexWebSocket()
	app := newCodexAppServerWithLifetime(dialCtx, context.Background(), conn)
	cancelDial()
	select {
	case <-app.done:
		t.Fatal("App Server lifetime ended with dial context")
	default:
	}
	_ = app.Close()
}

func TestCodexAppServerRejectsMalformedResumeResponse(t *testing.T) {
	conn := newFakeCodexWebSocket()
	conn.onWrite = func(payload []byte) {
		var request map[string]json.RawMessage
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		var method string
		if err := json.Unmarshal(request["method"], &method); err != nil {
			t.Errorf("decode method: %v", err)
			return
		}
		if method == "initialized" {
			return
		}
		result := any(map[string]any{"userAgent": "codex-test/1"})
		if method == "thread/resume" {
			result = map[string]any{}
		}
		conn.sendJSON(map[string]any{"id": request["id"], "result": result})
	}
	app := newCodexAppServerWithConn(context.Background(), conn)
	defer app.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := app.ResumeThread(ctx, "thread-1"); err == nil {
		t.Fatal("ResumeThread() error = nil for malformed response")
	}
}

func TestCodexMonitorQueueDeliversFIFOAfterIdle(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)
	ctx := context.Background()

	if err := bridge.Enqueue(ctx, "first"); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	if err := bridge.Enqueue(ctx, "second"); err != nil {
		t.Fatalf("Enqueue(second) error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 0 {
		t.Fatalf("turn starts while active = %#v, want none", got)
	}

	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "remoteControl/status/changed",
		Params: mustJSON(map[string]any{
			"threadId": "thread-1",
			"status":   "disabled",
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(remoteControl/status/changed) error = %v", err)
	}

	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "turn/completed",
		Params: mustJSON(map[string]any{"threadId": "thread-1"}),
	}); err != nil {
		t.Fatalf("HandleNotification(completed) error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("turn starts after first idle = %#v, want [first]", got)
	}

	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "thread/status/changed",
		Params: mustJSON(map[string]any{
			"threadId": "thread-1",
			"status":   map[string]any{"type": "idle"},
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(idle) error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 2 || got[1] != "second" {
		t.Fatalf("turn starts after second idle = %#v, want [first second]", got)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("QueueLen() = %d, want 0", got)
	}
	if !bridge.Active() {
		t.Fatal("bridge is idle after submitting second turn, want active")
	}
}

func TestCodexMonitorQueueContinuesWhenCompletionRacesTurnResponse(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)
	if err := bridge.Enqueue(context.Background(), "first"); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	if err := bridge.Enqueue(context.Background(), "second"); err != nil {
		t.Fatalf("Enqueue(second) error = %v", err)
	}
	starter.onStart = func() { bridge.SetActive(false) }

	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{
		Method: "turn/completed",
		Params: mustJSON(map[string]any{"threadId": "thread-1"}),
	}); err != nil {
		t.Fatalf("HandleNotification(completed) error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("turn starts after completion race = %#v, want [first second]", got)
	}
}

func TestCodexMonitorQueueRetainsFailedSubmission(t *testing.T) {
	starter := &fakeCodexTurnStarter{errs: []error{errors.New("temporary rejection"), nil}}
	bridge := newCodexMonitorBridge(starter, "thread-1")

	if err := bridge.Enqueue(context.Background(), "retry-me"); err == nil {
		t.Fatal("Enqueue() error = nil, want submission error")
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() after failed submission = %d, want 1", got)
	}
	if bridge.Active() {
		t.Fatal("bridge active after failed submission, want idle for retry")
	}

	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{
		Method: "thread/status/changed",
		Params: mustJSON(map[string]any{
			"threadId": "thread-1",
			"status":   map[string]any{"type": "idle"},
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(idle) error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 2 || got[1] != "retry-me" {
		t.Fatalf("turn starts after retry = %#v, want [retry-me retry-me]", got)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("QueueLen() after retry = %d, want 0", got)
	}
}

func TestCodexMonitorUnknownSubmissionStopsActionSink(t *testing.T) {
	starter := &fakeCodexTurnStarter{errs: []error{errCodexTurnSubmitUnknown}}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	ctx := context.Background()

	action := watchAgentAction{line: "possibly-submitted", eventName: "task.handoff.review.reject"}
	if err := bridge.ActionSinkWithContext(ctx)(action); !errors.Is(err, errCodexTurnSubmitUnknown) {
		t.Fatalf("ActionSinkWithContext() error = %v, want unknown submission", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() after unknown submission = %d, want reserved action retained", got)
	}
	if !bridge.disabled {
		t.Fatal("bridge disabled = false, want terminal monitor state")
	}
	if err := bridge.HandleNotification(ctx, codexAppServerNotification{Method: "thread/status/changed", Params: mustJSON(map[string]any{"threadId": "thread-1", "status": map[string]any{"type": "idle"}})}); err != nil {
		t.Fatalf("HandleNotification(idle): %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 1 {
		t.Fatalf("turn starts = %#v, want one possibly-submitted attempt", got)
	}
}

func TestCodexMonitorUnknownSubmissionIgnoresObservedTurnStarted(t *testing.T) {
	starter := &fakeCodexTurnStarter{errs: []error{errCodexTurnSubmitUnknown}}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	ctx := context.Background()
	starter.onStart = func() {
		if err := bridge.HandleNotification(ctx, codexAppServerNotification{
			Method: "turn/started",
			Params: mustJSON(map[string]any{
				"threadId": "thread-1",
				"turn":     map[string]any{"id": "foreign-turn"},
			}),
		}); err != nil {
			t.Errorf("HandleNotification(turn/started): %v", err)
		}
	}

	if err := bridge.Enqueue(ctx, "possibly-submitted"); !errors.Is(err, errCodexTurnSubmitUnknown) {
		t.Fatalf("Enqueue() error = %v, want unknown submission", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() after foreign turn/started = %d, want reserved action retained", got)
	}
	if got := starter.callsSnapshot(); len(got) != 1 {
		t.Fatalf("turn starts = %#v, want one possibly-submitted attempt", got)
	}
}

func TestCodexMonitorQueueRetriesAfterTransientCompletionFailure(t *testing.T) {
	starter := &fakeCodexTurnStarter{errs: []error{errors.New("temporary rejection"), nil}}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)
	if err := bridge.Enqueue(context.Background(), "retry-after-completion"); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{
		Method: "turn/completed",
		Params: mustJSON(map[string]any{"threadId": "thread-1"}),
	}); err != nil {
		t.Fatalf("HandleNotification(completed) error = %v, want transient failure suppressed", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() after completion failure = %d, want 1", got)
	}

	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{
		Method: "thread/status/changed",
		Params: mustJSON(map[string]any{
			"threadId": "thread-1",
			"status":   map[string]any{"type": "idle"},
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(idle) error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 2 || got[0] != "retry-after-completion" || got[1] != "retry-after-completion" {
		t.Fatalf("turn starts after completion retry = %#v, want two retry attempts", got)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("QueueLen() after completion retry = %d, want 0", got)
	}
}

func TestCodexMonitorIdleThreadStartedRetriesTransientStartFailure(t *testing.T) {
	starter := &fakeCodexTurnStarter{errs: []error{errors.New("temporary rejection"), nil}}
	bridge := newCodexMonitorBridge(starter, "")
	ctx := context.Background()

	if err := bridge.Enqueue(ctx, "retry-after-thread-start"); err != nil {
		t.Fatalf("Enqueue() error = %v, want queued before thread starts", err)
	}

	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "thread/started",
		Params: mustJSON(map[string]any{
			"thread": map[string]any{
				"id":     "thread-1",
				"status": map[string]any{"type": "idle"},
			},
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(thread/started) error = %v, want transient failure suppressed", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() after thread/started failure = %d, want 1", got)
	}
	if bridge.Active() {
		t.Fatal("bridge active after thread/started failure, want idle for retry")
	}

	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "thread/status/changed",
		Params: mustJSON(map[string]any{
			"threadId": "thread-1",
			"status":   map[string]any{"type": "idle"},
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(idle) error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 2 || got[0] != "retry-after-thread-start" || got[1] != "retry-after-thread-start" {
		t.Fatalf("turn starts after thread/started retry = %#v, want two retry attempts", got)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("QueueLen() after thread/started retry = %d, want 0", got)
	}
}

func TestCodexMonitorAppServerClosedRetainsPossiblySubmittedItem(t *testing.T) {
	app := newFakeCodexMonitorApp()
	app.notificationErr = errors.New("App Server connection lost")
	startCalls := 0
	app.startTurn = func(context.Context, string, string) (codexTurn, error) {
		startCalls++
		return codexTurn{}, errors.New("turn start failed")
	}
	bridge := newCodexMonitorBridge(app, "thread-1")
	ctx := context.Background()

	if err := bridge.Enqueue(ctx, "must-remain-queued"); err == nil {
		t.Fatal("Enqueue() error = nil, want terminal App Server failure")
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() after fatal App Server failure = %d, want 1", got)
	}
	if !bridge.disabled {
		t.Fatal("bridge disabled = false, want terminal monitor state")
	}

	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "thread/status/changed",
		Params: mustJSON(map[string]any{
			"threadId": "thread-1",
			"status":   map[string]any{"type": "idle"},
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(idle) error = %v, want terminal state to suppress retries", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() after terminal idle notification = %d, want 1", got)
	}
	if got := startCalls; got != 1 {
		t.Fatalf("turn starts after terminal idle notification = %d, want 1", got)
	}
}

func TestCodexMonitorQueuesBeforeThreadIsAttached(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "")

	if err := bridge.Enqueue(context.Background(), "before-thread"); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 0 {
		t.Fatalf("turn starts before thread attachment = %#v, want none", got)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() before thread attachment = %d, want 1", got)
	}

	if err := bridge.AttachThread(context.Background(), "thread-1", false); err != nil {
		t.Fatalf("AttachThread() error = %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 1 || got[0] != "before-thread" {
		t.Fatalf("turn starts after thread attachment = %#v, want [before-thread]", got)
	}
}

func TestCodexMonitorQueuePrunesQueuedApprovalAfterGoalHandoffReceive(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)
	ctx := context.Background()

	for _, action := range []codexMonitorAction{
		{line: "same-goal-approval", eventName: "decision.approved", goalID: "goal-1"},
		{line: "other-goal-approval", eventName: "decision.approved", goalID: "goal-2"},
	} {
		if err := bridge.enqueueAction(ctx, action); err != nil {
			t.Fatalf("enqueueAction(%q): %v", action.line, err)
		}
	}
	if err := bridge.enqueueAction(ctx, codexMonitorAction{
		line:      "goal-receive",
		eventName: "goal.handoff.receive",
		goalID:    "goal-1",
	}); err != nil {
		t.Fatalf("enqueueAction(goal receive): %v", err)
	}
	if got := bridge.QueueLen(); got != 2 {
		t.Fatalf("queue length after goal receive = %d, want other approval and receive", got)
	}

	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "thread/status/changed",
		Params: mustJSON(map[string]any{
			"threadId": "thread-1",
			"status":   map[string]any{"type": "idle"},
		}),
	}); err != nil {
		t.Fatalf("HandleNotification(idle): %v", err)
	}
	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "turn/completed",
		Params: mustJSON(map[string]any{"threadId": "thread-1"}),
	}); err != nil {
		t.Fatalf("HandleNotification(completed): %v", err)
	}

	if got := starter.callsSnapshot(); len(got) != 2 || got[0] != "other-goal-approval" || got[1] != "goal-receive" {
		t.Fatalf("turns after queued approval prune = %#v, want [other-goal-approval goal-receive]", got)
	}
}

func TestCodexMonitorQueueKeepsActiveApprovalAfterGoalHandoffReceive(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	ctx := context.Background()

	if err := bridge.enqueueAction(ctx, codexMonitorAction{
		line:      "active-approval",
		eventName: "decision.approved",
		goalID:    "goal-1",
	}); err != nil {
		t.Fatalf("enqueueAction(active approval): %v", err)
	}
	if !bridge.Active() {
		t.Fatal("bridge is idle after starting approval, want active")
	}
	if err := bridge.enqueueAction(ctx, codexMonitorAction{
		line:      "goal-receive",
		eventName: "goal.handoff.receive",
		goalID:    "goal-1",
	}); err != nil {
		t.Fatalf("enqueueAction(goal receive): %v", err)
	}

	if got := starter.callsSnapshot(); len(got) != 1 || got[0] != "active-approval" {
		t.Fatalf("turns while approval is active = %#v, want [active-approval]", got)
	}
	if err := bridge.HandleNotification(ctx, codexAppServerNotification{
		Method: "turn/completed",
		Params: mustJSON(map[string]any{"threadId": "thread-1"}),
	}); err != nil {
		t.Fatalf("HandleNotification(completed): %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 2 || got[1] != "goal-receive" {
		t.Fatalf("turns after active approval completed = %#v, want receive after active approval", got)
	}
}

func TestCodexMonitorCoalescesPendingAndActiveActionsByDeliveryKey(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	ctx := context.Background()
	active := codexMonitorAction{line: "request", eventName: "task.handoff.request", goalID: "goal-1", deliveryKey: "task.handoff.request\x00handoff-1"}
	if err := bridge.enqueueAction(ctx, active); err != nil {
		t.Fatalf("enqueue active: %v", err)
	}
	if err := bridge.enqueueAction(ctx, active); err != nil {
		t.Fatalf("enqueue active duplicate: %v", err)
	}
	pending := codexMonitorAction{line: "review", eventName: "task.handoff.review.request", goalID: "goal-1", deliveryKey: "task.handoff.review.request\x00handoff-1"}
	if err := bridge.enqueueAction(ctx, pending); err != nil {
		t.Fatalf("enqueue pending: %v", err)
	}
	if err := bridge.enqueueAction(ctx, pending); err != nil {
		t.Fatalf("enqueue pending duplicate: %v", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("QueueLen() = %d, want one pending action", got)
	}
	if err := bridge.HandleNotification(ctx, codexAppServerNotification{Method: "turn/completed", Params: mustJSON(map[string]any{"threadId": "thread-1"})}); err != nil {
		t.Fatalf("complete active turn: %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 2 || got[0] != "request" || got[1] != "review" {
		t.Fatalf("started turns = %#v, want [request review]", got)
	}
}

func TestCodexMonitorKeepsDistinctDeliveryPhasesInOrder(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	ctx := context.Background()
	for _, action := range []codexMonitorAction{
		{line: "request", eventName: "task.handoff.request", goalID: "goal-1", deliveryKey: "task.handoff.request\x00handoff-1"},
		{line: "review", eventName: "task.handoff.review.request", goalID: "goal-1", deliveryKey: "task.handoff.review.request\x00handoff-1"},
		{line: "reject", eventName: "task.handoff.review.reject", goalID: "goal-1", deliveryKey: "task.handoff.review.reject\x00handoff-1"},
		{line: "other", eventName: "task.handoff.request", goalID: "goal-1", deliveryKey: "task.handoff.request\x00handoff-2"},
	} {
		if err := bridge.enqueueAction(ctx, action); err != nil {
			t.Fatalf("enqueue %q: %v", action.line, err)
		}
	}
	for range 3 {
		if err := bridge.HandleNotification(ctx, codexAppServerNotification{Method: "turn/completed", Params: mustJSON(map[string]any{"threadId": "thread-1"})}); err != nil {
			t.Fatalf("complete turn: %v", err)
		}
	}
	if got := starter.callsSnapshot(); strings.Join(got, ",") != "request,review,reject,other" {
		t.Fatalf("started turns = %#v, want distinct phases and handoffs in order", got)
	}
}

func TestCodexMonitorReviewQueueUsesHandoffGeneration(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)
	ctx := context.Background()
	const handoffID = "handoff-246"
	const targetRole = "executor"
	const requestGeneration = "2026-09-14T00:00:01.000000000Z"
	const receiptGeneration = "2026-09-14T00:00:02.000000000Z"
	const retryGeneration = "2026-09-14T00:00:03.000000000Z"
	const rejectionReceiptGeneration = "2026-09-14T00:00:04.000000000Z"

	enqueue := func(eventName, line, generation string) {
		t.Helper()
		action := watchAgentAction{
			line:        line,
			eventName:   eventName,
			deliveryKey: strings.Join([]string{eventName, targetRole, handoffID}, "\x00"),
			generation:  generation,
			controlOnly: eventName == "task.handoff.review.receive",
		}
		if err := bridge.ActionSinkWithContext(ctx)(action); err != nil {
			t.Fatalf("enqueue %q: %v", line, err)
		}
	}
	queuedLine := func() string {
		t.Helper()
		bridge.stateMu.Lock()
		defer bridge.stateMu.Unlock()
		if len(bridge.queue) != 1 {
			t.Fatalf("queued actions = %#v, want one action", bridge.queue)
		}
		return bridge.queue[0].line
	}

	enqueue("task.handoff.review.request", "request", requestGeneration)
	if got := queuedLine(); got != "request" {
		t.Fatalf("queued request = %q, want request", got)
	}
	enqueue("task.handoff.review.receive", "receipt", receiptGeneration)
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("queue after receipt = %d, want stale request removed and receipt omitted", got)
	}
	enqueue("task.handoff.review.request", "later retry", retryGeneration)
	if got := queuedLine(); got != "later retry" {
		t.Fatalf("queued retry = %q, want newer retry to replace receipt", got)
	}
	enqueue("task.handoff.review.reject.receive", "rejection receipt", rejectionReceiptGeneration)
	if got := queuedLine(); got != "rejection receipt" {
		t.Fatalf("queued rejection receipt = %q, want newest review action", got)
	}
	enqueue("task.handoff.review.request", "delayed old request", requestGeneration)
	if got := queuedLine(); got != "rejection receipt" {
		t.Fatalf("queued delayed request = %q, want old request discarded", got)
	}
	enqueue("task.handoff.review.reject", "same-generation rejection", rejectionReceiptGeneration)
	if got := queuedLine(); got != "rejection receipt" {
		t.Fatalf("queued same-generation action = %q, want duplicate discarded", got)
	}
}

func TestCodexMonitorReviewQueueKeepsActiveActionAndScopesCoalescing(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	ctx := context.Background()

	active := watchAgentAction{
		line:        "active review",
		eventName:   "task.handoff.review.request",
		deliveryKey: "task.handoff.review.request\x00executor\x00handoff-active",
		generation:  "2026-09-14T00:00:01.000000000Z",
	}
	if err := bridge.ActionSinkWithContext(ctx)(active); err != nil {
		t.Fatalf("enqueue active review: %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 1 || got[0] != "active review" {
		t.Fatalf("started active review = %#v, want active review", got)
	}

	newer := watchAgentAction{
		line:        "newer queued review",
		eventName:   "task.handoff.review.receive",
		deliveryKey: "task.handoff.review.receive\x00executor\x00handoff-active",
		generation:  "2026-09-14T00:00:02.000000000Z",
		controlOnly: true,
	}
	if err := bridge.ActionSinkWithContext(ctx)(newer); err != nil {
		t.Fatalf("enqueue newer review: %v", err)
	}
	if err := bridge.ActionSinkWithContext(ctx)(watchAgentAction{
		line:        "delayed active review",
		eventName:   "task.handoff.review.request",
		deliveryKey: "task.handoff.review.request\x00executor\x00handoff-active",
		generation:  "2026-09-14T00:00:00.000000000Z",
	}); err != nil {
		t.Fatalf("enqueue delayed active review: %v", err)
	}

	bridge.stateMu.Lock()
	if bridge.activeAction == nil || bridge.activeAction.line != "active review" {
		t.Fatalf("active action = %#v, want active review", bridge.activeAction)
	}
	if len(bridge.queue) != 0 {
		t.Fatalf("active handoff queue = %#v, want no receipt turn", bridge.queue)
	}
	bridge.stateMu.Unlock()

	for _, action := range []watchAgentAction{
		{
			line:        "other handoff old review",
			eventName:   "task.handoff.review.request",
			deliveryKey: "task.handoff.review.request\x00executor\x00handoff-other",
			generation:  "2026-09-14T00:00:00.000000000Z",
		},
		{
			line:        "non-review lifecycle",
			eventName:   "task.handoff.request",
			deliveryKey: "task.handoff.request\x00subcommander\x00handoff-active",
			generation:  "2026-09-14T00:00:00.000000000Z",
		},
	} {
		if err := bridge.ActionSinkWithContext(ctx)(action); err != nil {
			t.Fatalf("enqueue scoped action %q: %v", action.line, err)
		}
	}

	bridge.stateMu.Lock()
	if bridge.activeAction == nil || bridge.activeAction.line != "active review" {
		t.Fatalf("active action after scoped actions = %#v, want active review", bridge.activeAction)
	}
	if len(bridge.queue) != 2 || bridge.queue[0].line != "other handoff old review" || bridge.queue[1].line != "non-review lifecycle" {
		t.Fatalf("scoped review queue = %#v, want other handoff and lifecycle", bridge.queue)
	}
	bridge.stateMu.Unlock()
}

func TestCodexMonitorReconciliationReviewQueueUsesGeneration(t *testing.T) {
	const requestState = `{"goals":[],"decisions":[],"goal_handoffs":[],"plan_handoffs":[],"task_handoffs":[{"ID":"handoff-246","GoalID":246,"TaskID":1273,"ReviewRequestedAt":"2026-09-14T00:00:01.000000000Z"}]}`
	const receiptState = `{"goals":[],"decisions":[],"goal_handoffs":[],"plan_handoffs":[],"task_handoffs":[{"ID":"handoff-246","GoalID":246,"TaskID":1273,"ReviewReceivedAt":"2026-09-14T00:00:02.000000000Z"}]}`
	responses := []string{requestState, receiptState, requestState, receiptState, receiptState}
	responseIndex := 0
	client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/events/reconcile" {
			return nil, errors.New("unexpected request path")
		}
		body := responses[responseIndex]
		responseIndex++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	bridge.SetActive(true)
	scope := watchScope{ProjectID: "1"}
	newState := func() (map[watchDeliveryKey]struct{}, *string, map[watchWakeupDiscrepancyDeliveryKey]struct{}, map[watchWakeupDeliveryKey]struct{}) {
		return make(map[watchDeliveryKey]struct{}), new(string), make(map[watchWakeupDiscrepancyDeliveryKey]struct{}), make(map[watchWakeupDeliveryKey]struct{})
	}
	delivered, lastWakeupContent, discrepancyDelivered, wakeupDelivered := newState()

	reconcile := func(stateDelivered map[watchDeliveryKey]struct{}, stateLastWakeupContent *string, stateDiscrepancyDelivered map[watchWakeupDiscrepancyDeliveryKey]struct{}, stateWakeupDelivered map[watchWakeupDeliveryKey]struct{}) {
		t.Helper()
		if err := reconcileWatchScope(
			context.Background(), client, "http://daemon", scope, io.Discard,
			stateDelivered, stateLastWakeupContent, stateDiscrepancyDelivered, stateWakeupDelivered,
			newWatchPassThroughFilter(), bridge.LineSink(), bridge.ActionSink(),
		); err != nil {
			t.Fatalf("reconcileWatchScope: %v", err)
		}
	}

	reconcile(delivered, lastWakeupContent, discrepancyDelivered, wakeupDelivered)
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("queue after review request reconciliation = %d, want 1", got)
	}

	reconcile(delivered, lastWakeupContent, discrepancyDelivered, wakeupDelivered)
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("queue after review receipt reconciliation = %d, want no receipt turn", got)
	}

	oldDelivered, oldLastWakeupContent, oldDiscrepancyDelivered, oldWakeupDelivered := newState()
	reconcile(oldDelivered, oldLastWakeupContent, oldDiscrepancyDelivered, oldWakeupDelivered)
	reconcile(oldDelivered, oldLastWakeupContent, oldDiscrepancyDelivered, oldWakeupDelivered)
	reconcile(oldDelivered, oldLastWakeupContent, oldDiscrepancyDelivered, oldWakeupDelivered)

	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("queue after delayed request and repeated receipt = %d, want no stale request or receipt", got)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("queue after repeated receipt reconciliation = %d, want 0", got)
	}
	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{
		Method: "turn/completed",
		Params: mustJSON(map[string]any{"threadId": "thread-1"}),
	}); err != nil {
		t.Fatalf("HandleNotification(completed): %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 0 {
		t.Fatalf("started reconciliation actions = %#v, want no receipt turn", got)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("queue after idle notification = %d, want 0", got)
	}
}

func TestCodexMonitorRejectionDeliveryAcrossHandoffKinds(t *testing.T) {
	const (
		goalID              = "246"
		taskID              = "1281"
		handoffRequestAt    = "2026-09-16T00:00:01.000000000Z"
		handoffReceivedAt   = "2026-09-16T00:00:02.000000000Z"
		handoffRejectedAt   = "2026-09-16T00:00:03.000000000Z"
		handoffRequestEvent = ".handoff.review.request"
		handoffReceiveEvent = ".handoff.review.receive"
		handoffRejectEvent  = ".handoff.review.reject"
		livenessEvent       = "monitor.liveness"
	)
	cases := []struct {
		name      string
		kind      string
		handoffID string
		role      string
		task      bool
	}{
		{name: "plan", kind: "plan", handoffID: "plan-handoff-246", role: "subcommander"},
		{name: "goal", kind: "goal", handoffID: "goal-handoff-246", role: "subcommander"},
		{name: "task", kind: "task", handoffID: "task-handoff-1281", role: "executor", task: true},
	}
	stringPtr := func(value string) *string { return &value }

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope := watchScope{ProjectID: "1", GoalID: goalID, Role: tc.role}
			if tc.task {
				scope.TaskID = taskID
			}
			actionFor := func(eventName, generation string) watchAgentAction {
				t.Helper()
				decision := watchDecision{
					GoalID:             goalID,
					HandoffID:          tc.handoffID,
					TargetRole:         tc.role,
					deliveryGeneration: generation,
				}
				if tc.task {
					decision.TaskID = taskID
				}
				var line string
				var ok bool
				if eventName == livenessEvent {
					line = formatWatchLiveness(scope)
					ok = true
				} else {
					line, ok = formatWatchDecision(eventName, decision)
				}
				if !ok {
					t.Fatalf("formatWatchDecision(%q) rejected test action", eventName)
				}
				action, ok := selectWatchAgentAction(line, eventName, decision)
				if !ok {
					t.Fatalf("selectWatchAgentAction(%q) rejected test action", eventName)
				}
				wantControlOnly := strings.HasSuffix(eventName, ".handoff.review.receive")
				if action.controlOnly != wantControlOnly {
					t.Fatalf("action %q controlOnly = %v, want %v", eventName, action.controlOnly, wantControlOnly)
				}
				return action
			}

			t.Run("confirmed rejection precedes liveness", func(t *testing.T) {
				starter := &fakeCodexTurnStarter{}
				bridge := newCodexMonitorBridge(starter, "thread-1")
				bridge.SetActive(true)
				ctx := context.Background()
				enqueue := func(action watchAgentAction) {
					t.Helper()
					if err := bridge.ActionSinkWithContext(ctx)(action); err != nil {
						t.Fatalf("enqueue %q: %v", action.line, err)
					}
				}

				enqueue(actionFor(tc.kind+handoffRequestEvent, handoffRequestAt))
				enqueue(actionFor(tc.kind+handoffReceiveEvent, handoffReceivedAt))
				if got := bridge.QueueLen(); got != 0 {
					t.Fatalf("queue after control-only receipt = %d, want older request removed", got)
				}
				if got := starter.callsSnapshot(); len(got) != 0 {
					t.Fatalf("turns after control-only receipt = %#v, want none", got)
				}

				rejection := actionFor(tc.kind+handoffRejectEvent, handoffRejectedAt)
				liveness := actionFor(livenessEvent, "")
				enqueue(rejection)
				enqueue(liveness)
				bridge.stateMu.Lock()
				if len(bridge.queue) != 2 || bridge.queue[0].line != rejection.line || bridge.queue[1].line != liveness.line {
					t.Fatalf("queued actions = %#v, want rejection then liveness", bridge.queue)
				}
				if bridge.queue[0].controlOnly {
					t.Fatal("rejection action is control-only")
				}
				bridge.stateMu.Unlock()

				for range 2 {
					if err := bridge.HandleNotification(ctx, codexAppServerNotification{
						Method: "turn/completed",
						Params: mustJSON(map[string]any{"threadId": "thread-1"}),
					}); err != nil {
						t.Fatalf("HandleNotification(completed): %v", err)
					}
				}
				if got := starter.callsSnapshot(); len(got) != 2 || got[0] != rejection.line || got[1] != liveness.line {
					t.Fatalf("confirmed turns = %#v, want [%q %q]", got, rejection.line, liveness.line)
				}

				handoff := watchReconciliationHandoff{
					ID:                tc.handoffID,
					GoalID:            246,
					RequestedAt:       stringPtr(handoffRequestAt),
					ReviewRequestedAt: stringPtr(handoffRequestAt),
					ReviewReceivedAt:  stringPtr(handoffReceivedAt),
					ReviewRejectedAt:  stringPtr(handoffRejectedAt),
				}
				state := watchReconciliation{}
				switch tc.kind {
				case "plan":
					state.PlanHandoffs = []watchReconciliationHandoff{handoff}
				case "goal":
					state.GoalHandoffs = []watchReconciliationHandoff{handoff}
				case "task":
					handoff.TaskID = 1281
					state.TaskHandoffs = []watchReconciliationHandoff{handoff}
				}
				payload := string(mustJSON(state))
				var reconcileCalls int
				client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path != "/api/events/reconcile" {
						return nil, errors.New("unexpected request path")
					}
					reconcileCalls++
					return &http.Response{
						StatusCode: http.StatusOK,
						Status:     "200 OK",
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(payload)),
					}, nil
				})}
				freshStarter := &fakeCodexTurnStarter{}
				freshBridge := newCodexMonitorBridge(freshStarter, "thread-fresh")
				delivered := make(map[watchDeliveryKey]struct{})
				lastWakeupContent := ""
				wakeupDiscrepancyDelivered := make(map[watchWakeupDiscrepancyDeliveryKey]struct{})
				wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
				scopeFilter := newWatchScopeFilter(goalID)
				if tc.task {
					scopeFilter = newWatchTaskScopeFilter(taskID)
				}
				for range 2 {
					if err := reconcileWatchScope(
						ctx, client, "http://daemon", scope, io.Discard,
						delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, wakeupDelivered,
						scopeFilter, freshBridge.LineSink(), freshBridge.ActionSink(),
					); err != nil {
						t.Fatalf("fresh reconcileWatchScope: %v", err)
					}
				}
				if reconcileCalls != 2 {
					t.Fatalf("fresh reconciliation calls = %d, want 2", reconcileCalls)
				}
				if got := freshStarter.callsSnapshot(); len(got) != 1 || got[0] != rejection.line {
					t.Fatalf("fresh watcher turns = %#v, want one %q", got, rejection.line)
				}
			})

			t.Run("unknown rejection stops liveness", func(t *testing.T) {
				starter := &fakeCodexTurnStarter{errs: []error{errCodexTurnSubmitUnknown}}
				bridge := newCodexMonitorBridge(starter, "thread-1")
				bridge.SetActive(true)
				ctx := context.Background()
				enqueue := func(action watchAgentAction) {
					t.Helper()
					if err := bridge.ActionSinkWithContext(ctx)(action); err != nil {
						t.Fatalf("enqueue %q: %v", action.line, err)
					}
				}
				enqueue(actionFor(tc.kind+handoffRequestEvent, handoffRequestAt))
				enqueue(actionFor(tc.kind+handoffReceiveEvent, handoffReceivedAt))
				bridge.SetActive(false)
				rejection := actionFor(tc.kind+handoffRejectEvent, handoffRejectedAt)
				if err := bridge.ActionSinkWithContext(ctx)(rejection); !errors.Is(err, errCodexTurnSubmitUnknown) {
					t.Fatalf("rejection sink error = %v, want unknown submission", err)
				}
				if got := starter.callsSnapshot(); len(got) != 1 || got[0] != rejection.line {
					t.Fatalf("unknown turns = %#v, want one %q", got, rejection.line)
				}
				if got := bridge.QueueLen(); got != 1 {
					t.Fatalf("queue after unknown rejection = %d, want retained rejection", got)
				}
				if !bridge.disabled {
					t.Fatal("bridge disabled = false, want terminal monitor state")
				}
				if err := bridge.ActionSinkWithContext(ctx)(actionFor(livenessEvent, "")); err == nil {
					t.Fatal("liveness sink error = nil, want terminal bridge error")
				}
				if err := bridge.HandleNotification(ctx, codexAppServerNotification{
					Method: "thread/status/changed",
					Params: mustJSON(map[string]any{
						"threadId": "thread-1",
						"status":   map[string]any{"type": "idle"},
					}),
				}); err != nil {
					t.Fatalf("HandleNotification(idle): %v", err)
				}
				if got := starter.callsSnapshot(); len(got) != 1 {
					t.Fatalf("turns after liveness/idle = %#v, want one attempt", got)
				}
			})
		})
	}
}

func TestCodexMonitorEventSinkOnlyReceivesFormattedLines(t *testing.T) {
	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	rawSink := bridge.LineSink()
	actionSink := bridge.ActionSink()
	state := make(map[watchDeliveryKey]struct{})
	lastWakeup := ""
	if err := emitWatchDecisionWithStateAndSinks(io.Discard, "decision.approved", watchDecision{DecisionID: "d1"}, state, &lastWakeup, make(map[watchWakeupDiscrepancyDeliveryKey]struct{}), make(map[watchWakeupDeliveryKey]struct{}), rawSink, actionSink); err != nil {
		t.Fatalf("emit approved: %v", err)
	}
	if err := emitWatchDecisionWithStateAndSinks(io.Discard, "keepalive", watchDecision{}, state, &lastWakeup, make(map[watchWakeupDiscrepancyDeliveryKey]struct{}), make(map[watchWakeupDeliveryKey]struct{}), rawSink, actionSink); err != nil {
		t.Fatalf("emit keepalive: %v", err)
	}
	for _, line := range []string{
		"atct watch: connection unavailable; reconnecting in 5s",
		"atct decision default applied (decision_id: d2)",
		"atct wakeup: malformed",
	} {
		if err := rawSink(line); err != nil {
			t.Fatalf("rawSink(%q): %v", line, err)
		}
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("diagnostic lines queued = %d, want 0", got)
	}
	if got := starter.callsSnapshot(); len(got) != 1 || got[0] != "atct decision approved (decision_id: d1)" {
		t.Fatalf("sink calls = %#v, want only formatted action line", got)
	}
}

func TestReconcileWatchScopeSendsAppliedApprovalToCodexMonitorBridge(t *testing.T) {
	client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/events/reconcile" {
			return nil, errors.New("unexpected request path")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"goals":[{"id":42,"status":"active"}],"decisions":[{"id":71,"goal_id":42,"kind":"goal_approval","status":"applied","answer_label":"approve"}],"goal_handoffs":[],"plan_handoffs":[],"task_handoffs":[]}`)),
		}, nil
	})}

	starter := &fakeCodexTurnStarter{}
	bridge := newCodexMonitorBridge(starter, "thread-1")
	lastWakeupContent := ""
	err := reconcileWatchScope(
		context.Background(), client, "http://daemon", watchScope{ProjectID: "1"}, io.Discard,
		make(map[watchDeliveryKey]struct{}), &lastWakeupContent,
		make(map[watchWakeupDiscrepancyDeliveryKey]struct{}), make(map[watchWakeupDeliveryKey]struct{}),
		newWatchScopeFilter(""), bridge.LineSink(), bridge.ActionSink(),
	)
	if err != nil {
		t.Fatalf("reconcileWatchScope: %v", err)
	}
	if got := starter.callsSnapshot(); len(got) != 1 || got[0] != "atct decision approved (decision_id: 71)" {
		t.Fatalf("bridge turn starts = %#v, want one approval action line", got)
	}
}

type fakeCodexWebSocket struct {
	mu       sync.Mutex
	writes   [][]byte
	messages chan []byte
	closed   chan struct{}
	onWrite  func([]byte)
	once     sync.Once
}

func newFakeCodexWebSocket() *fakeCodexWebSocket {
	return &fakeCodexWebSocket{
		messages: make(chan []byte, 32),
		closed:   make(chan struct{}),
	}
}

func (f *fakeCodexWebSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case message := <-f.messages:
		return websocket.MessageText, message, nil
	case <-f.closed:
		return 0, nil, io.EOF
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (f *fakeCodexWebSocket) Write(_ context.Context, _ websocket.MessageType, payload []byte) error {
	f.mu.Lock()
	f.writes = append(f.writes, append([]byte(nil), payload...))
	onWrite := f.onWrite
	f.mu.Unlock()
	if onWrite != nil {
		onWrite(payload)
	}
	return nil
}

func (f *fakeCodexWebSocket) Close(_ websocket.StatusCode, _ string) error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeCodexWebSocket) sendJSON(value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	f.messages <- payload
}

func (f *fakeCodexWebSocket) writesSnapshot() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	writes := make([][]byte, len(f.writes))
	for i, write := range f.writes {
		writes[i] = append([]byte(nil), write...)
	}
	return writes
}

func assertCodexRequest(t *testing.T, payload []byte, method string, hasID bool) {
	t.Helper()
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode %s request: %v", method, err)
	}
	if got := request["method"]; got != method {
		t.Fatalf("request method = %#v, want %q", got, method)
	}
	_, gotID := request["id"]
	if gotID != hasID {
		t.Fatalf("request %s has id = %v, want %v", method, gotID, hasID)
	}
}

func waitForCodexWrite(t *testing.T, conn *fakeCodexWebSocket, count int) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		writes := conn.writesSnapshot()
		if len(writes) >= count {
			return writes[count-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("WebSocket writes = %d, want at least %d", len(writes), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func jsonValuesEqual(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func mustJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

type fakeCodexTurnStarter struct {
	mu      sync.Mutex
	calls   []string
	errs    []error
	onStart func()
}

func (f *fakeCodexTurnStarter) StartTurn(_ context.Context, _ string, text string) (codexTurn, error) {
	f.mu.Lock()
	f.calls = append(f.calls, text)
	onStart := f.onStart
	if len(f.errs) == 0 {
		f.mu.Unlock()
		if onStart != nil {
			onStart()
		}
		return codexTurn{ID: "turn-test"}, nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	f.mu.Unlock()
	if onStart != nil {
		onStart()
	}
	if err != nil {
		return codexTurn{}, err
	}
	return codexTurn{ID: "turn-test"}, nil
}

func (f *fakeCodexTurnStarter) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
