package daemon

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

func TestTaskCreateHandoffLifecycleOverRPC(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	client := mcpshim.NewClient(fixture.socketPath)
	ctx := context.Background()

	var goal store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{"handoff_id": "task-create-goal", "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID}, &goal); err != nil {
		t.Fatalf("goal.handoff.request: %v", err)
	}
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{"handoff_id": goal.ID, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID}, &goal); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}
	var plan store.PlanHandoff
	if err := client.Call(ctx, "plan.handoff.review.request", map[string]any{"handoff_id": "task-create-plan", "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID, "review_request_report": "ready"}, &plan); err != nil {
		t.Fatalf("plan.handoff.review.request: %v", err)
	}
	if err := client.Call(ctx, "plan.handoff.review.receive", map[string]any{"handoff_id": plan.ID, "goal_id": fixture.claimedGoalID, "received_by": fixture.requesterID}, &plan); err != nil {
		t.Fatalf("plan.handoff.review.receive: %v", err)
	}
	if err := client.Call(ctx, "plan.handoff.complete", map[string]any{"handoff_id": plan.ID, "goal_id": fixture.claimedGoalID, "agent_session_id": fixture.requesterID, "complete_report": "accepted"}, &plan); err != nil {
		t.Fatalf("plan.handoff.complete: %v", err)
	}
	handoff, err := fixture.store.GetTaskCreateHandoffForGoal(ctx, fixture.claimedGoalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal: %v", err)
	}
	decisionTasks, err := fixture.store.CreateTasks(ctx, fixture.claimedGoalID, "fixture", "task-create-decision", []string{"decision task"}, []string{"holds the decision returned to the task-create receiver"})
	if err != nil {
		t.Fatalf("CreateTasks decision task: %v", err)
	}
	decision, err := fixture.store.AskDecision(ctx, store.AskInput{GoalID: fixture.claimedGoalID, TaskID: decisionTasks[0].ID, Kind: domain.KindDecision, Question: "task create decision", AgentSessionID: fixture.requesterID})
	if err != nil {
		t.Fatalf("AskDecision task-create decision: %v", err)
	}
	if _, err := fixture.store.AnswerDecision(ctx, store.AnswerInput{DecisionID: decision.ID, AnswerText: "answer"}); err != nil {
		t.Fatalf("AnswerDecision task-create decision: %v", err)
	}
	foreignTasks, err := fixture.store.CreateTasks(ctx, fixture.unclaimedGoalID, "fixture", "foreign-task-create-decision", []string{"foreign decision task"}, []string{"must not appear in the task-create response"})
	if err != nil {
		t.Fatalf("CreateTasks foreign decision task: %v", err)
	}
	foreignDecision, err := fixture.store.AskDecision(ctx, store.AskInput{GoalID: fixture.unclaimedGoalID, TaskID: foreignTasks[0].ID, Kind: domain.KindDecision, Question: "foreign task create decision", AgentSessionID: fixture.requesterID})
	if err != nil {
		t.Fatalf("AskDecision foreign task-create decision: %v", err)
	}
	if _, err := fixture.store.AnswerDecision(ctx, store.AnswerInput{DecisionID: foreignDecision.ID, AnswerText: "answer"}); err != nil {
		t.Fatalf("AnswerDecision foreign task-create decision: %v", err)
	}

	var tasks []domain.Task
	if err := client.Call(ctx, "task.create", map[string]any{"goal_id": fixture.claimedGoalID, "agent_session_id": fixture.receiverID, "titles": []string{"implementation"}, "descriptions": []string{"implement"}, "idempotency_key": "task-create"}, &tasks); err == nil {
		t.Fatal("task.create without a received handoff succeeded")
	}
	foreignID, err := fixture.store.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(foreign): %v", err)
	}
	var received store.TaskCreateHandoff
	if err := client.Call(ctx, "task.create_handoff.receive", map[string]any{"handoff_id": handoff.ID, "received_by": foreignID}, &received); err == nil {
		t.Fatal("task.create_handoff.receive accepted a foreign receiver")
	}
	if err := client.Call(ctx, "task.create_handoff.receive", map[string]any{"handoff_id": handoff.ID, "received_by": fixture.receiverID}, &received); err != nil {
		t.Fatalf("task.create_handoff.receive: %v", err)
	}
	if received.ReceivedBy != fixture.receiverID {
		t.Fatalf("received handoff owner = %d, want %d", received.ReceivedBy, fixture.receiverID)
	}
	if err := client.Call(ctx, "task.create", map[string]any{"handoff_id": handoff.ID, "goal_id": fixture.claimedGoalID, "agent_session_id": foreignID, "agent": "worker", "titles": []string{"implementation"}, "descriptions": []string{"implement"}, "idempotency_key": "task-create"}, &tasks); err == nil {
		t.Fatal("task.create accepted a foreign receiver")
	}
	if err := client.Call(ctx, "task.create", map[string]any{"handoff_id": handoff.ID, "goal_id": fixture.claimedGoalID, "agent_session_id": fixture.receiverID, "agent": "worker", "titles": []string{"implementation"}, "descriptions": []string{"implement"}, "idempotency_key": "task-create"}, &tasks); err != nil {
		t.Fatalf("task.create raw response: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task.create raw tasks = %#v, want one", tasks)
	}
	if tasks[0].Created == nil || !*tasks[0].Created {
		t.Fatalf("task.create created = %#v, want true", tasks[0].Created)
	}
	var createResponse pendingResponseEnvelope
	if err := client.Call(ctx, "task.create", map[string]any{"handoff_id": handoff.ID, "goal_id": fixture.claimedGoalID, "agent_session_id": fixture.receiverID, "agent": "worker", "titles": []string{"implementation"}, "descriptions": []string{"implement"}, "idempotency_key": "task-create", "include_unapplied_answers": true}, &createResponse); err != nil {
		t.Fatalf("task.create: %v", err)
	}
	if len(createResponse.UnappliedDecisions) != 1 || createResponse.UnappliedDecisions[0].DecisionID != decision.ID {
		t.Fatalf("task.create unapplied decisions = %#v, want only %d (excluded %d)", createResponse.UnappliedDecisions, decision.ID, foreignDecision.ID)
	}
	if err := json.Unmarshal(createResponse.Data, &tasks); err != nil {
		t.Fatalf("decode task.create data: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task.create tasks = %#v, want one", tasks)
	}
	if tasks[0].Created == nil || *tasks[0].Created {
		t.Fatalf("task.create retry created = %#v, want false", tasks[0].Created)
	}
	completed, err := fixture.store.GetTaskCreateHandoffForGoal(ctx, fixture.claimedGoalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal after create: %v", err)
	}
	if completed.CompletedAt == nil || completed.CompletedBy != fixture.receiverID {
		t.Fatalf("completed handoff = %+v, want completed by %d without task delegation", completed, fixture.receiverID)
	}
}
