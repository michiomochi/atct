package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func TestSSEStreamsOnlyLiveEventsWithoutStableID(t *testing.T) {
	f := newBareFixture(t)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID: f.goal.ID, Kind: domain.DecisionKind("question"), Question: "live decision",
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	stream, reader := openSSEStream(t, f.ctx, srv.Client(), srv.URL+"/api/events?project_id="+strconv.FormatInt(f.project.ID, 10)+"&cursor=0&watcher_key=reviewer-1")
	defer stream.Body.Close()
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{DecisionID: decision.ID, AnswerLabel: "yes", AnswerText: "live"}); err != nil {
		t.Fatal(err)
	}

	frame := readSSEFrame(t, reader)
	if frame.event != "decision.answered" {
		t.Fatalf("live event = %q, want decision.answered; lines=%v", frame.event, frame.lines)
	}
	if frame.id != "" {
		t.Fatalf("live event id = %q, want no stable ID; lines=%v", frame.id, frame.lines)
	}
	var got domain.Decision
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("live event data: %v", err)
	}
	if got.ID != decision.ID || got.AnswerText != "live" {
		t.Fatalf("backfilled decision = %+v", got)
	}
}

func TestWatchCursorRouteIsAbsent(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/watch/cursor?project_id="+strconv.FormatInt(f.project.ID, 10)+"&watcher_key=reviewer-1", nil)
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		t.Fatalf("cursor route unexpectedly succeeded with status %d; body=%s", status, body)
	}
}

func TestWorkflowReconcileCanonicalEndpoint(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/events/reconcile?project_id="+strconv.FormatInt(f.project.ID, 10)+"&goal_id="+strconv.FormatInt(f.goal.ID, 10), nil)
	if status != http.StatusOK {
		t.Fatalf("reconcile status = %d; body=%s", status, body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"events", "oldest_sequence", "current_sequence", "high_watermark", "cursor"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("reconciliation contains delivery field %q: %s", field, body)
		}
	}
	var goals []domain.Goal
	if err := json.Unmarshal(fields["goals"], &goals); err != nil {
		t.Fatalf("decode goals: %v; body=%s", err, body)
	}
	if len(goals) != 1 || goals[0].ID != f.goal.ID {
		t.Fatalf("reconciliation goals = %+v", goals)
	}
	var decisions []domain.Decision
	if err := json.Unmarshal(fields["decisions"], &decisions); err != nil {
		t.Fatalf("decode decisions: %v; body=%s", err, body)
	}
	if len(decisions) != 0 {
		t.Fatalf("reconciliation decisions = %+v", decisions)
	}
}

func TestWorkflowReconcileMonitorWatermarkEmptyForNewBinding(t *testing.T) {
	f := newBareFixture(t)
	session, err := f.store.RegisterAgentSession(f.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	const token = "workflow-watermark-empty"
	if err := f.store.BindMonitorToken(f.ctx, token, session); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/events/reconcile?project_id="+strconv.FormatInt(f.project.ID, 10)+"&monitor_token="+token, nil)
	if status != http.StatusOK {
		t.Fatalf("reconcile status = %d; body=%s", status, body)
	}
	var response struct {
		MonitorLastReconciledAt *string `json:"monitor_last_reconciled_at"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.MonitorLastReconciledAt == nil || *response.MonitorLastReconciledAt != "" {
		t.Fatalf("monitor_last_reconciled_at = %v, want empty metadata", response.MonitorLastReconciledAt)
	}
}

func TestWorkflowReconcileMonitorWatermarkPreservedAfterRebinding(t *testing.T) {
	f := newBareFixture(t)
	firstSession, err := f.store.RegisterAgentSession(f.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := f.store.RegisterAgentSession(f.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	const token = "workflow-watermark-rebind"
	const watermark = "2026-09-19T00:00:00Z"
	if err := f.store.BindMonitorToken(f.ctx, token, firstSession); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().ExecContext(f.ctx, `UPDATE monitor_bindings SET last_reconciled_at = ? WHERE token = ?`, watermark, token); err != nil {
		t.Fatal(err)
	}
	if err := f.store.BindMonitorToken(f.ctx, token, secondSession); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/events/reconcile?project_id="+strconv.FormatInt(f.project.ID, 10)+"&monitor_token="+token, nil)
	if status != http.StatusOK {
		t.Fatalf("reconcile status = %d; body=%s", status, body)
	}
	var response struct {
		MonitorLastReconciledAt *string `json:"monitor_last_reconciled_at"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.MonitorLastReconciledAt == nil || *response.MonitorLastReconciledAt != watermark {
		t.Fatalf("monitor_last_reconciled_at = %v, want %q", response.MonitorLastReconciledAt, watermark)
	}
}

func TestSSEDoesNotRejectAStaleCursor(t *testing.T) {
	f := newBareFixture(t)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID: f.goal.ID, Kind: domain.DecisionKind("question"), Question: "live after stale cursor",
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	stream, reader := openSSEStream(t, f.ctx, srv.Client(), srv.URL+"/api/events?project_id="+strconv.FormatInt(f.project.ID, 10)+"&cursor=1&watcher_key=reviewer-1")
	defer stream.Body.Close()
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{DecisionID: decision.ID, AnswerLabel: "yes", AnswerText: "live"}); err != nil {
		t.Fatal(err)
	}
	frame := readSSEFrame(t, reader)
	if frame.event != "decision.answered" || frame.id != "" {
		t.Fatalf("live event after stale cursor = %+v, want wake-up event without ID", frame)
	}
}
