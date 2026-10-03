package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/store"
)

type reviewExchangeBody struct {
	Exchanges []struct {
		Scope     string `json:"scope"`
		TaskID    int64  `json:"task_id"`
		Rejection struct {
			Source string `json:"source"`
			Reason string `json:"reason"`
		} `json:"rejection"`
		Response *struct {
			Report string `json:"report"`
		} `json:"response"`
	} `json:"exchanges"`
	Gaps []struct {
		HandoffID string `json:"handoff_id"`
	} `json:"gaps"`
}

func TestReviewExchangesEndpoints(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	project, err := db.CreateProject(ctx, "atct", filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	goal, err := db.CreateGoal(ctx, project.ID, "goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	blank, err := db.CreateGoal(ctx, project.ID, "blank", "human")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := db.CreateTasks(ctx, goal.ID, "a", "k", []string{"t"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	taskID := tasks[0].ID
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.DB().Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO task_handoffs (id, task_id) VALUES ('th', ?)`, taskID)
	exec(`INSERT INTO task_handoff_entries (handoff_id, kind, body, created_at) VALUES
('th', 'review_rejected', 'fix it', '2026-10-01T00:00:01.000000000Z'),
('th', 'review_requested', 'fixed', '2026-10-01T00:00:02.000000000Z')`)
	exec(`INSERT INTO decisions (goal_id, kind, question, status, answer_label, answer_text, answered_at, agent_session_id, created_at)
VALUES (?, 'goal_review', 'q', 'applied', 'reject', 'human no', '2026-10-01T00:00:03.000000000Z', 0, '2026-10-01T00:00:00.000000000Z')`, goal.ID)
	exec(`INSERT INTO handoff_history_gaps (handoff_id, scope) VALUES ('th', 'task')`)

	server := newTestServer(t, db)
	t.Cleanup(server.Close)
	get := func(path string) (int, []byte) {
		status, _, body := doRequest(t, server.Client(), http.MethodGet, server.URL+path, nil)
		return status, body
	}
	decode := func(body []byte) reviewExchangeBody {
		t.Helper()
		var out reviewExchangeBody
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return out
	}
	countRows := func() string {
		var n, m int
		_ = db.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM task_handoff_entries), (SELECT COUNT(*) FROM decisions)`).Scan(&n, &m)
		return fmt.Sprint(n, m)
	}
	before := countRows()

	status, body := get(fmt.Sprintf("/api/goals/%d/review-exchanges", goal.ID))
	if status != http.StatusOK {
		t.Fatalf("goal status = %d: %s", status, body)
	}
	goalView := decode(body)
	if len(goalView.Exchanges) != 2 || goalView.Exchanges[0].Scope != "task" || goalView.Exchanges[0].Response == nil || goalView.Exchanges[0].Response.Report != "fixed" ||
		goalView.Exchanges[1].Rejection.Source != "human" || goalView.Exchanges[1].Response != nil || len(goalView.Gaps) != 1 {
		t.Fatalf("goal view = %s", body)
	}

	status, body = get(fmt.Sprintf("/api/tasks/%d/review-exchanges", taskID))
	if status != http.StatusOK {
		t.Fatalf("task status = %d: %s", status, body)
	}
	if v := decode(body); len(v.Exchanges) != 1 || v.Exchanges[0].TaskID != taskID || len(v.Gaps) != 1 {
		t.Fatalf("task view = %s", body)
	}

	status, body = get(fmt.Sprintf("/api/goals/%d/review-exchanges", blank.ID))
	if status != http.StatusOK || !strings.Contains(string(body), `"exchanges":[]`) || !strings.Contains(string(body), `"gaps":[]`) {
		t.Fatalf("empty goal = %d %s, want [] arrays", status, body)
	}

	for _, path := range []string{"/api/goals/9999/review-exchanges", "/api/tasks/9999/review-exchanges"} {
		if status, body = get(path); status != http.StatusNotFound {
			t.Fatalf("%s = %d %s, want 404", path, status, body)
		}
	}
	if status, _, _ = doRequest(t, server.Client(), http.MethodPost, server.URL+fmt.Sprintf("/api/goals/%d/review-exchanges", goal.ID), []byte(`{}`)); status != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want 400", status)
	}
	if after := countRows(); after != before {
		t.Fatalf("reads changed rows: before %s after %s", before, after)
	}
}
