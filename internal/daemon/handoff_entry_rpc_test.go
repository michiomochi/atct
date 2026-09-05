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
	var appended struct {
		EntryID         string `json:"entry_id"`
		HandoffID       string `json:"handoff_id"`
		Sequence        int64  `json:"sequence"`
		Kind            string `json:"kind"`
		Body            string `json:"body"`
		AuthorSessionID int64  `json:"author_session_id"`
	}
	if err := client.Call(ctx, "handoff.entry.append", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"kind":             "progress",
		"body":             "first progress",
		"agent_session_id": fixture.receiverID,
	}, &appended); err != nil {
		t.Fatalf("handoff.entry.append: %v", err)
	}
	if appended.EntryID == "" || appended.HandoffID != handoffID || appended.Sequence != 3 || appended.Kind != "progress" || appended.Body != "first progress" || appended.AuthorSessionID != fixture.receiverID {
		t.Fatalf("appended entry = %+v, want the third entry authored by receiver", appended)
	}

	var page struct {
		Entries []struct {
			Sequence int64  `json:"sequence"`
			Kind     string `json:"kind"`
			Body     string `json:"body"`
		} `json:"entries"`
		HasMore    bool  `json:"has_more"`
		NextCursor int64 `json:"next_cursor"`
	}
	if err := client.Call(ctx, "handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"cursor":           0,
		"limit":            2,
		"agent_session_id": fixture.receiverID,
	}, &page); err != nil {
		t.Fatalf("handoff.entry.history: %v", err)
	}
	if len(page.Entries) != 2 || !page.HasMore || page.NextCursor != 2 {
		t.Fatalf("history page = entries:%d has_more:%t next_cursor:%d, want 2/true/2", len(page.Entries), page.HasMore, page.NextCursor)
	}
	if page.Entries[0].Sequence != 1 || page.Entries[0].Kind != "request" || page.Entries[1].Sequence != 2 || page.Entries[1].Kind != "received" {
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
		"kind":             "question",
		"body":             "is this ready?",
		"agent_session_id": fixture.receiverID,
	}, &entry); err != nil {
		t.Fatalf("goal.handoff.entry.append: %v", err)
	}
	if entry["handoff_id"] != handoffID || entry["kind"] != "question" || entry["body"] != "is this ready?" {
		t.Fatalf("goal entry = %#v, want the appended question", entry)
	}

	var page map[string]any
	if err := client.Call(ctx, "goal.handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"goal_id":          fixture.claimedGoalID,
		"limit":            10,
		"agent_session_id": fixture.receiverID,
	}, &page); err != nil {
		t.Fatalf("goal.handoff.entry.history: %v", err)
	}
	if page["has_more"] != false || page["next_cursor"] != float64(3) {
		t.Fatalf("goal history page = %#v, want has_more=false next_cursor=3", page)
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
			var response struct {
				Entries []struct {
					Kind string `json:"kind"`
				} `json:"entries"`
				HasMore    bool  `json:"has_more"`
				NextCursor int64 `json:"next_cursor"`
			}
			if err := client.Call(ctx, tc.receive, params, &response); err != nil {
				t.Fatalf("%s: %v", tc.receive, err)
			}
			if len(response.Entries) != 2 || response.HasMore || response.NextCursor != 2 {
				t.Fatalf("receive page = entries:%d has_more:%t next_cursor:%d, want 2/false/2", len(response.Entries), response.HasMore, response.NextCursor)
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
		"kind":             "progress",
		"body":             "outsider write",
		"agent_session_id": fixture.claimableTaskID,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), store.ErrHandoffEntryParticipant.Error()) {
		t.Fatalf("outsider append error = %v, want participant authorization error", err)
	}

	err = client.Call(ctx, "handoff.entry.history", map[string]any{
		"handoff_id":       handoffID,
		"task_id":          fixture.claimedTaskID,
		"cursor":           -1,
		"limit":            1,
		"agent_session_id": fixture.receiverID,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), store.ErrHandoffHistoryCursorInvalid.Error()) {
		t.Fatalf("negative cursor error = %v, want cursor validation error", err)
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
			"kind":       "progress",
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
		"kind":       "progress",
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
