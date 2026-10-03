package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

type canonicalHandoffEntryRPCResponse struct {
	ID              int64     `json:"id"`
	HandoffID       string    `json:"handoff_id"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	AuthorSessionID int64     `json:"author_session_id"`
	InReplyToID     *int64    `json:"in_reply_to_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type canonicalHandoffEntryPageRPCResponse struct {
	Entries     []canonicalHandoffEntryRPCResponse `json:"entries"`
	HasMore     bool                               `json:"has_more"`
	NextAfterID int64                              `json:"next_after_id"`
}

type canonicalHandoffRPCResponse struct {
	Entries     []canonicalHandoffEntryRPCResponse   `json:"entries"`
	HasMore     bool                                 `json:"has_more"`
	NextAfterID int64                                `json:"next_after_id"`
	History     canonicalHandoffEntryPageRPCResponse `json:"history"`
}

func assertCanonicalHandoffEntryJSON(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode handoff entry JSON: %v", err)
	}
	for _, field := range []string{"entry_id", "sequence", "relates_to", "source"} {
		if _, ok := fields[field]; ok {
			t.Errorf("handoff entry JSON exposes removed field %q: %s", field, raw)
		}
	}
	var entry canonicalHandoffEntryRPCResponse
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decode canonical handoff entry: %v", err)
	}
	if entry.ID <= 0 {
		t.Errorf("handoff entry id = %d, want positive integer: %s", entry.ID, raw)
	}
}

func assertCanonicalHandoffEntryPageJSON(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode handoff history JSON: %v", err)
	}
	if _, ok := fields["next_cursor"]; ok {
		t.Errorf("handoff history JSON exposes removed cursor field: %s", raw)
	}
	var page canonicalHandoffEntryPageRPCResponse
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode canonical handoff history: %v", err)
	}
	for _, entry := range page.Entries {
		if entry.ID <= 0 {
			t.Errorf("history entry id = %d, want positive integer", entry.ID)
		}
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(fields["entries"], &entries); err != nil {
		t.Fatalf("decode history entries: %v", err)
	}
	for _, entry := range entries {
		assertCanonicalHandoffEntryJSON(t, entry)
	}
}

func assertCanonicalHandoffJSON(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode handoff JSON: %v", err)
	}
	if _, ok := fields["next_cursor"]; ok {
		t.Errorf("handoff JSON exposes removed cursor field: %s", raw)
	}
	if entries, ok := fields["entries"]; ok {
		var decoded []json.RawMessage
		if err := json.Unmarshal(entries, &decoded); err != nil {
			t.Fatalf("decode handoff entries: %v", err)
		}
		for _, entry := range decoded {
			assertCanonicalHandoffEntryJSON(t, entry)
		}
	}
	if history, ok := fields["history"]; ok {
		assertCanonicalHandoffEntryPageJSON(t, history)
	}
}

func TestTaskHandoffEntryRPCAppendsAndPagesHistory(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-task"
	if _, err := fixture.store.RequestTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.requesterID, "request"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	client := mcpshim.NewClient(fixture.socketPath)
	var appendedRaw json.RawMessage
	if err := client.Call(ctx, "handoff.entry.append", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"kind":             "review_requested",
		"body":             "first review",
		"in_reply_to_id":   1,
		"agent_session_id": fixture.receiverID,
	}, &appendedRaw); err != nil {
		t.Fatalf("handoff.entry.append: %v", err)
	}
	assertCanonicalHandoffEntryJSON(t, appendedRaw)
	var appended canonicalHandoffEntryRPCResponse
	if err := json.Unmarshal(appendedRaw, &appended); err != nil {
		t.Fatalf("decode appended entry: %v", err)
	}
	if appended.ID != 3 || appended.HandoffID != handoffID || appended.Kind != "review_requested" || appended.Body != "first review" || appended.AuthorSessionID != fixture.receiverID || appended.InReplyToID == nil || *appended.InReplyToID != 1 {
		t.Fatalf("appended entry = %+v, want the third entry authored by receiver replying to entry 1", appended)
	}

	var pageRaw json.RawMessage
	if err := client.Call(ctx, "handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"after_id":         0,
		"limit":            2,
		"agent_session_id": fixture.receiverID,
	}, &pageRaw); err != nil {
		t.Fatalf("handoff.entry.history: %v", err)
	}
	assertCanonicalHandoffEntryPageJSON(t, pageRaw)
	var page canonicalHandoffEntryPageRPCResponse
	if err := json.Unmarshal(pageRaw, &page); err != nil {
		t.Fatalf("decode history page: %v", err)
	}
	if len(page.Entries) != 2 || !page.HasMore || page.NextAfterID != 2 {
		t.Fatalf("history page = entries:%d has_more:%t next_after_id:%d, want 2/true/2", len(page.Entries), page.HasMore, page.NextAfterID)
	}
	if page.Entries[0].ID != 1 || page.Entries[0].Kind != "request" || page.Entries[1].ID != 2 || page.Entries[1].Kind != "received" {
		t.Fatalf("history entries = %+v, want request then received", page.Entries)
	}
}

func TestGoalHandoffEntryRPCAppendsAndPagesHistory(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-goal"
	if _, err := fixture.store.RequestGoalHandoff(ctx, handoffID, fixture.claimedGoalID, fixture.requesterID, "request"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveGoalHandoff(ctx, handoffID, fixture.claimedGoalID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	client := mcpshim.NewClient(fixture.socketPath)
	var entry map[string]any
	if err := client.Call(ctx, "goal.handoff.entry.append", map[string]any{
		"handoff_id":       handoffID,
		"goal_id":          fixture.claimedGoalID,
		"kind":             "review_rejected",
		"body":             "not ready yet",
		"agent_session_id": fixture.receiverID,
	}, &entry); err != nil {
		t.Fatalf("goal.handoff.entry.append: %v", err)
	}
	if entry["id"] != float64(3) || entry["handoff_id"] != handoffID || entry["kind"] != "review_rejected" || entry["body"] != "not ready yet" {
		t.Fatalf("goal entry = %#v, want the appended review rejection", entry)
	}
	encodedEntry, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("encode goal entry: %v", err)
	}
	assertCanonicalHandoffEntryJSON(t, encodedEntry)

	var page map[string]any
	if err := client.Call(ctx, "goal.handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"goal_id":          fixture.claimedGoalID,
		"after_id":         2,
		"limit":            10,
		"agent_session_id": fixture.receiverID,
	}, &page); err != nil {
		t.Fatalf("goal.handoff.entry.history: %v", err)
	}
	if page["has_more"] != false || page["next_after_id"] != float64(3) {
		t.Fatalf("goal history page = %#v, want has_more=false next_after_id=3", page)
	}
}

func TestHandoffReceiveRPCIncludesInitialHistoryPage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		request   string
		receive   string
		idField   string
		claimedID int64
		handoffID string
		requester int64
		receiver  int64
	}{
		{
			name: "task", request: "handoff.request", receive: "handoff.receive", idField: "task_id",
			handoffID: "rpc-entry-receive-task",
		},
		{
			name: "goal", request: "goal.handoff.request", receive: "goal.handoff.receive", idField: "goal_id",
			handoffID: "rpc-entry-receive-goal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var fixture struct {
				store       *store.Store
				socketPath  string
				claimedID   int64
				requesterID int64
				receiverID  int64
			}
			if tc.name == "task" {
				f := newTaskHandoffRPCTestFixture(t)
				fixture.store, fixture.socketPath, fixture.claimedID, fixture.requesterID, fixture.receiverID = f.store, f.socketPath, f.claimedTaskID, f.requesterID, f.receiverID
			} else {
				f := newGoalHandoffRPCTestFixture(t)
				fixture.store, fixture.socketPath, fixture.claimedID, fixture.requesterID, fixture.receiverID = f.store, f.socketPath, f.claimedGoalID, f.requesterID, f.receiverID
			}
			if tc.name == "task" {
				if _, err := fixture.store.RequestTaskHandoff(ctx, tc.handoffID, fixture.claimedID, fixture.requesterID, "initial request"); err != nil {
					t.Fatalf("RequestTaskHandoff: %v", err)
				}
			} else {
				if _, err := fixture.store.RequestGoalHandoff(ctx, tc.handoffID, fixture.claimedID, fixture.requesterID, "initial request"); err != nil {
					t.Fatalf("RequestGoalHandoff: %v", err)
				}
			}

			client := mcpshim.NewClient(fixture.socketPath)
			params := map[string]any{tc.idField: fixture.claimedID, "received_by": fixture.receiverID}
			params["handoff_id"] = tc.handoffID
			var responseRaw json.RawMessage
			if err := client.Call(ctx, tc.receive, params, &responseRaw); err != nil {
				t.Fatalf("%s: %v", tc.receive, err)
			}
			assertCanonicalHandoffJSON(t, responseRaw)
			var response canonicalHandoffRPCResponse
			if err := json.Unmarshal(responseRaw, &response); err != nil {
				t.Fatalf("decode receive response: %v", err)
			}
			if len(response.Entries) != 2 || response.HasMore || response.NextAfterID != 2 {
				t.Fatalf("receive page = entries:%d has_more:%t next_after_id:%d, want 2/false/2", len(response.Entries), response.HasMore, response.NextAfterID)
			}
			if response.Entries[0].Kind != store.HandoffEntryKindRequest || response.Entries[1].Kind != store.HandoffEntryKindReceived {
				t.Fatalf("receive entries = %+v, want request then received", response.Entries)
			}
		})
	}
}

func TestHandoffEntryRPCPreservesStoreAuthorizationAndCursorErrors(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-auth"
	if _, err := fixture.store.RequestTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.requesterID, "request"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	client := mcpshim.NewClient(fixture.socketPath)
	err := client.Call(ctx, "handoff.entry.append", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"kind":             "review_requested",
		"body":             "outsider write",
		"agent_session_id": fixture.claimableTaskID,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), store.ErrHandoffEntryParticipant.Error()) {
		t.Fatalf("outsider append error = %v, want participant authorization error", err)
	}
	for _, kind := range []string{"progress", "question", "answer", "amend", "system"} {
		err := client.Call(ctx, "handoff.entry.append", map[string]any{
			"handoff_id":       handoffID,
			"task_id":          fixture.claimedTaskID,
			"kind":             kind,
			"body":             "removed kind",
			"agent_session_id": fixture.receiverID,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), store.ErrHandoffEntryKindInvalid.Error()) {
			t.Errorf("removed kind %q error = %v, want kind validation error", kind, err)
		}
	}

	err = client.Call(ctx, "handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"after_id":         -1,
		"limit":            1,
		"agent_session_id": fixture.receiverID,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), store.ErrHandoffHistoryCursorInvalid.Error()) {
		t.Fatalf("negative cursor error = %v, want cursor validation error", err)
	}
}

func TestHandoffLifecycleRPCCreatesCanonicalReportEntries(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-lifecycle"
	client := mcpshim.NewClient(fixture.socketPath)

	var requestResponse json.RawMessage
	if err := client.Call(ctx, "handoff.request", map[string]any{
		"handoff_id":     handoffID,
		"task_id":        fixture.claimedTaskID,
		"requested_by":   fixture.requesterID,
		"request_report": "request report body",
	}, &requestResponse); err != nil {
		t.Fatalf("handoff.request: %v", err)
	}
	assertCanonicalHandoffJSON(t, requestResponse)

	var receiveResponse json.RawMessage
	if err := client.Call(ctx, "handoff.receive", map[string]any{
		"handoff_id":  handoffID,
		"task_id":     fixture.claimedTaskID,
		"received_by": fixture.receiverID,
	}, &receiveResponse); err != nil {
		t.Fatalf("handoff.receive: %v", err)
	}
	assertCanonicalHandoffJSON(t, receiveResponse)

	var completeResponse json.RawMessage
	if err := client.Call(ctx, "handoff.complete", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"complete_report":  "complete report body",
		"agent_session_id": fixture.receiverID,
	}, &completeResponse); err != nil {
		t.Fatalf("handoff.complete: %v", err)
	}
	assertCanonicalHandoffJSON(t, completeResponse)

	var historyRaw json.RawMessage
	if err := client.Call(ctx, "handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"after_id":         0,
		"limit":            10,
		"agent_session_id": fixture.receiverID,
	}, &historyRaw); err != nil {
		t.Fatalf("handoff.entry.history: %v", err)
	}
	assertCanonicalHandoffEntryPageJSON(t, historyRaw)
	var history canonicalHandoffEntryPageRPCResponse
	if err := json.Unmarshal(historyRaw, &history); err != nil {
		t.Fatalf("decode lifecycle history: %v", err)
	}
	if len(history.Entries) != 3 || history.Entries[0].Kind != "request" || history.Entries[0].Body != "request report body" || history.Entries[1].Kind != "received" || history.Entries[2].Kind != "completed" || history.Entries[2].Body != "complete report body" {
		t.Fatalf("lifecycle entries = %+v, want request/received/completed report bodies", history.Entries)
	}
}

func TestHandoffEntryRPCRejectsRemovedWireFields(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-removed-fields"
	if _, err := fixture.store.RequestTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.requesterID, "request"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	client := mcpshim.NewClient(fixture.socketPath)
	err := client.Call(ctx, "handoff.entry.append", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"kind":             "review_received",
		"body":             "reply",
		"relates_to":       "1",
		"agent_session_id": fixture.receiverID,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("removed relates_to error = %v, want unknown field rejection", err)
	}

	err = client.Call(ctx, "handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"cursor":           1,
		"limit":            1,
		"agent_session_id": fixture.receiverID,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("removed cursor error = %v, want unknown field rejection", err)
	}
}

func TestHandoffCapabilityIsShortLivedSingleUseAndConnectionBound(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-capability"
	if _, err := fixture.store.RequestTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.requesterID, "request"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	d := newDaemonWithClock(fixture.store, func() time.Time { return now })
	issue := func() string {
		raw, err := d.dispatch(ctx, rpcRequestForTest(t, "monitor.capability.issue", map[string]any{
			"agent_session_id": fixture.receiverID,
			"monitored":        true,
		}))
		if err != nil {
			t.Fatalf("monitor.capability.issue: %v", err)
		}
		var response struct {
			Capability string `json:"capability"`
			ExpiresAt  string `json:"expires_at"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatalf("decode capability: %v", err)
		}
		if response.Capability == "" || response.ExpiresAt == "" {
			t.Fatalf("capability response = %s, want token and expiry", raw)
		}
		return response.Capability
	}

	capability := issue()
	appendWithCapability := func(token string) error {
		_, err := d.dispatch(ctx, rpcRequestForTest(t, "handoff.entry.append", map[string]any{
			"handoff_id": handoffID,
			"task_id":    fixture.claimedTaskID,
			"kind":       "review_requested",
			"body":       "capability write",
			"capability": token,
		}))
		return err
	}
	if err := appendWithCapability(capability); err != nil {
		t.Fatalf("append with capability: %v", err)
	}
	if err := appendWithCapability(capability); err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("capability replay error = %v, want replay rejection", err)
	}

	expired := issue()
	now = now.Add(31 * time.Second)
	if err := appendWithCapability(expired); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired capability error = %v, want expiry rejection", err)
	}
}

func TestHandoffCompleteConsumesMonitoredCallerCapability(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-complete-capability"
	if _, err := fixture.store.RequestTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.requesterID, "request"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	d := newDaemonWithClock(fixture.store, time.Now)
	raw, err := d.dispatch(ctx, rpcRequestForTest(t, "monitor.capability.issue", map[string]any{
		"agent_session_id": fixture.receiverID,
		"monitored":        true,
	}))
	if err != nil {
		t.Fatalf("monitor.capability.issue: %v", err)
	}
	var capability struct {
		Capability string `json:"capability"`
	}
	if err := json.Unmarshal(raw, &capability); err != nil {
		t.Fatalf("decode capability: %v", err)
	}
	params := map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"complete_report":  "verified complete",
		"agent_session_id": fixture.receiverID,
		"capability":       capability.Capability,
	}
	if _, err := d.dispatch(ctx, rpcRequestForTest(t, "handoff.complete", params)); err != nil {
		t.Fatalf("handoff.complete with capability: %v", err)
	}
	if _, err := d.dispatch(ctx, rpcRequestForTest(t, "handoff.complete", params)); err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("handoff.complete replay error = %v, want replay rejection", err)
	}
}

func TestHandoffCapabilityRejectsAnotherUnixSocketConnection(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	const handoffID = "rpc-entry-peer-binding"
	if _, err := fixture.store.RequestTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.requesterID, "request"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.claimedTaskID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}

	first, err := net.Dial("unix", fixture.socketPath)
	if err != nil {
		t.Fatalf("dial first peer: %v", err)
	}
	defer first.Close()
	second, err := net.Dial("unix", fixture.socketPath)
	if err != nil {
		t.Fatalf("dial second peer: %v", err)
	}
	defer second.Close()

	issued := call(t, first, "monitor.capability.issue", map[string]any{
		"agent_session_id": fixture.receiverID,
		"monitored":        true,
	})
	if issued.Error != "" {
		t.Fatalf("monitor.capability.issue: %v", issued.Error)
	}
	var capability struct {
		Capability string `json:"capability"`
	}
	if err := json.Unmarshal(issued.Result, &capability); err != nil {
		t.Fatalf("decode capability: %v", err)
	}
	if capability.Capability == "" {
		t.Fatalf("capability response = %s, want opaque token", issued.Result)
	}

	params := map[string]any{
		"handoff_id": handoffID,
		"task_id":    fixture.claimedTaskID,
		"kind":       "review_requested",
		"body":       "peer-bound write",
		"capability": capability.Capability,
	}
	wrongPeer := call(t, second, "handoff.entry.append", params)
	if wrongPeer.Error == "" || !strings.Contains(wrongPeer.Error, "another socket peer") {
		t.Fatalf("wrong peer error = %q, want socket peer binding rejection", wrongPeer.Error)
	}
	rightPeer := call(t, first, "handoff.entry.append", params)
	if rightPeer.Error != "" {
		t.Fatalf("bound peer append: %v", rightPeer.Error)
	}
}

func rpcRequestForTest(t *testing.T, method string, params any) rpc.Request {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	return rpc.Request{Method: method, Params: raw}
}
