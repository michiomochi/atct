package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func (f *flowFixture) nextStep(method string, params any) []nextStepOption {
	f.t.Helper()
	raw := f.call(method, params)
	var fields struct {
		NextStep []nextStepOption `json:"next_step"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		f.t.Fatalf("%s: decode next_step: %v", method, err)
	}
	if len(fields.NextStep) == 0 {
		f.t.Fatalf("%s returned no next_step", method)
	}
	return fields.NextStep
}

func calls(options []nextStepOption) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		out = append(out, option.Call)
	}
	return out
}

func wantCalls(t *testing.T, method string, got []nextStepOption, want ...string) {
	t.Helper()
	have := calls(got)
	if len(have) != len(want) {
		t.Fatalf("after %s next_step = %v, want exactly %v", method, have, want)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Fatalf("after %s next_step = %v, want %v", method, have, want)
		}
	}
}

// Walking the flow, every transition names what follows it.
func TestNextStepCoversEveryContinuingTransition(t *testing.T) {
	f := newFlowFixture(t)
	goal := f.newGoal("next step at each hop")

	wantCalls(t, "goal.handoff.request", f.nextStep("goal.handoff.request", goalHandoffRequestParams{
		HandoffID: "gh-1", GoalID: goal.ID, RequestedBy: f.commanderID, RequestReport: "deliver",
	}), "atct_goal_handoff_receive")

	wantCalls(t, "goal.handoff.receive", f.nextStep("goal.handoff.receive", goalHandoffReceiveParams{
		HandoffID: "gh-1", GoalID: goal.ID, ReceivedBy: f.subcommanderID,
	}), "atct_plan_handoff_review_request")

	if _, err := f.store.UpdateGoalRequestReport(f.ctx, goal.ID, "# Spec", "# Plan"); err != nil {
		t.Fatalf("UpdateGoalRequestReport: %v", err)
	}
	wantCalls(t, "plan.handoff.review.request", f.nextStep("plan.handoff.review.request", planHandoffReviewRequestParams{
		HandoffID: "gh-1", GoalID: goal.ID, RequestedBy: f.subcommanderID, ReviewRequestReport: "review",
	}), "atct_plan_handoff_review_receive")

	// A review offers both outcomes, not just the accepting one.
	wantCalls(t, "plan.handoff.review.receive", f.nextStep("plan.handoff.review.receive", planHandoffReviewReceiveParams{
		HandoffID: "gh-1", GoalID: goal.ID, ReceivedBy: f.commanderID,
	}), "atct_plan_handoff_complete", "atct_plan_handoff_review_reject")

	wantCalls(t, "plan.handoff.complete", f.nextStep("plan.handoff.complete", planHandoffCompleteParams{
		HandoffID: "gh-1", GoalID: goal.ID, AgentSessionID: f.commanderID, CompleteReport: "ok",
	}), "atct_task_create_handoff_receive")

	tasks := f.createTasks(goal.ID, "only task")
	wantCalls(t, "task.handoff.request", f.nextStep("task.handoff.request", taskHandoffRequestParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, RequestedBy: f.subcommanderID, RequestReport: "implement",
	}), "atct_task_handoff_receive")

	wantCalls(t, "task.handoff.receive", f.nextStep("task.handoff.receive", taskHandoffReceiveParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, ReceivedBy: f.executorID,
	}), "atct_task_handoff_review_request")

	wantCalls(t, "task.handoff.review.request", f.nextStep("task.handoff.review.request", taskHandoffReviewRequestParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, RequestedBy: f.executorID, ReviewRequestReport: "done",
	}), "atct_task_handoff_review_receive")

	wantCalls(t, "task.handoff.review.receive", f.nextStep("task.handoff.review.receive", taskHandoffReviewReceiveParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, ReceivedBy: f.subcommanderID,
	}), "atct_task_handoff_complete", "atct_task_handoff_review_reject")

	wantCalls(t, "task.handoff.complete", f.nextStep("task.handoff.complete", taskHandoffCompleteParams{
		HandoffID: "th-1", TaskID: tasks[0].ID, AgentSessionID: f.subcommanderID, CompleteReport: "ok",
	}), "atct_task_handoff_request", "atct_goal_handoff_review_request")

	wantCalls(t, "goal.handoff.review.request", f.nextStep("goal.handoff.review.request", goalHandoffReviewRequestParams{
		HandoffID: "gh-1", GoalID: goal.ID, RequestedBy: f.subcommanderID, ReviewRequestReport: "done",
	}), "atct_goal_handoff_review_receive")

	wantCalls(t, "goal.handoff.review.receive", f.nextStep("goal.handoff.review.receive", goalHandoffReviewReceiveParams{
		HandoffID: "gh-1", GoalID: goal.ID, ReceivedBy: f.commanderID,
	}), "atct_goal_review_request", "atct_goal_handoff_review_reject")
}

// Every branch must say which arm applies, or the caller has to look it up.
func TestNextStepBranchesCarryTheirCondition(t *testing.T) {
	for method, options := range nextStepAfter {
		if len(options) < 2 {
			continue
		}
		for i, option := range options {
			if strings.TrimSpace(option.When) == "" {
				t.Errorf("%s option %d (%s) has no condition", method, i, option.Call)
			}
		}
	}
}

// Single-option transitions must not invent a condition: there is nothing to
// choose between.
func TestNextStepSingleOptionsHaveNoCondition(t *testing.T) {
	for method, options := range nextStepAfter {
		if len(options) == 1 && strings.TrimSpace(options[0].When) != "" {
			t.Errorf("%s has one option but states a condition %q", method, options[0].When)
		}
	}
}

// Every option must name a real tool, and no entry may be empty.
func TestNextStepOptionsNameATool(t *testing.T) {
	for method, options := range nextStepAfter {
		if len(options) == 0 {
			t.Errorf("%s has an empty next_step", method)
		}
		for i, option := range options {
			if !strings.HasPrefix(option.Call, "atct_") {
				t.Errorf("%s option %d call = %q, want an atct tool name", method, i, option.Call)
			}
		}
	}
}

// A rejection must point back at the same handoff; the flow forbids a new one.
func TestNextStepAfterRejectionReusesTheHandoff(t *testing.T) {
	for _, method := range []string{
		"plan.handoff.review.reject.receive",
		"task.handoff.review.reject.receive",
		"goal.handoff.review.reject.receive",
	} {
		options := nextStepAfter[method]
		if len(options) != 1 {
			t.Fatalf("%s options = %v, want one", method, calls(options))
		}
		if !strings.Contains(options[0].Note, "same handoff_id") ||
			!strings.Contains(options[0].Note, "Do not create a new one") {
			t.Errorf("%s note = %q, want it to require reusing the handoff", method, options[0].Note)
		}
	}
}

// next_step must survive the shim envelope, which decodes named fields and
// drops anything it does not list.
func TestNextStepSurvivesTheShimEnvelope(t *testing.T) {
	raw := withNextStep(json.RawMessage(`{"data":{"id":"gh-1"},"role":"commander"}`),
		nextStepAfter["plan.handoff.review.receive"])
	var envelope struct {
		Data     json.RawMessage  `json:"data"`
		NextStep []nextStepOption `json:"next_step"`
		Role     string           `json:"role"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.NextStep) != 2 {
		t.Fatalf("next_step = %v, want both review outcomes", calls(envelope.NextStep))
	}
	if envelope.Role != "commander" || len(envelope.Data) == 0 {
		t.Fatalf("envelope lost data or role: %+v", envelope)
	}
}

func TestWithNextStepLeavesNonObjectsAlone(t *testing.T) {
	options := nextStepAfter["goal.handoff.request"]
	for _, raw := range []string{`[1,2]`, `"text"`, `null`, ``} {
		if got := string(withNextStep(json.RawMessage(raw), options)); got != raw {
			t.Fatalf("withNextStep(%q) = %q, want it unchanged", raw, got)
		}
	}
	if got := string(withNextStep(json.RawMessage(`{"a":1}`), nil)); got != `{"a":1}` {
		t.Fatalf("withNextStep with no next step = %q, want it unchanged", got)
	}
}
