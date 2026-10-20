package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

// An answer or a goal-review reject recorded while no watch was running must
// reach the next `--once` watch as soon as it starts.
func TestWatchGapAnswerReachesFreshOnceWatch(t *testing.T) {
	p := startCommanderProject(t)
	defer p.cancel()
	var goal domain.Goal
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.create", map[string]any{"cwd": p.root, "content": "gap", "creator": "human"}, &goal)
	callWorkflowMonitorRPC(t, p.ctx, p.client, "decision.ask", map[string]any{
		"goal_id": goal.ID, "question": "Q?", "options": []domain.Option{{Label: "A"}, {Label: "B"}}, "agent_session_id": p.commander,
	}, &map[string]any{})
	d := p.openDecisionFor(t, goal.ID)

	// no watch running: the gap
	body, _ := json.Marshal(map[string]string{"answer_label": "A"})
	resp, err := p.server.Client().Post(fmt.Sprintf("%s/api/decisions/%d/answer", p.server.URL, d.ID), "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("answer: %v %v", err, resp)
	}
	resp.Body.Close()

	w := startBoundLoop(p.ctx, p.server, p.server.URL, t.TempDir(), p.root, bindToken, true)
	defer w.stop(t)
	w.waitOutput(t, fmt.Sprintf("atct decision answered (decision_id: %d)", d.ID))
}

func TestWatchGapGoalReviewRejectReachesFreshOnceWatch(t *testing.T) {
	p := startCommanderProject(t)
	defer p.cancel()
	var goal domain.Goal
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.create", map[string]any{"cwd": p.root, "content": "gap", "creator": "human"}, &goal)
	sub := registerWorkflowMonitorSession(t, p.ctx, p.client, p.root, "measure-sub", "measure-sub-token")
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.handoff.request", map[string]any{"handoff_id": "gh1", "goal_id": goal.ID, "requested_by": p.commander, "request_report": "x"}, &map[string]any{})
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.handoff.receive", map[string]any{"handoff_id": "gh1", "goal_id": goal.ID, "received_by": sub}, &map[string]any{})
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.handoff.review.request", map[string]any{"handoff_id": "gh1", "goal_id": goal.ID, "requested_by": sub, "review_request_report": "r"}, &map[string]any{})
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.handoff.review.receive", map[string]any{"handoff_id": "gh1", "goal_id": goal.ID, "received_by": p.commander}, &map[string]any{})
	var review domain.Decision
	callWorkflowMonitorRPC(t, p.ctx, p.client, "goal.review.request", map[string]any{
		"goal_id": goal.ID, "agent_session_id": p.commander, "work_done": "d", "now_possible": "n", "how_to_verify": "h", "surprises": "なし", "needs_review": "なし", "next_goal_ids": []int64{},
	}, &review)

	// no watch running: the gap. The human rejects.
	body, _ := json.Marshal(map[string]string{"reason": "no"})
	resp, err := p.server.Client().Post(fmt.Sprintf("%s/api/decisions/%d/reject", p.server.URL, review.ID), "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: %v %v", err, resp)
	}
	resp.Body.Close()

	w := startBoundLoop(p.ctx, p.server, p.server.URL, t.TempDir(), p.root, bindToken, true)
	defer w.stop(t)
	w.waitOutput(t, fmt.Sprintf("atct goal review rejected (goal_id: %d, decision_id: %d)", goal.ID, review.ID))
}
