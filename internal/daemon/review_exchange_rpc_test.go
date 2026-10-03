package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestReviewExchangeListRPC(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	project, err := s.CreateProject(ctx, "atct", filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateGoal(ctx, project.ID, "other", "human")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.CreateTasks(ctx, goal.ID, "a", "k", []string{"t"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	taskID := tasks[0].ID
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
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

	d := New(s)
	call := func(params map[string]any) (store.ReviewExchangeHistory, json.RawMessage, error) {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.dispatch(ctx, rpc.Request{Method: "review.exchange.list", Params: raw})
		var history store.ReviewExchangeHistory
		if err == nil {
			if jsonErr := json.Unmarshal(out, &history); jsonErr != nil {
				t.Fatalf("decode %s: %v", out, jsonErr)
			}
		}
		return history, out, err
	}
	rows := func() string {
		var n, m int
		_ = s.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM task_handoff_entries), (SELECT COUNT(*) FROM decisions)`).Scan(&n, &m)
		return fmt.Sprint(n, m)
	}
	before := rows()

	history, _, err := call(map[string]any{"goal_id": goal.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Exchanges) != 2 || history.Exchanges[0].Scope != "task" || history.Exchanges[0].Response == nil || history.Exchanges[0].Response.Report != "fixed" ||
		history.Exchanges[1].Rejection.Source != "human" || history.Exchanges[1].Response != nil || len(history.Gaps) != 1 {
		t.Fatalf("goal history = %+v", history)
	}
	// numeric strings are accepted like every other goal_id / task_id RPC
	history, _, err = call(map[string]any{"goal_id": fmt.Sprint(goal.ID), "task_id": fmt.Sprint(taskID)})
	if err != nil || len(history.Exchanges) != 1 || history.Exchanges[0].TaskID != taskID {
		t.Fatalf("task history = %+v, err %v", history, err)
	}

	_, raw, err := call(map[string]any{"goal_id": other.ID})
	if err != nil || !strings.Contains(string(raw), `"exchanges":[]`) || !strings.Contains(string(raw), `"gaps":[]`) {
		t.Fatalf("empty goal = %s, err %v, want [] arrays", raw, err)
	}
	if _, _, err = call(map[string]any{"goal_id": 9999}); err == nil {
		t.Fatal("missing goal succeeded")
	}
	if _, _, err = call(map[string]any{"goal_id": other.ID, "task_id": taskID}); !errors.Is(err, store.ErrTaskHandoffTaskMismatch) {
		t.Fatalf("foreign task error = %v, want ErrTaskHandoffTaskMismatch", err)
	}
	if _, _, err = call(map[string]any{"goal_id": goal.ID, "capability": "bogus"}); err == nil {
		t.Fatal("a bogus capability was accepted")
	}
	if after := rows(); after != before {
		t.Fatalf("reads changed rows: before %s after %s", before, after)
	}
}
