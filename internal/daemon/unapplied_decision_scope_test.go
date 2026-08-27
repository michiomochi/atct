package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

type unappliedDecisionScopeRPCTestFixture struct {
	store                 *store.Store
	socketPath            string
	projectRoot           string
	projectID             int64
	goalAID               int64
	goalBID               int64
	decisionAID           int64
	decisionBID           int64
	commanderSessionID    int64
	subcommanderSessionID int64
}

func newUnappliedDecisionScopeRPCTestFixture(t *testing.T) unappliedDecisionScopeRPCTestFixture {
	t.Helper()
	dir, err := os.MkdirTemp(".", "atct-decision-scope-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	ctx := context.Background()
	projectRoot := filepath.Join(dir, "repo")
	project, err := s.CreateProject(ctx, "decision-scope", projectRoot)
	if err != nil {
		s.Close()
		t.Fatalf("CreateProject: %v", err)
	}
	goalA, err := s.CreateGoal(ctx, project.ID, "goal A", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal A: %v", err)
	}
	goalB, err := s.CreateGoal(ctx, project.ID, "goal B", "human")
	if err != nil {
		s.Close()
		t.Fatalf("CreateGoal B: %v", err)
	}
	commanderSessionID := daemonTestSessionID(t, s, "decision-scope-commander")
	subcommanderSessionID := daemonTestSessionID(t, s, "decision-scope-subcommander")
	for _, sessionID := range []int64{commanderSessionID, subcommanderSessionID} {
		if err := s.AssociateAgentSessionWithProject(ctx, sessionID, project.ID); err != nil {
			s.Close()
			t.Fatalf("AssociateAgentSessionWithProject(%v): %v", sessionID, err)
		}
	}
	if _, err := s.ClaimProject(ctx, project.ID, commanderSessionID); err != nil {
		s.Close()
		t.Fatalf("ClaimProject: %v", err)
	}
	if _, err := s.RequestGoalHandoff(ctx, "decision-scope-goal-handoff", goalA.ID, commanderSessionID, "delegate goal A"); err != nil {
		s.Close()
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, "decision-scope-goal-handoff", goalA.ID, subcommanderSessionID); err != nil {
		s.Close()
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	taskA, err := s.DeclareTasks(ctx, goalA.ID, "fixture", "decision-scope-task-a", []string{"task A"}, []string{"task A description"})
	if err != nil {
		s.Close()
		t.Fatalf("DeclareTasks A: %v", err)
	}
	taskB, err := s.DeclareTasks(ctx, goalB.ID, "fixture", "decision-scope-task-b", []string{"task B"}, []string{"task B description"})
	if err != nil {
		s.Close()
		t.Fatalf("DeclareTasks B: %v", err)
	}
	decisionA, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goalA.ID, TaskID: taskA[0].ID, Kind: domain.KindDecision,
		Question: "Question for goal A", AgentSessionID: daemonTestSessionID(t, s, "decision-scope-answer-a"),
	})
	if err != nil {
		s.Close()
		t.Fatalf("AskDecision A: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: decisionA.ID, AnswerText: "answer A"}); err != nil {
		s.Close()
		t.Fatalf("AnswerDecision A: %v", err)
	}
	decisionB, err := s.AskDecision(ctx, store.AskInput{
		GoalID: goalB.ID, TaskID: taskB[0].ID, Kind: domain.KindDecision,
		Question: "Question for goal B", AgentSessionID: daemonTestSessionID(t, s, "decision-scope-answer-b"),
	})
	if err != nil {
		s.Close()
		t.Fatalf("AskDecision B: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: decisionB.ID, AnswerText: "answer B"}); err != nil {
		s.Close()
		t.Fatalf("AnswerDecision B: %v", err)
	}

	socketPath := filepath.Join(dir, "daemon.sock")
	serveCtx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	d := New(s)
	go func() { serveDone <- d.Serve(serveCtx, socketPath) }()

	var conn net.Conn
	for i := 0; i < 100; i++ {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			break
		}
		select {
		case serveErr := <-serveDone:
			cancel()
			s.Close()
			t.Fatalf("daemon.Serve exited before socket appeared: %v", serveErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		cancel()
		s.Close()
		t.Fatalf("dial daemon: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
				t.Errorf("daemon.Serve: %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("daemon.Serve did not stop after cancellation")
		}
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("store.Close: %v", closeErr)
		}
	})

	return unappliedDecisionScopeRPCTestFixture{
		store:                 s,
		socketPath:            socketPath,
		projectRoot:           projectRoot,
		projectID:             project.ID,
		goalAID:               goalA.ID,
		goalBID:               goalB.ID,
		decisionAID:           decisionA.ID,
		decisionBID:           decisionB.ID,
		commanderSessionID:    commanderSessionID,
		subcommanderSessionID: subcommanderSessionID,
	}
}

func (f unappliedDecisionScopeRPCTestFixture) callRPC(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", f.socketPath)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	defer conn.Close()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("encode %v: %v", method, err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("decode %v: %v", method, err)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		t.Fatalf("RPC %v returned error: %v", method, response.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode %v result: %v", method, err)
	}
	return result
}

func notificationIDs(t *testing.T, result map[string]any) map[int64]bool {
	t.Helper()
	items, ok := result["unapplied_decisions"].([]any)
	if !ok {
		t.Fatalf("unapplied_decisions = %#v, want array", result["unapplied_decisions"])
	}
	ids := make(map[int64]bool, len(items))
	for _, item := range items {
		decision, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unapplied decision = %#v, want object", item)
		}
		id, ok := decision["decision_id"].(float64)
		if !ok {
			t.Fatalf("unapplied decision id = %#v, want number", decision["decision_id"])
		}
		ids[int64(id)] = true
	}
	return ids
}

func orphanedDecisionIDs(t *testing.T, result map[string]any) map[int64]bool {
	t.Helper()
	items, ok := result["orphaned_decisions"].([]any)
	if !ok {
		t.Fatalf("orphaned_decisions = %#v, want array", result["orphaned_decisions"])
	}
	ids := make(map[int64]bool, len(items))
	for _, item := range items {
		decision, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("orphaned decision = %#v, want object", item)
		}
		id, ok := decision["id"].(float64)
		if !ok {
			t.Fatalf("orphaned decision id = %#v, want number", decision["id"])
		}
		ids[int64(id)] = true
	}
	return ids
}

func responseData(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	data, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want object", result["data"])
	}
	return data
}

func assertDecisionSet(t *testing.T, got map[int64]bool, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decision IDs = %v, want %v", got, want)
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("decision IDs = %v, want %v", got, want)
		}
	}
}

func TestTaskDeclareForSubcommanderExcludesOtherGoalDecisions(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	result := f.callRPC(t, "task.declare", map[string]any{
		"goal_id":                   f.goalAID,
		"agent":                     "subcommander",
		"idempotency_key":           "rpc-subcommander-task",
		"titles":                    []string{"subcommander task"},
		"descriptions":              []string{"subcommander task description"},
		"agent_session_id":          f.subcommanderSessionID,
		"include_unapplied_answers": true,
	})
	assertDecisionSet(t, notificationIDs(t, result), f.decisionAID)
}

func TestGoalListForSubcommanderExcludesOtherGoalOrphanedDecisions(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	result := f.callRPC(t, "goal.list", map[string]any{
		"cwd":                       f.projectRoot,
		"agent_session_id":          f.subcommanderSessionID,
		"include_unapplied_answers": true,
	})
	data := responseData(t, result)
	assertDecisionSet(t, orphanedDecisionIDs(t, data), f.decisionAID)
	assertDecisionSet(t, notificationIDs(t, result), f.decisionAID)
}

func TestSessionRoleUnchangedAfterExtractingHelper(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	commander := f.callRPC(t, "session.role", map[string]any{"agent_session_id": f.commanderSessionID})
	if commander["role"] != "commander" || commander["project_id"] != float64(f.projectID) {
		t.Fatalf("commander role response = %#v", commander)
	}
	if _, ok := commander["goal_id"]; ok {
		t.Fatalf("commander role response unexpectedly contains goal_id: %#v", commander)
	}
	subcommander := f.callRPC(t, "session.role", map[string]any{"agent_session_id": f.subcommanderSessionID})
	if subcommander["role"] != "subcommander" || subcommander["goal_id"] != float64(f.goalAID) {
		t.Fatalf("subcommander role response = %#v", subcommander)
	}
	if _, ok := subcommander["project_id"]; ok {
		t.Fatalf("subcommander role response unexpectedly contains project_id: %#v", subcommander)
	}
}

func TestTaskDeclareForCommanderKeepsProjectWideDecisions(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	result := f.callRPC(t, "task.declare", map[string]any{
		"goal_id":                   f.goalAID,
		"agent":                     "commander",
		"idempotency_key":           "rpc-commander-task",
		"titles":                    []string{"commander task"},
		"descriptions":              []string{"commander task description"},
		"agent_session_id":          f.commanderSessionID,
		"include_unapplied_answers": true,
	})
	assertDecisionSet(t, notificationIDs(t, result), f.decisionAID, f.decisionBID)
}

func TestGoalListForCommanderKeepsProjectWideOrphanedDecisions(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	result := f.callRPC(t, "goal.list", map[string]any{
		"cwd":                       f.projectRoot,
		"agent_session_id":          f.commanderSessionID,
		"include_unapplied_answers": true,
	})
	data := responseData(t, result)
	assertDecisionSet(t, orphanedDecisionIDs(t, data), f.decisionAID, f.decisionBID)
}

func TestTaskDeclareWithoutSessionKeepsProjectWideDecisions(t *testing.T) {
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	result := f.callRPC(t, "task.declare", map[string]any{
		"goal_id":                   f.goalAID,
		"agent":                     "no-session",
		"idempotency_key":           "rpc-no-session-task",
		"titles":                    []string{"no-session task"},
		"descriptions":              []string{"no-session task description"},
		"include_unapplied_answers": true,
	})
	assertDecisionSet(t, notificationIDs(t, result), f.decisionAID, f.decisionBID)
}
