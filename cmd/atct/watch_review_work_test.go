package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/store"
)

func TestReviewWorkDurableDeliveryParityAcrossRestartIdleReconnect(t *testing.T) {
	nextTaskID := int64(8)
	work := store.OrchestrationReviewWork{
		ReviewWorkID:              "review-work-1224",
		ProjectID:                 3,
		GoalID:                    5,
		TaskID:                    int64Ptr(7),
		Kind:                      "task",
		HandoffID:                 "handoff-1224",
		State:                     store.OrchestrationReviewWorkStateCompleted,
		ReviewRequestedGeneration: "review-generation-1",
		SettlementGeneration:      "settlement-generation-1",
		Active:                    false,
		ActionRole:                "subcommander",
		ActionScopeKey:            "review-scope-1224",
		ActionTaskID:              &nextTaskID,
		ActionInstruction:         "hand off the next declared todo task and launch its executor monitor; leave it todo until received",
	}
	decision, ok := watchOrchestrationReviewWorkDecision(work)
	if !ok {
		t.Fatal("completed review work should produce a delivery decision")
	}
	line, ok := formatWatchDecision("orchestration.recovery", decision)
	if !ok {
		t.Fatal("review-work decision should format")
	}
	if line == "" {
		t.Fatal("formatted review-work decision should not be empty")
	}

	claudeAction, ok := selectWatchAgentAction("claude", "orchestration.recovery", decision)
	if !ok {
		t.Fatal("Claude selector should accept review-work recovery")
	}
	codexAction, ok := selectWatchAgentAction("codex", "orchestration.recovery", decision)
	if !ok {
		t.Fatal("Codex selector should accept review-work recovery")
	}
	if claudeAction.deliveryKey != codexAction.deliveryKey ||
		claudeAction.generation != codexAction.generation ||
		claudeAction.targetRole != codexAction.targetRole ||
		claudeAction.scopeKey != codexAction.scopeKey {
		t.Fatalf("Claude/Codex review-work selectors diverged: claude=%+v codex=%+v", claudeAction, codexAction)
	}

	claudeAPI := newFakeWatchDeliveryAPI()
	claudeScope := watchScope{
		ProjectID: "3",
		GoalID:    "5",
		Role:      "subcommander",
		ScopeKey:  "review-scope-1224",
	}
	var claudeDelivered []watchAgentAction
	claudeSink := newWatchDurableActionSink(
		claudeAPI,
		newWatchDeliveryTestReporter("claude-review-monitor", claudeScope.ScopeKey),
		claudeScope,
		func(action watchAgentAction) error {
			claudeDelivered = append(claudeDelivered, action)
			return nil
		},
	)
	claudeSink(claudeAction)
	claudeReconnectSink := newWatchDurableActionSink(
		claudeAPI,
		newWatchDeliveryTestReporter("claude-review-monitor", claudeScope.ScopeKey),
		claudeScope,
		func(action watchAgentAction) error {
			claudeDelivered = append(claudeDelivered, action)
			return nil
		},
	)
	claudeReconnectSink(claudeAction)
	if len(claudeAPI.receipts) != 1 {
		t.Fatalf("Claude restart/reconnect should persist one durable receipt, got %d", len(claudeAPI.receipts))
	}

	codexAPI := newFakeWatchDeliveryAPI()
	firstBridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-review-work")
	firstBridge.SetActive(true)
	codexSink := newWatchDurableCodexActionSink(
		codexAPI,
		newWatchDeliveryTestReporter("codex-review-monitor", claudeScope.ScopeKey),
		claudeScope,
		firstBridge,
	)
	codexSink(codexAction)
	if firstBridge.QueueLen() != 1 {
		t.Fatalf("Codex should queue review work before idle settlement, got %d", firstBridge.QueueLen())
	}
	if err := firstBridge.HandleNotification(context.Background(), codexAppServerNotification{
		Method: "turn/completed",
	}); err != nil {
		t.Fatalf("Codex idle settlement: %v", err)
	}

	reconnectedBridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-review-work")
	reconnectedBridge.SetActive(true)
	reconnectedSink := newWatchDurableCodexActionSink(
		codexAPI,
		newWatchDeliveryTestReporter("codex-review-monitor", claudeScope.ScopeKey),
		claudeScope,
		reconnectedBridge,
	)
	reconnectedSink(codexAction)
	if reconnectedBridge.QueueLen() != 0 {
		t.Fatalf("Codex reconnect should suppress the settled duplicate, got queue length %d", reconnectedBridge.QueueLen())
	}
}

func TestPlanReviewWorkCompletionDurableDeliveryAcrossReconnectAndUnknown(t *testing.T) {
	plan := store.OrchestrationReviewWork{
		ReviewWorkID:              "review_work:plan:plan-handoff-1224:settlement-1",
		ProjectID:                 3,
		GoalID:                    5,
		Kind:                      "plan",
		HandoffID:                 "plan-handoff-1224",
		State:                     store.OrchestrationReviewWorkStateCompleted,
		ReviewRequestedGeneration: "plan-review-generation-1",
		SettlementGeneration:      "plan-settlement-generation-1",
		ActionRole:                "subcommander",
		ActionScopeKey:            "goal:5:subcommander:goal-handoff-1224",
		ActionInstruction:         "plan handoff plan-handoff-1224 completed; resume the approved plan in the owning subcommander scope; do not auto-complete any task",
	}
	decision, ok := watchOrchestrationReviewWorkDecision(plan)
	if !ok {
		t.Fatal("completed plan review work should produce a delivery decision")
	}
	line, ok := formatWatchDecision("orchestration.recovery", decision)
	if !ok || line == "" {
		t.Fatal("completed plan review work should format as recovery")
	}
	claudeAction, ok := selectWatchAgentAction(line, "orchestration.recovery", decision)
	if !ok {
		t.Fatal("Claude selector should accept completed plan review work")
	}
	codexAction, ok := selectWatchAgentAction(line, "orchestration.recovery", decision)
	if !ok {
		t.Fatal("Codex selector should accept completed plan review work")
	}
	if claudeAction.deliveryKey != codexAction.deliveryKey ||
		claudeAction.generation != codexAction.generation ||
		claudeAction.targetRole != codexAction.targetRole ||
		claudeAction.scopeKey != codexAction.scopeKey {
		t.Fatalf("plan completion selector identity diverged: claude=%+v codex=%+v", claudeAction, codexAction)
	}

	scope := watchScope{
		ProjectID: "3",
		GoalID:    "5",
		Role:      "subcommander",
		ScopeKey:  plan.ActionScopeKey,
	}
	claudeAPI := newFakeWatchDeliveryAPI()
	var claudeDelivered []watchAgentAction
	collect := func(action watchAgentAction) error {
		claudeDelivered = append(claudeDelivered, action)
		return nil
	}
	claudeFirst := newWatchDurableActionSink(claudeAPI, newWatchDeliveryTestReporter("plan-claude-a", scope.ScopeKey), scope, collect)
	claudeSecond := newWatchDurableActionSink(claudeAPI, newWatchDeliveryTestReporter("plan-claude-b", scope.ScopeKey), scope, collect)
	claudeReconnect := newWatchDurableActionSink(claudeAPI, newWatchDeliveryTestReporter("plan-claude-a", scope.ScopeKey), scope, collect)
	if err := claudeFirst(claudeAction); err != nil {
		t.Fatalf("first Claude plan delivery: %v", err)
	}
	if err := claudeSecond(claudeAction); err != nil {
		t.Fatalf("second Claude wrapper plan delivery: %v", err)
	}
	if err := claudeReconnect(claudeAction); err != nil {
		t.Fatalf("reconnected Claude plan delivery: %v", err)
	}
	if len(claudeDelivered) != 1 {
		t.Fatalf("Claude plan completion should deliver once across wrappers/reconnect, got %d", len(claudeDelivered))
	}

	codexAPI := newFakeWatchDeliveryAPI()
	codexBridge := newCodexMonitorBridge(&fakeCodexTurnStarter{errs: []error{errCodexTurnSubmitUnknown}}, "plan-codex-thread")
	codexBridge.SetActive(true)
	codexSink := newWatchDurableCodexActionSink(codexAPI, newWatchDeliveryTestReporter("plan-codex", scope.ScopeKey), scope, codexBridge)
	if err := codexSink(codexAction); err != nil {
		t.Fatalf("queue Codex plan delivery: %v", err)
	}
	if err := codexBridge.HandleNotification(context.Background(), codexAppServerNotification{Method: "turn/completed", Params: mustJSON(map[string]any{"threadId": "plan-codex-thread"})}); err != nil {
		t.Fatalf("unknown Codex plan receipt: %v", err)
	}
	codexReconnectBridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "plan-codex-thread")
	codexReconnectBridge.SetActive(true)
	codexReconnectSink := newWatchDurableCodexActionSink(codexAPI, newWatchDeliveryTestReporter("plan-codex", scope.ScopeKey), scope, codexReconnectBridge)
	if err := codexReconnectSink(codexAction); err != nil {
		t.Fatalf("reconnected Codex plan delivery: %v", err)
	}
	if codexReconnectBridge.QueueLen() != 0 {
		t.Fatalf("unknown Codex plan receipt should suppress reconnect duplicate, got queue length %d", codexReconnectBridge.QueueLen())
	}
	codexAPI.mu.Lock()
	defer codexAPI.mu.Unlock()
	if codexAPI.reserveCall != 2 || codexAPI.acceptCall != 0 || codexAPI.unknownCall != 1 {
		t.Fatalf("Codex plan receipt calls reserve=%d accept=%d unknown=%d; want 2,0,1", codexAPI.reserveCall, codexAPI.acceptCall, codexAPI.unknownCall)
	}
}

func TestWatchReviewWorkDistinguishesReviewerWorkFromCompletionRedelegation(t *testing.T) {
	nextTaskID := int64(8)
	base := store.OrchestrationReviewWork{
		ReviewWorkID:              "review_work:task:handoff-1:generation-1",
		ProjectID:                 3,
		GoalID:                    5,
		TaskID:                    int64Ptr(7),
		Kind:                      "task",
		HandoffID:                 "handoff-1",
		ExpectedReviewerRole:      "subcommander",
		ReviewerScopeKey:          "goal:5:subcommander:goal-handoff-1",
		ReviewRequestedGeneration: "generation-1",
		State:                     store.OrchestrationReviewWorkStateRequested,
		Active:                    true,
	}
	requested, ok := watchOrchestrationReviewWorkDecision(base)
	if !ok || requested.TargetRole != "subcommander" || requested.ScopeKey != base.ReviewerScopeKey || requested.Condition != "review_work_requested" {
		t.Fatalf("requested review decision = %#v, want reviewer-scoped requested work", requested)
	}
	if !strings.Contains(requested.Instruction, "receive") || strings.Contains(strings.ToLower(requested.Instruction), "complete") {
		t.Fatalf("requested review instruction = %q, want receipt without completion", requested.Instruction)
	}

	receivedWork := base
	receivedWork.State = store.OrchestrationReviewWorkStateReceived
	receivedWork.ReviewReceivedGeneration = "generation-2"
	received, ok := watchOrchestrationReviewWorkDecision(receivedWork)
	if !ok || received.Condition != "review_work_received" || received.TargetRole != "subcommander" {
		t.Fatalf("received review decision = %#v, want unsettled reviewer work", received)
	}
	if !strings.Contains(received.Instruction, "accept") || !strings.Contains(received.Instruction, "reject") {
		t.Fatalf("received review instruction = %q, want explicit accept/reject", received.Instruction)
	}

	completedWork := base
	completedWork.Active = false
	completedWork.State = store.OrchestrationReviewWorkStateCompleted
	completedWork.SettlementGeneration = "generation-3"
	completedWork.ActionRole = "subcommander"
	completedWork.ActionScopeKey = base.ReviewerScopeKey
	completedWork.ActionTaskID = &nextTaskID
	completedWork.ActionInstruction = "task handoff handoff-1 completed; hand off declared todo task 8 to an executor, then launch its monitor"
	completed, ok := watchOrchestrationReviewWorkDecision(completedWork)
	if !ok || completed.Condition != "review_work_completed" || completed.TargetRole != "subcommander" || completed.ScopeKey != base.ReviewerScopeKey {
		t.Fatalf("completed review decision = %#v, want owner-scoped completion action", completed)
	}
	if strings.Contains(strings.ToLower(completed.Instruction), "auto-complete") || strings.Contains(strings.ToLower(completed.Instruction), "executor authority") {
		t.Fatalf("completed instruction grants forbidden authority: %q", completed.Instruction)
	}
}

func TestWatchReviewWorkUsesParityAndStableExactlyOnceIdentity(t *testing.T) {
	nextTaskID := int64(8)
	work := store.OrchestrationReviewWork{
		ReviewWorkID:              "review_work:task:handoff-1:generation-1",
		ProjectID:                 3,
		GoalID:                    5,
		TaskID:                    int64Ptr(7),
		Kind:                      "task",
		HandoffID:                 "handoff-1",
		ReviewRequestedGeneration: "generation-1",
		SettlementGeneration:      "generation-3",
		State:                     store.OrchestrationReviewWorkStateCompleted,
		ActionRole:                "subcommander",
		ActionScopeKey:            "goal:5:subcommander:goal-handoff-1",
		ActionTaskID:              &nextTaskID,
		ActionInstruction:         "hand off declared todo task 8 and launch its monitor",
	}
	decision, ok := watchOrchestrationReviewWorkDecision(work)
	if !ok {
		t.Fatal("completed review work did not produce a decision")
	}
	line, ok := formatWatchDecision("orchestration.recovery", decision)
	if !ok {
		t.Fatal("completed review work did not format as an agent action")
	}
	claudeAction, ok := selectWatchAgentAction(line, "orchestration.recovery", decision)
	if !ok {
		t.Fatal("Claude action selector suppressed completed review work")
	}
	codexAction, ok := selectWatchAgentAction(line, "orchestration.recovery", decision)
	if !ok {
		t.Fatal("Codex action selector suppressed completed review work")
	}
	if claudeAction.deliveryKey != codexAction.deliveryKey || claudeAction.generation != codexAction.generation || claudeAction.targetRole != codexAction.targetRole || claudeAction.scopeKey != codexAction.scopeKey {
		t.Fatalf("Claude/Codex action identity differs: Claude=%#v Codex=%#v", claudeAction, codexAction)
	}
	if claudeAction.targetRole != "subcommander" || claudeAction.scopeKey == "" || claudeAction.generation != "generation-3" {
		t.Fatalf("action identity = %#v, want fenced owner and settlement generation", claudeAction)
	}

	var out bytes.Buffer
	var actionCount int
	delivered := make(map[watchDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	lastWakeup := ""
	if err := emitWatchDecisionWithStateAndSinks(&out, "orchestration.recovery", decision, delivered, &lastWakeup, map[watchWakeupDeliveryKey]struct{}{}, detectionDelivered, nil, func(action watchAgentAction) error {
		actionCount++
		return nil
	}); err != nil {
		t.Fatalf("first review action emission: %v", err)
	}
	if err := emitWatchDecisionWithStateAndSinks(&out, "orchestration.recovery", decision, delivered, &lastWakeup, map[watchWakeupDeliveryKey]struct{}{}, detectionDelivered, nil, func(action watchAgentAction) error {
		actionCount++
		return nil
	}); err != nil {
		t.Fatalf("duplicate review action emission: %v", err)
	}
	if actionCount != 1 {
		t.Fatalf("review action count = %d, want exactly one for one lifecycle generation", actionCount)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
