package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

type canonicalHandoffEntry struct {
	ID              int64  `json:"id"`
	HandoffID       string `json:"handoff_id"`
	Kind            string `json:"kind"`
	Body            string `json:"body"`
	AuthorSessionID int64  `json:"author_session_id"`
	InReplyToID     *int64 `json:"in_reply_to_id,omitempty"`
	CreatedAt       string `json:"created_at"`
}

type canonicalHandoffEvent struct {
	ProjectID       int64  `json:"project_id"`
	Scope           string `json:"scope"`
	GoalID          int64  `json:"goal_id"`
	TaskID          int64  `json:"task_id,omitempty"`
	HandoffID       string `json:"handoff_id"`
	ID              int64  `json:"id"`
	Kind            string `json:"kind"`
	AuthorSessionID int64  `json:"author_session_id"`
	InReplyToID     *int64 `json:"in_reply_to_id,omitempty"`
	BodyPreview     string `json:"body_preview"`
}

func decodeCanonicalHandoffEntries(t *testing.T, rawEntries []json.RawMessage) []canonicalHandoffEntry {
	t.Helper()
	entries := make([]canonicalHandoffEntry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("handoff entry is not an object: %v", err)
		}
		for _, removed := range []string{"entry_id", "sequence", "relates_to", "source"} {
			if _, ok := fields[removed]; ok {
				t.Fatalf("handoff entry exposes removed field %q: %s", removed, raw)
			}
		}
		var entry canonicalHandoffEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("decode canonical handoff entry: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func assertCanonicalHandoffEvent(t *testing.T, raw []byte) canonicalHandoffEvent {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("handoff event is not an object: %v", err)
	}
	for _, removed := range []string{"entry_id", "sequence", "relates_to", "source", "preview"} {
		if _, ok := fields[removed]; ok {
			t.Fatalf("handoff event exposes removed field %q: %s", removed, raw)
		}
	}
	var event canonicalHandoffEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode canonical handoff event: %v", err)
	}
	return event
}

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

func createTaskHandoffForHTTPTest(t *testing.T, f *fixture, handoffID string) (store.TaskHandoff, int64, int64) {
	t.Helper()
	tasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "http-task-handoff", handoffID, []string{"transport task"}, []string{"exercise the task handoff transport"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	_, goalReceiver := createGoalHandoffForHTTPTest(t, f, handoffID+"-parent")
	taskReceiver, err := f.store.RegisterAgentSession(f.ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(task receiver): %v", err)
	}
	handoff, err := f.store.RequestTaskHandoff(f.ctx, handoffID, tasks[0].ID, goalReceiver, "initial task request")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := f.store.ReceiveTaskHandoff(f.ctx, handoffID, tasks[0].ID, taskReceiver); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	return handoff, tasks[0].ID, taskReceiver
}

func TestHTTPGoalHandoffHistoryIsReadOnlyAndAfterIDPaginated(t *testing.T) {
	f := newBareFixture(t)
	handoff, receiver := createGoalHandoffForHTTPTest(t, f, "http-goal-history")
	initial, err := f.store.ListGoalHandoffEntries(f.ctx, handoff.ID, 0, 2)
	if err != nil {
		t.Fatalf("ListGoalHandoffEntries: %v", err)
	}
	for _, entry := range []struct {
		kind string
		body string
	}{
		{kind: store.HandoffEntryKindReviewRequested, body: "first review"},
		{kind: store.HandoffEntryKindReviewReceived, body: "review response"},
	} {
		relatesTo := ""
		if entry.kind == store.HandoffEntryKindReviewReceived {
			relatesTo = strconv.FormatInt(initial.Entries[0].ID, 10)
		}
		if _, err := f.store.AppendGoalHandoffEntry(f.ctx, handoff.ID, entry.kind, entry.body, receiver, relatesTo); err != nil {
			t.Fatalf("AppendGoalHandoffEntry(%s): %v", entry.kind, err)
		}
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	endpoint := srv.URL + "/api/goals/" + idText(f.goal.ID) + "/handoffs/" + handoff.ID + "?after_id=0&limit=2"
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, endpoint, nil)
	if status != http.StatusOK {
		t.Fatalf("handoff history status = %d; body=%s", status, body)
	}
	var first struct {
		ID            string            `json:"id"`
		Scope         string            `json:"scope"`
		GoalID        int64             `json:"goal_id"`
		RequestReport string            `json:"request_report"`
		Entries       []json.RawMessage `json:"entries"`
		HasMore       bool              `json:"has_more"`
		NextAfterID   int64             `json:"next_after_id"`
	}
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	if first.ID != handoff.ID || first.Scope != "goal" || first.GoalID != f.goal.ID || first.RequestReport != "initial request" {
		t.Fatalf("handoff detail = %+v", first)
	}
	firstEntries := decodeCanonicalHandoffEntries(t, first.Entries)
	if len(firstEntries) != 2 || firstEntries[0].ID != initial.Entries[0].ID || firstEntries[0].Kind != store.HandoffEntryKindRequest || firstEntries[1].ID != initial.Entries[1].ID || firstEntries[1].Kind != store.HandoffEntryKindReceived || !first.HasMore || first.NextAfterID != initial.Entries[1].ID {
		t.Fatalf("first history page = %+v", first)
	}

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+idText(f.goal.ID)+"/handoffs/"+handoff.ID+"?after_id="+strconv.FormatInt(initial.Entries[1].ID, 10)+"&limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("second history status = %d; body=%s", status, body)
	}
	var second struct {
		Entries     []json.RawMessage `json:"entries"`
		HasMore     bool              `json:"has_more"`
		NextAfterID int64             `json:"next_after_id"`
	}
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatal(err)
	}
	secondEntries := decodeCanonicalHandoffEntries(t, second.Entries)
	if len(secondEntries) != 2 || secondEntries[0].Kind != store.HandoffEntryKindReviewRequested || secondEntries[1].Kind != store.HandoffEntryKindReviewReceived || secondEntries[1].InReplyToID == nil || *secondEntries[1].InReplyToID != initial.Entries[0].ID || second.HasMore || second.NextAfterID != secondEntries[1].ID {
		t.Fatalf("second history page = %+v", second)
	}

	status, _, _ = doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/goals/"+idText(f.goal.ID)+"/handoffs/"+handoff.ID, nil)
	if status == http.StatusOK || status == http.StatusCreated {
		t.Fatalf("POST handoff history status = %d, want read-only rejection", status)
	}
}

func TestHTTPTaskHandoffHistoryUsesCanonicalAfterIDPage(t *testing.T) {
	f := newBareFixture(t)
	handoff, taskID, receiver := createTaskHandoffForHTTPTest(t, f, "http-task-history")
	initial, err := f.store.ListTaskHandoffEntries(f.ctx, handoff.ID, 0, 2)
	if err != nil {
		t.Fatalf("ListTaskHandoffEntries: %v", err)
	}
	entry, err := f.store.AppendTaskHandoffEntry(f.ctx, handoff.ID, store.HandoffEntryKindReviewRequested, "task review", receiver, "")
	if err != nil {
		t.Fatalf("AppendTaskHandoffEntry: %v", err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	endpoint := srv.URL + "/api/tasks/" + idText(taskID) + "/handoffs/" + handoff.ID + "?after_id=" + strconv.FormatInt(initial.Entries[1].ID, 10) + "&limit=2"
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, endpoint, nil)
	if status != http.StatusOK {
		t.Fatalf("task handoff history status = %d; body=%s", status, body)
	}
	var response struct {
		ID          string            `json:"id"`
		Scope       string            `json:"scope"`
		GoalID      int64             `json:"goal_id"`
		TaskID      int64             `json:"task_id"`
		Entries     []json.RawMessage `json:"entries"`
		HasMore     bool              `json:"has_more"`
		NextAfterID int64             `json:"next_after_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	entries := decodeCanonicalHandoffEntries(t, response.Entries)
	if response.ID != handoff.ID || response.Scope != "task" || response.GoalID != f.goal.ID || response.TaskID != taskID || len(entries) != 1 || entries[0].ID != entry.ID || entries[0].Kind != store.HandoffEntryKindReviewRequested || response.HasMore || response.NextAfterID != entry.ID {
		t.Fatalf("task handoff history = %+v entries=%+v", response, entries)
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

	entry, err := f.store.AppendGoalHandoffEntry(f.ctx, handoff.ID, store.HandoffEntryKindReviewRequested, "append through the store", receiver, "")
	if err != nil {
		t.Fatalf("AppendGoalHandoffEntry: %v", err)
	}
	frame := readSSEFrame(t, reader)
	if frame.event != httpapi.EventHandoffEntryAdded {
		t.Fatalf("SSE event = %q, want %q; lines=%v", frame.event, httpapi.EventHandoffEntryAdded, frame.lines)
	}
	if !containsLine(frame.lines, "id: "+strconv.FormatInt(entry.ID, 10)) {
		t.Fatalf("SSE entry id missing from lines=%v", frame.lines)
	}
	got := assertCanonicalHandoffEvent(t, []byte(frame.data))
	if got.ProjectID != f.project.ID || got.GoalID != f.goal.ID || got.HandoffID != handoff.ID || got.ID != entry.ID || got.Kind != entry.Kind || got.AuthorSessionID != receiver {
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

	other := map[string]any{"project_id": f.project.ID, "goal_id": otherGoal.ID, "task_id": tasks[1].ID, "handoff_id": "other", "id": int64(41), "kind": store.HandoffEntryKindReviewRequested, "body_preview": "other"}
	removed := map[string]any{"project_id": f.project.ID, "goal_id": f.goal.ID, "task_id": tasks[0].ID, "handoff_id": "removed", "id": int64(40), "kind": "progress", "body_preview": "removed"}
	target := map[string]any{"project_id": f.project.ID, "goal_id": f.goal.ID, "task_id": tasks[0].ID, "handoff_id": "target", "id": int64(42), "kind": store.HandoffEntryKindReviewRequested, "body_preview": "target"}
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: removed})
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: target})
	frame := readSSEFrame(t, goalReader)
	got := assertCanonicalHandoffEvent(t, []byte(frame.data))
	if got.ID != 42 {
		t.Fatalf("goal-filtered event = %+v, want target", got)
	}

	taskQuery := srv.URL + "/api/events?task_id=" + idText(tasks[0].ID)
	taskStream, taskReader := openSSEStream(t, f.ctx, srv.Client(), taskQuery)
	defer taskStream.Body.Close()
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: removed})
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: httpapi.EventHandoffEntryAdded, Data: target})
	frame = readSSEFrame(t, taskReader)
	got = assertCanonicalHandoffEvent(t, []byte(frame.data))
	if got.ID != 42 {
		t.Fatalf("task-filtered event = %+v, want target", got)
	}
}

func TestWebSocketPublishesAppendedHandoffEntry(t *testing.T) {
	f := newBareFixture(t)
	handoff, receiver := createGoalHandoffForHTTPTest(t, f, "http-websocket-events")
	srv := newTestServer(t, f.store)
	defer srv.Close()
	conn := openWebSocket(t, websocketURL(srv.URL)+"?goal_id="+idText(f.goal.ID), nil)

	entry, err := f.store.AppendGoalHandoffEntry(f.ctx, handoff.ID, store.HandoffEntryKindReviewRequested, "append through WebSocket", receiver, "")
	if err != nil {
		t.Fatalf("AppendGoalHandoffEntry: %v", err)
	}
	frame := readWebSocketFrame(t, conn)
	if frame.Name != httpapi.EventHandoffEntryAdded {
		t.Fatalf("WebSocket event = %q, want %q", frame.Name, httpapi.EventHandoffEntryAdded)
	}
	if frame.ID != strconv.FormatInt(entry.ID, 10) {
		t.Fatalf("WebSocket event id = %q, want %d", frame.ID, entry.ID)
	}
	got := assertCanonicalHandoffEvent(t, frame.Data)
	if got.ProjectID != f.project.ID || got.GoalID != f.goal.ID || got.HandoffID != handoff.ID || got.ID != entry.ID || got.Kind != entry.Kind || got.AuthorSessionID != receiver {
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
