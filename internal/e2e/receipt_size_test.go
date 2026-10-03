package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bodyMarker is embedded in every report body the test sends, so a receipt that
// still carries a body is found by a plain substring search.
const bodyMarker = "BODY-MARKER"

// body returns a report of about size bytes that contains bodyMarker-<name>.
func body(name string, size int) string {
	unit := "The quick brown fox jumps over the lazy dog; this sentence stands in for report prose. "
	text := bodyMarker + "-" + name + " " + strings.Repeat(unit, size/len(unit)+1)
	return text[:size]
}

type daemonRecord struct {
	method string
	result []byte
}

// recordingProxy sits between the shim and the daemon and keeps what the daemon
// returned, which is what the shim handed to MCP before receipts existed.
type recordingProxy struct {
	mu      sync.Mutex
	records []daemonRecord
}

func startRecordingProxy(t *testing.T, daemonSocket string) (*recordingProxy, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct-proxy")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socket := filepath.Join(dir, "p.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("Listen: %v", err)
	}
	proxy := &recordingProxy{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				proxy.serve(conn, daemonSocket)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		wg.Wait()
		os.RemoveAll(dir)
	})
	return proxy, socket
}

func (p *recordingProxy) serve(client net.Conn, daemonSocket string) {
	defer client.Close()
	upstream, err := net.Dial("unix", daemonSocket)
	if err != nil {
		return
	}
	defer upstream.Close()
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		return
	}
	var request rpc.Request
	_ = json.Unmarshal(line, &request)
	if _, err := upstream.Write(line); err != nil {
		return
	}
	reply, err := bufio.NewReader(upstream).ReadBytes('\n')
	if err != nil {
		return
	}
	var response rpc.Response
	if json.Unmarshal(reply, &response) == nil && response.Error == "" {
		p.mu.Lock()
		p.records = append(p.records, daemonRecord{method: request.Method, result: append([]byte(nil), response.Result...)})
		p.mu.Unlock()
	}
	_, _ = io.Copy(client, strings.NewReader(string(reply)))
}

// last returns the most recent record for method at or after index from.
func (p *recordingProxy) last(method string, from int) (daemonRecord, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.records) - 1; i >= from; i-- {
		if p.records[i].method == method {
			return p.records[i], true
		}
	}
	return daemonRecord{}, false
}

func (p *recordingProxy) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.records)
}

type sizeRow struct {
	tool               string
	raw, before, after int
}

type receiptRig struct {
	t        *testing.T
	stack    *e2eStack
	proxy    *recordingProxy
	sessions map[string]*mcp.ClientSession
	ids      map[string]int64
	rows     []sizeRow
}

func newReceiptRig(t *testing.T) *receiptRig {
	t.Helper()
	stack := newE2EStack(t)
	proxy, socket := startRecordingProxy(t, stack.socket)
	rig := &receiptRig{t: t, stack: stack, proxy: proxy, sessions: map[string]*mcp.ClientSession{}, ids: map[string]int64{}}
	project := createProject(t, stack)
	for _, role := range []string{"commander", "subcommander", "executor"} {
		id, err := stack.db.RegisterAgentSessionInProject(context.Background(), os.Getpid(), project.ID)
		if err != nil {
			t.Fatalf("register %s session: %v", role, err)
		}
		rig.ids[role] = id
		rig.sessions[role] = connectShim(t, mcpshim.NewClient(socket), id)
	}
	return rig
}

func connectShim(t *testing.T, client *mcpshim.Client, agentSessionID int64) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-e2e", Version: "test"}, nil)
	mcpshim.Register(server, client, agentSessionID)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

type toolResult struct {
	raw   []byte                     // structuredContent as sent to the agent
	outer map[string]json.RawMessage // its top-level keys
	data  json.RawMessage
}

// call runs one MCP tool as role and returns the receipt. daemonMethod is the
// RPC the tool wraps; it names the proxy record used as the "before" size.
func (r *receiptRig) call(role, tool, daemonMethod string, args map[string]any) toolResult {
	r.t.Helper()
	from := r.proxy.count()
	result, err := r.sessions[role].CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		r.t.Fatalf("%s (%s): %v", tool, role, err)
	}
	if result.IsError {
		text, _ := json.Marshal(result.Content)
		r.t.Fatalf("%s (%s) returned a tool error: %s", tool, role, text)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		r.t.Fatalf("marshal %s structured content: %v", tool, err)
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		r.t.Fatalf("%s structured content %s: %v", tool, raw, err)
	}
	got := toolResult{raw: raw, outer: outer, data: outer["data"]}
	if daemonMethod != "" {
		before, ok := r.proxy.last(daemonMethod, from)
		if !ok {
			r.t.Fatalf("%s: proxy saw no %s call", tool, daemonMethod)
		}
		old := oldShimResponse(before.result)
		r.rows = append(r.rows, sizeRow{tool: tool, raw: len(before.result), before: len(old), after: len(raw)})
		r.checkReceipt(tool, old, got)
	}
	return got
}

var blockedKeys = map[string]bool{}

func init() {
	for _, k := range []string{
		"content", "spec", "plan", "work_done", "now_possible", "how_to_verify", "surprises",
		"needs_review", "result_summary", "request_report", "review_request_report",
		"review_reject_report", "reject_report", "complete_report", "recovery_report",
		"entries", "history", "has_more", "next_cursor", "description", "body", "question", "options",
	} {
		blockedKeys[normalize(k)] = true
	}
}

func normalize(k string) string { return strings.ToLower(strings.ReplaceAll(k, "_", "")) }

// firstObjectKeys returns the keys of raw, or of its first element when raw is
// an array of objects.
func firstObjectKeys(raw json.RawMessage) map[string]bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		var list []map[string]json.RawMessage
		if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
			return nil
		}
		object = list[0]
	}
	keys := map[string]bool{}
	for k := range object {
		keys[k] = true
	}
	return keys
}

// oldShimResponse is what the shim put in structuredContent before receipts:
// the daemon's data envelope without any other key, or the bare object. Some
// receive responses repeat the handoff fields next to "data"; the old shim never
// forwarded those, so they are not part of the "before" size.
func oldShimResponse(rawResult []byte) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(rawResult, &fields) != nil || len(fields["data"]) == 0 || string(fields["data"]) == "null" {
		return rawResult
	}
	kept := map[string]json.RawMessage{}
	for _, k := range []string{"data", "next_step", "role", "claim_evidence", "unapplied_decisions", "claimable_tasks"} {
		if v, ok := fields[k]; ok {
			kept[k] = v
		}
	}
	out, _ := json.Marshal(kept)
	return out
}

// checkReceipt compares the old response with the receipt: every key that is
// not a body stays, and no body marker survives.
func (r *receiptRig) checkReceipt(tool string, rawResult []byte, got toolResult) {
	r.t.Helper()
	beforeData := json.RawMessage(rawResult)
	var beforeOuter map[string]json.RawMessage
	if json.Unmarshal(rawResult, &beforeOuter) == nil && len(beforeOuter["data"]) > 0 {
		beforeData = beforeOuter["data"]
	} else {
		beforeOuter = nil
	}
	keep := ""
	switch {
	case strings.HasSuffix(tool, "_review_reject_receive"):
		keep = "reviewrejectreport"
	case strings.HasSuffix(tool, "_review_receive"):
		keep = "reviewrequestreport"
	}
	afterKeys := firstObjectKeys(got.data)
	for k := range firstObjectKeys(beforeData) {
		if blockedKeys[normalize(k)] && normalize(k) != keep {
			if afterKeys[k] {
				r.t.Errorf("%s: receipt still has body key %q", tool, k)
			}
			continue
		}
		if !afterKeys[k] {
			r.t.Errorf("%s: receipt lost %q", tool, k)
		}
	}
	for k := range beforeOuter {
		if k == "data" || k == "unapplied_decisions" {
			continue
		}
		if _, ok := got.outer[k]; !ok {
			r.t.Errorf("%s: response lost outer key %q", tool, k)
		}
	}
	markers := strings.Count(string(got.data), bodyMarker)
	switch {
	case keep != "" && markers != 1:
		r.t.Errorf("%s: delivered receipt carries %d report bodies, want exactly 1", tool, markers)
	case keep == "" && markers != 0:
		r.t.Errorf("%s: receipt still carries %d report bodies: %s", tool, markers, got.data)
	}
	if strings.Contains(string(rawResult), bodyMarker) && len(got.raw) >= len(rawResult) {
		r.t.Errorf("%s: receipt is %d bytes, not smaller than the %d before", tool, len(got.raw), len(rawResult))
	}
	if strings.Contains(tool, "handoff") && !strings.Contains(tool, "history") {
		hasID := afterKeys["ID"] || afterKeys["HandoffID"] || afterKeys["handoff_id"] || afterKeys["id"]
		if !hasID {
			r.t.Errorf("%s: receipt has no handoff id (ack_transport reads ID / HandoffID / handoff_id / id): keys %v", tool, afterKeys)
		}
	}
}

func (r *receiptRig) logTable() {
	r.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-44s %8s %8s %8s %6s\n", "tool", "daemon", "before", "after", "saved")
	var totalRaw, totalBefore, totalAfter int
	for _, row := range r.rows {
		fmt.Fprintf(&b, "%-44s %8d %8d %8d %5d%%\n", row.tool, row.raw, row.before, row.after, 100*(row.before-row.after)/row.before)
		totalRaw += row.raw
		totalBefore += row.before
		totalAfter += row.after
	}
	if totalBefore > 0 {
		fmt.Fprintf(&b, "%-44s %8d %8d %8d %5d%%\n", "total", totalRaw, totalBefore, totalAfter, 100*(totalBefore-totalAfter)/totalBefore)
	}
	r.t.Log(b.String())
}

func idArg(id int64) string { return idText(id) }

// run executes one step of the lifecycle as a subtest and points the rig's
// failures at it.
func (r *receiptRig) run(t *testing.T, name string, fn func()) {
	t.Helper()
	parent := r.t
	t.Run(name, func(t *testing.T) {
		r.t = t
		defer func() { r.t = parent }()
		fn()
	})
}

// firstID reads the ID of an object, or of the first element of an array.
func firstID(t *testing.T, data json.RawMessage) int64 {
	t.Helper()
	var one struct {
		ID int64 `json:"ID"`
	}
	if json.Unmarshal(data, &one) == nil && one.ID != 0 {
		return one.ID
	}
	var many []struct {
		ID int64 `json:"ID"`
	}
	if err := json.Unmarshal(data, &many); err != nil || len(many) == 0 || many[0].ID == 0 {
		t.Fatalf("no ID in %s", data)
	}
	return many[0].ID
}

func TestReceiptLifecycleThroughRealDaemon(t *testing.T) {
	rig := newReceiptRig(t)
	stack := rig.stack
	var goal domain.Goal
	callDaemon(t, stack, "goal.create", map[string]any{
		"cwd": e2eRoot, "creator": "human",
		"content": "Receipt flow goal\n\n" + body("goal-content", 1500),
	}, &goal)
	gid := idArg(goal.ID)
	handoff := "gh-1"

	rig.run(t, "goal handoff", func() {
		rig.call("commander", "atct_project_claim", "project.claim", map[string]any{"project_id": idArg(goal.ProjectID)})
		rig.call("commander", "atct_goal_handoff_request", "goal.handoff.request", map[string]any{
			"handoff_id": handoff, "goal_id": gid, "request_report": body("goal-request", 2500),
		})
		rig.call("subcommander", "atct_goal_handoff_receive", "goal.handoff.receive", map[string]any{
			"handoff_id": handoff, "goal_id": gid, "session_key": "sub-key",
		})
	})

	rig.run(t, "plan handoff with a rejection", func() {
		rig.call("subcommander", "atct_goal_update_request_report", "goal.update_request_report", map[string]any{
			"goal_id": gid, "spec": body("spec", 2500), "plan": body("plan", 2500),
		})
		planArgs := func(report string) map[string]any {
			return map[string]any{"handoff_id": handoff, "goal_id": gid, "review_request_report": body(report, 1500)}
		}
		ids := map[string]any{"handoff_id": handoff, "goal_id": gid}
		rig.call("subcommander", "atct_plan_handoff_review_request", "plan.handoff.review.request", planArgs("plan-review-1"))
		rig.call("commander", "atct_plan_handoff_review_receive", "plan.handoff.review.receive", ids)
		rig.call("commander", "atct_plan_handoff_review_reject", "plan.handoff.review.reject", map[string]any{
			"handoff_id": handoff, "goal_id": gid, "reject_report": body("plan-reject", 1200),
		})
		rig.call("subcommander", "atct_plan_handoff_review_reject_receive", "plan.handoff.review.reject.receive", ids)
		rig.call("subcommander", "atct_plan_handoff_review_request", "plan.handoff.review.request", planArgs("plan-review-2"))
		rig.call("commander", "atct_plan_handoff_review_receive", "plan.handoff.review.receive", ids)
		rig.call("commander", "atct_plan_handoff_complete", "plan.handoff.complete", map[string]any{
			"handoff_id": handoff, "goal_id": gid, "complete_report": body("plan-complete", 1000),
		})
	})

	var taskID string
	rig.run(t, "task creation", func() {
		handoffs, err := stack.db.ListTaskCreateHandoffs(context.Background(), goal.ID)
		if err != nil || len(handoffs) != 1 {
			t.Fatalf("task-create handoffs = %v, %v; want one generated by plan completion", handoffs, err)
		}
		createID := handoffs[0].ID
		rig.call("subcommander", "atct_task_create_handoff_receive", "task.create_handoff.receive", map[string]any{"handoff_id": createID})
		created := rig.call("subcommander", "atct_task_create", "task.create", map[string]any{
			"handoff_id": createID, "goal_id": gid, "agent": "codex", "idempotency_key": "receipt-e2e",
			"titles":       []string{"Receipt task"},
			"descriptions": []string{body("task-description", 1500)},
		})
		taskID = idArg(firstID(t, created.data))
	})

	rig.run(t, "task handoff with a rejection", func() {
		th := "th-1"
		ids := map[string]any{"handoff_id": th, "task_id": taskID}
		reviewArgs := func(name string) map[string]any {
			return map[string]any{"handoff_id": th, "task_id": taskID, "review_request_report": body(name, 1500)}
		}
		rig.call("subcommander", "atct_task_handoff_request", "task.handoff.request", map[string]any{
			"handoff_id": th, "task_id": taskID, "request_report": body("task-request", 2500),
		})
		rig.call("executor", "atct_task_handoff_receive", "task.handoff.receive", map[string]any{
			"handoff_id": th, "task_id": taskID, "session_key": "exec-key",
		})
		rig.call("executor", "atct_task_handoff_review_request", "task.handoff.review.request", reviewArgs("task-review-1"))
		rig.call("subcommander", "atct_task_handoff_review_receive", "task.handoff.review.receive", ids)
		rig.call("subcommander", "atct_task_handoff_review_reject", "task.handoff.review.reject", map[string]any{
			"handoff_id": th, "task_id": taskID, "reject_report": body("task-reject", 1200),
		})
		rig.call("executor", "atct_task_handoff_review_reject_receive", "task.handoff.review.reject.receive", ids)
		rig.call("executor", "atct_task_handoff_review_request", "task.handoff.review.request", reviewArgs("task-review-2"))
		rig.call("subcommander", "atct_task_handoff_review_receive", "task.handoff.review.receive", ids)
		rig.call("subcommander", "atct_task_handoff_complete", "task.handoff.complete", map[string]any{
			"handoff_id": th, "task_id": taskID, "complete_report": body("task-complete", 1000),
		})
	})

	rig.run(t, "goal handoff review with a rejection", func() {
		ids := map[string]any{"handoff_id": handoff, "goal_id": gid}
		reviewArgs := func(name string) map[string]any {
			return map[string]any{"handoff_id": handoff, "goal_id": gid, "review_request_report": body(name, 1500)}
		}
		rig.call("subcommander", "atct_goal_handoff_review_request", "goal.handoff.review.request", reviewArgs("goal-review-1"))
		rig.call("commander", "atct_goal_handoff_review_receive", "goal.handoff.review.receive", ids)
		rig.call("commander", "atct_goal_handoff_review_reject", "goal.handoff.review.reject", map[string]any{
			"handoff_id": handoff, "goal_id": gid, "reject_report": body("goal-reject", 1200),
		})
		rig.call("subcommander", "atct_goal_handoff_review_reject_receive", "goal.handoff.review.reject.receive", ids)
		rig.call("subcommander", "atct_goal_handoff_review_request", "goal.handoff.review.request", reviewArgs("goal-review-2"))
		rig.call("commander", "atct_goal_handoff_review_receive", "goal.handoff.review.receive", ids)
	})

	rig.run(t, "human review and completion", func() {
		rig.call("commander", "atct_goal_review_request", "goal.review.request", map[string]any{
			"goal_id": gid, "work_done": body("work-done", 1200), "now_possible": body("now-possible", 600),
			"how_to_verify": body("how-to-verify", 600), "surprises": "なし", "needs_review": "なし",
		})
		decisions, err := stack.db.ListDecisionsForGoal(context.Background(), goal.ID)
		if err != nil {
			t.Fatalf("ListDecisionsForGoal: %v", err)
		}
		var review domain.Decision
		for _, d := range decisions {
			if d.Kind == domain.KindGoalReview {
				review = d
			}
		}
		if review.ID == 0 {
			t.Fatal("goal.review.request left no goal review decision")
		}
		if _, err := stack.db.ApproveGoalReview(context.Background(), review.ID); err != nil {
			t.Fatalf("ApproveGoalReview: %v", err)
		}
		rig.call("commander", "atct_goal_review_complete", "goal.review.complete", map[string]any{"goal_id": gid})
	})

	rig.logTable()
}

func TestReceiptUnappliedDecisionsOnlyWhenChanged(t *testing.T) {
	rig := newReceiptRig(t)
	stack := rig.stack
	goal := createGoal(t, stack)
	tasks := declareTasks(t, stack, goal.ID, []string{"Receipt decision task"})
	taskID := idArg(tasks[0].ID)
	rig.call("commander", "atct_project_claim", "project.claim", map[string]any{"project_id": idArg(goal.ProjectID)})

	// The commander owns the decision, so it may poll it. goal.list would apply
	// the answer on its own, which is why this test does not call it.
	var decision parkedDecision
	callDaemon(t, stack, "decision.ask", map[string]any{
		"goal_id": goal.ID, "task_id": tasks[0].ID, "agent_session_id": rig.ids["commander"], "wait_ms": 0,
		"question": "Should the agent continue with this task?",
		"options":  []domain.Option{{Label: "continue", Description: "Continue the task", Consequence: "The run proceeds"}},
	}, &decision)
	if !decision.Parked || decision.DecisionID == 0 {
		t.Fatalf("decision.ask returned %+v, want a parked decision", decision)
	}
	status, raw := httpJSON(t, stack, "POST", "/api/decisions/"+idText(decision.DecisionID)+"/answer", map[string]string{
		"answer_label": "continue", "answer_text": "Go ahead",
	})
	if status != 200 {
		t.Fatalf("POST answer status = %d, body %s", status, raw)
	}
	update := func(status string) toolResult {
		return rig.call("commander", "atct_task_update", "task.update", map[string]any{"task_id": taskID, "status": status})
	}
	listed := func(res toolResult) []int64 {
		var notices []struct {
			DecisionID int64 `json:"decision_id"`
		}
		if err := json.Unmarshal(res.outer["unapplied_decisions"], &notices); err != nil {
			t.Fatalf("unapplied_decisions %s: %v", res.outer["unapplied_decisions"], err)
		}
		var ids []int64
		for _, n := range notices {
			ids = append(ids, n.DecisionID)
		}
		return ids
	}

	first := update("doing")
	if got := listed(first); len(got) != 1 || got[0] != decision.DecisionID {
		t.Fatalf("first response unapplied_decisions = %s, want decision %d", first.outer["unapplied_decisions"], decision.DecisionID)
	}
	second := update("todo")
	if _, ok := second.outer["unapplied_decisions"]; ok {
		t.Fatalf("unchanged response repeats the list: %s", second.outer["unapplied_decisions"])
	}
	if string(second.outer["unapplied_count"]) != "1" {
		t.Fatalf("unchanged response unapplied_count = %s, want 1", second.outer["unapplied_count"])
	}

	// Polling applies the answer, so the list is now empty. atct_decision_poll
	// also asks for unapplied answers, which makes it the next response and the
	// one that says [].
	polled := rig.call("commander", "atct_decision_poll", "", map[string]any{"decision_id": idArg(decision.DecisionID)})
	if string(polled.outer["unapplied_decisions"]) != "[]" {
		t.Fatalf("poll that emptied the list: %s, want unapplied_decisions []", polled.raw)
	}
	for _, status := range []string{"doing", "todo"} {
		next := update(status)
		for _, key := range []string{"unapplied_decisions", "unapplied_count"} {
			if _, ok := next.outer[key]; ok {
				t.Fatalf("list stays empty, but the response has %s: %s", key, next.raw)
			}
		}
	}
}
