package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestReconcileWatchScopeRoutesCanonicalBlockerToCommanderOncePerGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events/reconcile" {
			t.Fatalf("request path = %q, want /api/events/reconcile", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"goals":[],"decisions":[],"goal_handoffs":[],"plan_handoffs":[],"task_handoffs":[],"expected_scopes":[],"monitor_health":[],"recoveries":[],"blockers":[{"blocker_id":"dependency_merge:main:abc:g1","project_id":1,"kind":"dependency_merge","source_id":"main:abc","generation":"g1","owner_role":"subcommander","scope_key":"goal:42:subcommander:h1","instruction":"integrate main abc before resuming goal 42"}]}`)
	}))
	defer server.Close()

	scope := watchScope{Role: "commander", ProjectID: "1", ScopeKey: "project:1:commander"}
	var output strings.Builder
	var actions []watchAgentAction
	actionSink := watchAgentActionSink(func(action watchAgentAction) error {
		actions = append(actions, action)
		return nil
	})
	delivered := make(map[watchDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	lastWakeup := ""
	if err := reconcileWatchScope(context.Background(), server.Client(), server.URL, scope, &output,
		delivered, &lastWakeup, make(map[watchWakeupDeliveryKey]struct{}), detectionDelivered,
		newWatchPassThroughFilter(), nil, actionSink); err != nil {
		t.Fatalf("first reconcileWatchScope: %v", err)
	}
	if err := reconcileWatchScope(context.Background(), server.Client(), server.URL, scope, &output,
		delivered, &lastWakeup, make(map[watchWakeupDeliveryKey]struct{}), detectionDelivered,
		newWatchPassThroughFilter(), nil, actionSink); err != nil {
		t.Fatalf("second reconcileWatchScope: %v", err)
	}

	if got, want := output.String(), "atct orchestration recovery: dependency_merge_wait (target_role commander, scope project:1:commander): integrate main abc before resuming goal 42\n"; got != want {
		t.Fatalf("blocker output = %q, want %q", got, want)
	}
	if len(actions) != 1 {
		t.Fatalf("blocker actions = %#v, want one action", actions)
	}
	if actions[0].eventName != "orchestration.recovery" || actions[0].targetRole != "commander" || actions[0].scopeKey != "project:1:commander" || actions[0].generation != "g1" {
		t.Fatalf("blocker action identity = %#v", actions[0])
	}
	if !strings.Contains(actions[0].deliveryKey, "dependency_merge:main:abc:g1") {
		t.Fatalf("blocker delivery key = %q, want blocker identity", actions[0].deliveryKey)
	}
}

func TestCanonicalBlockerDoesNotRouteToWorkerScope(t *testing.T) {
	blocker := watchOrchestrationBlocker{
		BlockerID:   "human_decision:17:g1",
		ProjectID:   1,
		Kind:        "human_decision",
		SourceID:    "17",
		Generation:  "g1",
		OwnerRole:   "subcommander",
		ScopeKey:    "goal:42:subcommander:h1",
		Instruction: "answer decision 17 before resuming",
	}
	if watchOrchestrationBlockerMatches(blocker, watchScope{Role: "subcommander", ProjectID: "1", GoalID: "42"}) {
		t.Fatal("canonical blocker matched worker scope; only commander should receive it")
	}
	if !watchOrchestrationBlockerMatches(blocker, watchScope{Role: "commander", ProjectID: "1", ScopeKey: "project:1:commander"}) {
		t.Fatal("canonical blocker did not match project commander scope")
	}
}

func TestCanonicalBlockerActionUsesStableGenerationAndSelector(t *testing.T) {
	blocker := watchOrchestrationBlocker{
		BlockerID:   "human_decision:17:g1",
		ProjectID:   1,
		Kind:        "human_decision",
		SourceID:    "17",
		Generation:  "g1",
		OwnerRole:   "commander",
		ScopeKey:    "project:1:commander",
		Instruction: "answer decision 17 before resuming",
	}
	decision := blocker.watchDecision()
	line, ok := formatWatchDecision("orchestration.recovery", decision)
	if !ok {
		t.Fatal("formatWatchDecision rejected canonical blocker recovery")
	}
	first, ok := selectWatchAgentAction(line, "orchestration.recovery", decision)
	if !ok {
		t.Fatal("canonical blocker recovery was not selected for agent delivery")
	}
	second, ok := selectWatchAgentAction(line, "orchestration.recovery", decision)
	if !ok || !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated blocker action identity changed: first=%#v second=%#v selected=%v", first, second, ok)
	}
	if first.generation != "g1" || first.targetRole != "commander" || first.scopeKey != "project:1:commander" {
		t.Fatalf("blocker action = %#v", first)
	}
}

func TestCanonicalBlockerUsesFencedDeliveryIdentity(t *testing.T) {
	blocker := watchOrchestrationBlocker{
		BlockerID:   "dependency_merge:main:abc:g1",
		ProjectID:   1,
		Kind:        "dependency_merge",
		SourceID:    "main:abc",
		Generation:  "g1",
		OwnerRole:   "subcommander",
		ScopeKey:    "goal:42:subcommander:h1",
		Instruction: "integrate main abc before resuming goal 42",
	}
	decision := blocker.watchDecision()
	line, ok := formatWatchDecision("orchestration.recovery", decision)
	if !ok {
		t.Fatal("formatWatchDecision rejected canonical blocker recovery")
	}
	action, ok := selectWatchAgentAction(line, "orchestration.recovery", decision)
	if !ok {
		t.Fatal("canonical blocker recovery was not selected")
	}

	api := newFakeWatchDeliveryAPI()
	var delivered []watchAgentAction
	sink := newWatchDurableActionSink(api, newWatchDeliveryTestReporter("commander-monitor", "project:1:commander"), watchScope{Role: "commander", ProjectID: "1"}, func(action watchAgentAction) error {
		delivered = append(delivered, action)
		return nil
	})
	if err := sink(action); err != nil {
		t.Fatalf("first fenced blocker delivery: %v", err)
	}
	if err := sink(action); err != nil {
		t.Fatalf("repeated fenced blocker delivery: %v", err)
	}
	if len(delivered) != 1 || !strings.Contains(delivered[0].deliveryKey, blocker.BlockerID) {
		t.Fatalf("fenced blocker deliveries = %#v, want one key containing %q", delivered, blocker.BlockerID)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.reserveCall != 2 || api.acceptCall != 1 {
		t.Fatalf("fenced blocker delivery calls reserve=%d accept=%d, want 2 and 1", api.reserveCall, api.acceptCall)
	}
}
