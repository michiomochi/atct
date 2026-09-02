package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func TestSSEBackfillsDurableDecisionWithStableID(t *testing.T) {
	f := newBareFixture(t)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID: f.goal.ID, Kind: domain.DecisionKind("question"), Question: "recover this rejection",
	})
	if err != nil {
		t.Fatal(err)
	}
	watermark, err := f.store.CurrentProjectEventSequence(f.ctx, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{DecisionID: decision.ID, AnswerLabel: "reject", AnswerText: "retry"}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	stream, reader := openSSEStream(t, f.ctx, srv.Client(), srv.URL+"/api/events?project_id="+strconv.FormatInt(f.project.ID, 10)+"&cursor="+strconv.FormatInt(watermark, 10)+"&watcher_key=reviewer-1")
	defer stream.Body.Close()

	frame := readSSEFrame(t, reader)
	if frame.event != "decision.answered" {
		t.Fatalf("backfilled event = %q, want decision.answered; lines=%v", frame.event, frame.lines)
	}
	wantID := strconv.FormatInt(f.project.ID, 10) + ":" + strconv.FormatInt(watermark+1, 10)
	if frame.id != wantID {
		t.Fatalf("backfilled event id = %q, want %q; lines=%v", frame.id, wantID, frame.lines)
	}
	var got domain.Decision
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("backfilled event data: %v", err)
	}
	if got.ID != decision.ID || got.AnswerText != "retry" {
		t.Fatalf("backfilled decision = %+v", got)
	}
}

func TestWorkflowReconcileAndCursorEndpoint(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/events/reconcile?project_id="+strconv.FormatInt(f.project.ID, 10)+"&goal_id="+strconv.FormatInt(f.goal.ID, 10), nil)
	if status != http.StatusOK {
		t.Fatalf("reconcile status = %d; body=%s", status, body)
	}
	var reconciliation struct {
		CurrentSequence int64         `json:"current_sequence"`
		Goals           []domain.Goal `json:"goals"`
	}
	if err := json.Unmarshal(body, &reconciliation); err != nil {
		t.Fatal(err)
	}
	if reconciliation.CurrentSequence == 0 || len(reconciliation.Goals) != 1 || reconciliation.Goals[0].ID != f.goal.ID {
		t.Fatalf("reconciliation = %+v", reconciliation)
	}

	request := mustJSON(t, map[string]any{
		"watcher_key": "reviewer-1",
		"project_id":  f.project.ID,
		"goal_id":     f.goal.ID,
		"sequence":    reconciliation.CurrentSequence,
	})
	status, _, body = doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/watch/cursor", request)
	if status != http.StatusOK {
		t.Fatalf("cursor status = %d; body=%s", status, body)
	}
	var cursor store.WatchDeliveryCursor
	if err := json.Unmarshal(body, &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.Sequence != reconciliation.CurrentSequence || cursor.WatcherKey != "reviewer-1" {
		t.Fatalf("cursor = %+v", cursor)
	}
}

func TestSSERejectsStaleDurableCursorWithBounds(t *testing.T) {
	f := newBareFixture(t)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID: f.goal.ID, Kind: domain.DecisionKind("question"), Question: "create retained bounds",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{DecisionID: decision.ID, AnswerLabel: "reject", AnswerText: "stale"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().ExecContext(f.ctx, "DELETE FROM workflow_event_outbox WHERE project_id = ? AND sequence < ?", f.project.ID, 3); err != nil {
		t.Fatalf("prune test events: %v", err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/events?project_id="+strconv.FormatInt(f.project.ID, 10)+"&cursor=1&watcher_key=reviewer-1", nil)
	if status != http.StatusGone {
		t.Fatalf("stale cursor status = %d; body=%s", status, body)
	}
	var response struct {
		Error           string `json:"error"`
		OldestSequence  int64  `json:"oldest_sequence"`
		CurrentSequence int64  `json:"current_sequence"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "stale_cursor" || response.OldestSequence != 3 || response.CurrentSequence < 3 {
		t.Fatalf("stale cursor response = %+v", response)
	}
}
