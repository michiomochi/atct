package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"unicode/utf8"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

func createGoalHandoffForHTTPTest(t *testing.T, f *fixture, handoffID string) (store.GoalHandoff, int64) {
	t.Helper()
	requester, err := f.store.RegisterAgentSession(f.ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(requester): %v", err)
	}
	receiver, err := f.store.RegisterAgentSession(f.ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(receiver): %v", err)
	}
	if _, err := f.store.ClaimProject(f.ctx, f.project.ID, requester); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	handoff, err := f.store.RequestGoalHandoff(f.ctx, handoffID, f.goal.ID, requester, "initial request")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := f.store.ReceiveGoalHandoff(f.ctx, handoffID, f.goal.ID, receiver); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	return handoff, receiver
}

func TestHTTPGoalHandoffHistoryIsReadOnlyAndCursorPaginated(t *testing.T) {
	f := newBareFixture(t)
	handoff, receiver := createGoalHandoffForHTTPTest(t, f, "http-goal-history")
	for _, entry := range []struct {
		kind string
		body string
	}{
		{kind: store.HandoffEntryKindProgress, body: "first progress"},
		{kind: store.HandoffEntryKindQuestion, body: "is this ready?"},
	} {
		if _, err := f.store.AppendGoalHandoffEntry(f.ctx, handoff.ID, entry.kind, entry.body, receiver, ""); err != nil {
			t.Fatalf("AppendGoalHandoffEntry(%s): %v", entry.kind, err)
		}
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	endpoint := srv.URL + "/api/goals/" + idText(f.goal.ID) + "/handoffs/" + handoff.ID + "?cursor=0&limit=2"
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, endpoint, nil)
	if status != http.StatusOK {
		t.Fatalf("handoff history status = %d; body=%s", status, body)
	}
	var first struct {
		ID            string               `json:"id"`
		Scope         string               `json:"scope"`
		GoalID        int64                `json:"goal_id"`
		RequestReport string               `json:"request_report"`
		Entries       []store.HandoffEntry `json:"entries"`
		HasMore       bool                 `json:"has_more"`
		NextCursor    int64                `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	if first.ID != handoff.ID || first.Scope != "goal" || first.GoalID != f.goal.ID || first.RequestReport != "initial request" {
		t.Fatalf("handoff detail = %+v", first)
	}
	if len(first.Entries) != 2 || first.Entries[0].Sequence != 1 || first.Entries[1].Sequence != 2 || !first.HasMore || first.NextCursor != 2 {
		t.Fatalf("first history page = %+v", first)
	}

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+idText(f.goal.ID)+"/handoffs/"+handoff.ID+"?cursor=2&limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("second history status = %d; body=%s", status, body)
	}
	var second struct {
		Entries    []store.HandoffEntry `json:"entries"`
		HasMore    bool                 `json:"has_more"`
		NextCursor int64                `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 2 || second.Entries[0].Sequence != 3 || second.Entries[1].Sequence != 4 || second.HasMore || second.NextCursor != 4 {
		t.Fatalf("second history page = %+v", second)
	}

	status, _, _ = doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/goals/"+idText(f.goal.ID)+"/handoffs/"+handoff.ID, nil)
	if status == http.StatusOK || status == http.StatusCreated {
		t.Fatalf("POST handoff history status = %d, want read-only rejection", status)
	}
}

func TestSSEPublishesAppendedHandoffEntryWithSafePreview(t *testing.T) {
	f := newBareFixture(t)
	handoff, receiver := createGoalHandoffForHTTPTest(t, f, "http-goal-events")
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURLWithGoal(srv.URL, idText(f.goal.ID)))
	defer stream.Body.Close()
	defer cancel()

	entry, err := f.store.AppendGoalHandoffEntry(f.ctx, handoff.ID, store.HandoffEntryKindProgress, "append through the store", receiver, "")
	if err != nil {
		t.Fatalf("AppendGoalHandoffEntry: %v", err)
	}
	frame := readSSEFrame(t, reader)
	if frame.event != httpapi.EventHandoffEntryAdded {
		t.Fatalf("SSE event = %q, want %q; lines=%v", frame.event, httpapi.EventHandoffEntryAdded, frame.lines)
	}
	if !containsLine(frame.lines, "id: "+entry.EntryID) {
		t.Fatalf("SSE entry id missing from lines=%v", frame.lines)
	}
	var got httpapi.HandoffEntryAddedEvent
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("SSE handoff event data: %v; data=%q", err, frame.data)
	}
	if got.ProjectID != f.project.ID || got.GoalID != f.goal.ID || got.HandoffID != handoff.ID || got.EntryID != entry.EntryID || got.Sequence != entry.Sequence || got.Kind != entry.Kind || got.AuthorSessionID != receiver {
		t.Fatalf("SSE handoff event = %+v", got)
	}
	if !utf8.ValidString(got.BodyPreview) || len([]byte(got.BodyPreview)) > httpapi.HandoffEntryPreviewMaxBytes {
		t.Fatalf("SSE body preview is not UTF-8-safe and bounded: bytes=%d value=%q", len([]byte(got.BodyPreview)), got.BodyPreview)
	}
}

func TestSSEHandoffEntryEventFiltersByGoalAndTask(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "http-event-filter", "http-event-filter", []string{"target", "other"}, []string{"target", "other"})
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Other HTTP event goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()

	goalCtx := f.ctx
	goalStream, goalReader := openSSEStream(t, goalCtx, srv.Client(), eventsURLWithGoal(srv.URL, idText(f.goal.ID)))
	defer goalStream.Body.Close()

	other := httpapi.HandoffEntryAddedEvent{ProjectID: f.project.ID, GoalID: otherGoal.ID, TaskID: tasks[1].ID, HandoffID: "other", EntryID: "other-entry", Sequence: 1, Kind: store.HandoffEntryKindProgress, BodyPreview: "other"}
	target := httpapi.HandoffEntryAddedEvent{ProjectID: f.project.ID, GoalID: f.goal.ID, TaskID: tasks[0].ID, HandoffID: "target", EntryID: "target-entry", Sequence: 1, Kind: store.HandoffEntryKindProgress, BodyPreview: "target"}
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: target})
	frame := readSSEFrame(t, goalReader)
	var got httpapi.HandoffEntryAddedEvent
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatal(err)
	}
	if got.EntryID != target.EntryID {
		t.Fatalf("goal-filtered event = %+v, want target", got)
	}

	taskQuery := srv.URL + "/api/events?task_id=" + idText(tasks[0].ID)
	taskStream, taskReader := openSSEStream(t, f.ctx, srv.Client(), taskQuery)
	defer taskStream.Body.Close()
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: target})
	frame = readSSEFrame(t, taskReader)
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatal(err)
	}
	if got.EntryID != target.EntryID {
		t.Fatalf("task-filtered event = %+v, want target", got)
	}
}

func TestWebSocketPublishesAppendedHandoffEntry(t *testing.T) {
	f := newBareFixture(t)
	handoff, receiver := createGoalHandoffForHTTPTest(t, f, "http-websocket-events")
	srv := newTestServer(t, f.store)
	defer srv.Close()
	conn := openWebSocket(t, websocketURL(srv.URL)+"?goal_id="+idText(f.goal.ID), nil)

	entry, err := f.store.AppendGoalHandoffEntry(f.ctx, handoff.ID, store.HandoffEntryKindProgress, "append through WebSocket", receiver, "")
	if err != nil {
		t.Fatalf("AppendGoalHandoffEntry: %v", err)
	}
	frame := readWebSocketFrame(t, conn)
	if frame.Name != httpapi.EventHandoffEntryAdded {
		t.Fatalf("WebSocket event = %q, want %q", frame.Name, httpapi.EventHandoffEntryAdded)
	}
	if frame.ID != entry.EntryID {
		t.Fatalf("WebSocket event id = %q, want %q", frame.ID, entry.EntryID)
	}
	var got httpapi.HandoffEntryAddedEvent
	if err := json.Unmarshal(frame.Data, &got); err != nil {
		t.Fatalf("WebSocket handoff event data: %v; data=%s", err, frame.Data)
	}
	if got.ProjectID != f.project.ID || got.GoalID != f.goal.ID || got.HandoffID != handoff.ID || got.EntryID != entry.EntryID || got.Sequence != entry.Sequence || got.Kind != entry.Kind || got.AuthorSessionID != receiver {
		t.Fatalf("WebSocket handoff event = %+v", got)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}
