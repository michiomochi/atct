package mcpshim

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fullHandoff is shaped like a real GoalHandoff / TaskHandoff response: the
// request body appears in RequestReport, in entries and again in history.
const fullHandoff = `{
 "ID":"h-1","GoalID":315,"TaskID":9007199254740993,"handoff_id":"h-1","task_id":9007199254740993,
 "Status":"requested","Kind":"task","RequestedBy":10939,"RequestedAt":"2026-10-03T12:48:57Z",
 "ReceivedBy":10947,"ReceivedAt":"2026-10-03T13:15:13Z",
 "RequestReport":"long request","request_report":"long request","ReviewRequestReport":"review body",
 "ReviewRejectReport":"reject body","CompleteReport":"c","RecoveryReport":"r","reject_report":"x",
 "spec":"S","plan":"P","content":"C","work_done":"w","now_possible":"n","how_to_verify":"v",
 "surprises":"s","needs_review":"nr","result_summary":"rs","Description":"d","body":"b",
 "question":"q","options":[{"label":"a"}],
 "entries":[{"id":1,"body":"long request","kind":"request"}],
 "history":{"entries":[{"id":1,"body":"long request"}],"has_more":false,"next_cursor":1},
 "has_more":false,"next_cursor":1}`

func receiptEnvelope(data string) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(data)); err != nil {
		panic(err)
	}
	data = compact.String()
	return `{"result":{"data":` + data + `,"next_step":[{"call":"x"}],"role":"executor","claim_evidence":{"scope":"task"}}}`
}

// startReceiptDaemon answers every call with handler(method).
func startReceiptDaemon(t *testing.T, handler func(method string) string) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socketPath := filepath.Join(dir, "d.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("Listen: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					Method string `json:"method"`
				}
				_ = json.Unmarshal(line, &req)
				_, _ = io.WriteString(conn, handler(req.Method)+"\n")
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		wg.Wait()
		os.RemoveAll(dir)
	})
	return NewClient(socketPath)
}

func callRaw(t *testing.T, c *Client, method string, params any) map[string]json.RawMessage {
	t.Helper()
	_, out, err := callWithUnappliedDecisions(context.Background(), c, method, params)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", encoded, err)
	}
	return got
}

func dataOf(t *testing.T, got map[string]json.RawMessage) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(got["data"], &data); err != nil {
		t.Fatalf("data %s: %v", got["data"], err)
	}
	return data
}

var blockedForTest = []string{
	"RequestReport", "request_report", "ReviewRequestReport", "ReviewRejectReport", "CompleteReport",
	"RecoveryReport", "reject_report", "spec", "plan", "content", "work_done", "now_possible",
	"how_to_verify", "surprises", "needs_review", "result_summary", "Description", "body", "question",
	"options", "entries", "history", "has_more", "next_cursor",
}

func TestReceiptMethodsDropBodiesAndKeepIDs(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string { return receiptEnvelope(fullHandoff) })
	for _, method := range receiptMethods {
		got := callRaw(t, c, method, map[string]any{})
		data := dataOf(t, got)
		for _, key := range blockedForTest {
			if _, ok := data[key]; ok {
				t.Errorf("%s: data still has %s", method, key)
			}
		}
		for _, key := range []string{"ID", "GoalID", "TaskID", "handoff_id", "task_id", "Status", "Kind", "RequestedBy", "RequestedAt", "ReceivedBy", "ReceivedAt"} {
			if _, ok := data[key]; !ok {
				t.Errorf("%s: data lost %s", method, key)
			}
		}
		if !strings.Contains(string(got["data"]), "9007199254740993") {
			t.Errorf("%s: id beyond 2^53 lost digits: %s", method, got["data"])
		}
		for _, key := range []string{"next_step", "role", "claim_evidence"} {
			if _, ok := got[key]; !ok {
				t.Errorf("%s: envelope lost %s", method, key)
			}
		}
	}
}

func TestDeliverMethodsKeepOnlyTheirReport(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string { return receiptEnvelope(fullHandoff) })
	for method, keep := range map[string]string{
		"task.handoff.review.receive":        "ReviewRequestReport",
		"goal.handoff.review.receive":        "ReviewRequestReport",
		"plan.handoff.review.receive":        "ReviewRequestReport",
		"task.handoff.review.reject.receive": "ReviewRejectReport",
		"goal.handoff.review.reject.receive": "ReviewRejectReport",
		"plan.handoff.review.reject.receive": "ReviewRejectReport",
	} {
		data := dataOf(t, callRaw(t, c, method, map[string]any{}))
		if _, ok := data[keep]; !ok {
			t.Errorf("%s: %s missing", method, keep)
		}
		for _, key := range blockedForTest {
			if _, ok := data[key]; ok && key != keep {
				t.Errorf("%s: data still has %s", method, key)
			}
		}
		if _, ok := data["ID"]; !ok {
			t.Errorf("%s: ID lost", method)
		}
	}
}

func TestPassthroughMethodsAreUnchanged(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string { return receiptEnvelope(fullHandoff) })
	var want bytes.Buffer
	if err := json.Compact(&want, []byte(fullHandoff)); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{
		"goal.get", "goal.list", "goal.sessions", "review.exchange.list", "handoff.entry.history",
		"goal.handoff.entry.history", "decision.poll", "decision.ask", "development.start",
		"session.discard", "session.discard.request", "unknown.method",
	} {
		if got := callRaw(t, c, method, map[string]any{}); string(got["data"]) != want.String() {
			t.Errorf("%s: data changed: %s", method, got["data"])
		}
	}
}

func TestShapeDataKeyCaseAndNesting(t *testing.T) {
	got := string(shapeData("task.update", json.RawMessage(
		`{"id":9007199254740993,"Task":{"Description":"d","title":"keep"},"list":[{"RequestReport":"x","n":1}]}`)))
	want := `{"Task":{"title":"keep"},"id":9007199254740993,"list":[{"n":1}]}`
	if got != want {
		t.Fatalf("shapeData = %s, want %s", got, want)
	}
}

func notice(id int, q string) string {
	b, _ := json.Marshal(map[string]any{"decision_id": id, "question": q})
	return string(b)
}

func TestUnappliedDecisionsOnlyWhenChanged(t *testing.T) {
	var mu sync.Mutex
	list := notice(1, "A") + "," + notice(2, "B")
	c := startReceiptDaemon(t, func(string) string {
		mu.Lock()
		defer mu.Unlock()
		if list == "" {
			return `{"result":{"data":{"ID":1}}}`
		}
		return `{"result":{"data":{"ID":1},"unapplied_decisions":[` + list + `]}}`
	})
	set := func(s string) { mu.Lock(); list = s; mu.Unlock() }
	include := map[string]any{"include_unapplied_answers": true}

	has := func(got map[string]json.RawMessage, key string) bool { _, ok := got[key]; return ok }

	got := callRaw(t, c, "task.update", include)
	if !has(got, "unapplied_decisions") || has(got, "unapplied_count") {
		t.Fatalf("first call = %v, want list without count", got)
	}
	got = callRaw(t, c, "task.update", include)
	if has(got, "unapplied_decisions") || string(got["unapplied_count"]) != "2" {
		t.Fatalf("same list = %v, want count 2 only", got)
	}
	set(notice(1, "A") + "," + notice(2, "B changed"))
	if got = callRaw(t, c, "task.update", include); !has(got, "unapplied_decisions") {
		t.Fatalf("question changed = %v, want list", got)
	}
	set(notice(1, "A"))
	if got = callRaw(t, c, "task.update", include); !has(got, "unapplied_decisions") {
		t.Fatalf("count dropped = %v, want list", got)
	}
	set(notice(3, "A"))
	if got = callRaw(t, c, "task.update", include); !has(got, "unapplied_decisions") {
		t.Fatalf("decision_id changed = %v, want list", got)
	}
	set("")
	if got = callRaw(t, c, "task.update", include); string(got["unapplied_decisions"]) != "[]" {
		t.Fatalf("became empty = %v, want []", got)
	}
	got = callRaw(t, c, "task.update", include)
	if has(got, "unapplied_decisions") || has(got, "unapplied_count") {
		t.Fatalf("still empty = %v, want neither", got)
	}
}

func TestUnappliedFirstEmptyAddsNothing(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string { return `{"result":{"data":{"ID":1}}}` })
	got := callRaw(t, c, "goal.list", map[string]any{"include_unapplied_answers": true})
	if _, ok := got["unapplied_decisions"]; ok {
		t.Fatalf("first empty = %v", got)
	}
}

func TestGoalListAlwaysFullAndUpdatesState(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string {
		return `{"result":{"data":{"ID":1},"unapplied_decisions":[` + notice(1, "A") + `]}}`
	})
	include := map[string]any{"include_unapplied_answers": true}
	for i := 0; i < 2; i++ {
		if got := callRaw(t, c, "goal.list", include); string(got["unapplied_decisions"]) == "" {
			t.Fatalf("goal.list call %d lacks the list: %v", i, got)
		}
	}
	// goal.list recorded the list, so a receipt now sees it as unchanged.
	got := callRaw(t, c, "task.update", include)
	if _, ok := got["unapplied_decisions"]; ok || string(got["unapplied_count"]) != "1" {
		t.Fatalf("after goal.list = %v, want count 1 only", got)
	}
}

func TestCallsWithoutIncludeDoNotTouchState(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string {
		return `{"result":{"data":{"ID":1},"unapplied_decisions":[` + notice(1, "A") + `]}}`
	})
	for i := 0; i < 2; i++ {
		got := callRaw(t, c, "task.update", map[string]any{})
		if _, ok := got["unapplied_decisions"]; !ok {
			t.Fatalf("call %d without include = %v, want list passed through", i, got)
		}
	}
	got := callRaw(t, c, "task.update", map[string]any{"include_unapplied_answers": true})
	if _, ok := got["unapplied_decisions"]; !ok {
		t.Fatalf("first include call = %v, want list (state untouched before)", got)
	}
}

func TestUnappliedStateIsRaceFree(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string {
		return `{"result":{"data":{"ID":1},"unapplied_decisions":[` + notice(1, "A") + `]}}`
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callRaw(t, c, "goal.list", map[string]any{"include_unapplied_answers": true})
		}()
	}
	wg.Wait()
}

// bareResponse is what the daemon returns for many methods: the object itself,
// with no data envelope, and next_step inside it.
func bareResponse(data string) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(data)); err != nil {
		panic(err)
	}
	return `{"result":` + strings.TrimSuffix(compact.String(), "}") + `,"next_step":[{"call":"x"}]}}`
}

func TestBareObjectResponsesAreShaped(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string { return bareResponse(fullHandoff) })
	for _, method := range []string{
		"goal.handoff.request", "task.handoff.review.reject", "plan.handoff.complete",
		"goal.update_request_report", "handoff.recover", "goal.release", "goal.withdraw",
		"task.create_handoff.receive", "goal.handoff.review.request", "task.handoff.complete",
	} {
		data := dataOf(t, callRaw(t, c, method, map[string]any{}))
		for _, key := range blockedForTest {
			if _, ok := data[key]; ok {
				t.Errorf("%s: bare data still has %s", method, key)
			}
		}
		for _, key := range []string{"ID", "GoalID", "TaskID", "handoff_id", "Status", "RequestedBy", "RequestedAt", "ReceivedAt", "next_step"} {
			if _, ok := data[key]; !ok {
				t.Errorf("%s: bare data lost %s", method, key)
			}
		}
	}
	for method, keep := range map[string]string{
		"task.handoff.review.reject.receive": "ReviewRejectReport",
		"goal.handoff.review.reject.receive": "ReviewRejectReport",
		"plan.handoff.review.reject.receive": "ReviewRejectReport",
		"task.handoff.review.receive":        "ReviewRequestReport",
	} {
		data := dataOf(t, callRaw(t, c, method, map[string]any{}))
		if _, ok := data[keep]; !ok {
			t.Errorf("%s: bare data lost %s", method, keep)
		}
		for _, key := range blockedForTest {
			if _, ok := data[key]; ok && key != keep {
				t.Errorf("%s: bare data still has %s", method, key)
			}
		}
	}
}

func TestBareSmallResponseIsHarmless(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string { return `{"result":{"ok":true}}` })
	for _, method := range []string{"task.update", "goal.get"} {
		if got := callRaw(t, c, method, map[string]any{}); string(got["data"]) != `{"ok":true}` {
			t.Errorf("%s: data = %s, want {\"ok\":true}", method, got["data"])
		}
	}
}
