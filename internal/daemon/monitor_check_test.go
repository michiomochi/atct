package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func monitorCheckDecision(t *testing.T, fixture goalListFixture, sessionKey string) monitorCheckResponse {
	t.Helper()
	params, err := json.Marshal(map[string]any{"session_key": sessionKey})
	if err != nil {
		t.Fatalf("marshal session.monitor_check params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "session.monitor_check", Params: params})
	if err != nil {
		t.Fatalf("session.monitor_check: %v", err)
	}
	var response monitorCheckResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode session.monitor_check response: %v", err)
	}
	return response
}

// A session earns its Monitor scope by receiving a handoff, and the gate asks
// for a live Monitor before it lets any ATCT tool run. Gating a session that
// has no scope yet closes the only door into the loop: it can never receive,
// so it can never be assigned, so it can never have a Monitor.
func TestMonitorCheckAllowsASessionWithNoAssignmentYet(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	const sessionKey = "monitor-check-unassigned"
	sessionID := daemonTestSessionID(t, fixture.store, "monitor-check-unassigned")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, sessionID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}

	response := monitorCheckDecision(t, fixture, sessionKey)
	if response.Decision != "" {
		t.Fatalf("monitor_check denied a session with no assignment: %+v", response)
	}
}

// Once the session does hold a scope, a missing Monitor is a real problem: a
// wakeup addressed to that scope would never arrive.
func TestMonitorCheckStillDeniesAnAssignedSessionWithoutAMonitor(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	const sessionKey = "monitor-check-assigned"
	sessionID := daemonTestSessionID(t, fixture.store, "monitor-check-assigned")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, sessionID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, sessionID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	response := monitorCheckDecision(t, fixture, sessionKey)
	if response.Decision != "block" {
		t.Fatalf("monitor_check allowed a commander with no live Monitor: %+v", response)
	}
}

// The whole way in, end to end. A pane opens with nothing but a handoff id in
// its launch message: no claim, no assignment, no Monitor. It has to be able
// to receive, because receiving is what earns it the scope a Monitor binds to.
func TestAFreshSessionCanReceiveItsGoalHandoffAndBecomeSubcommander(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	goalID := fixture.active[0].ID

	// The commander registers the handoff before the worker pane exists.
	commanderID := daemonTestSessionID(t, fixture.store, "entry-commander")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	const handoffID = "goal-handoff-entry"
	if _, err := fixture.store.RequestGoalHandoff(ctx, handoffID, goalID, commanderID, "do the goal"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}

	// The pane opens and identifies itself. Nothing else is true of it yet.
	const sessionKey = "entry-subcommander"
	workerID := daemonTestSessionID(t, fixture.store, "entry-subcommander")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, workerID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	if assignment, err := fixture.store.MonitorAssignment(ctx, workerID); err != nil {
		t.Fatalf("MonitorAssignment: %v", err)
	} else if assignment.ProjectID != 0 {
		t.Fatalf("a session that has received nothing already has a scope: %+v", assignment)
	}

	// The gate must let it through: it holds no scope, so no Monitor could
	// exist for it, and no wakeup could be addressed to it either.
	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "" {
		t.Fatalf("the gate denied the call that would create the scope: %+v", response)
	}

	params, err := json.Marshal(map[string]any{
		"handoff_id":  handoffID,
		"goal_id":     goalID,
		"received_by": workerID,
	})
	if err != nil {
		t.Fatalf("marshal goal.handoff.receive params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "goal.handoff.receive", Params: params}); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}

	// Receiving is what makes it a subcommander, which is the scope its
	// Monitor binds to.
	assignment, err := fixture.store.MonitorAssignment(ctx, workerID)
	if err != nil {
		t.Fatalf("MonitorAssignment after receive: %v", err)
	}
	if assignment.Role != "subcommander" || assignment.GoalID != goalID || assignment.ProjectID != fixture.project.ID {
		t.Fatalf("assignment after receive = %+v, want subcommander on project %d goal %d", assignment, fixture.project.ID, goalID)
	}

	// And from here the gate does apply: the scope exists, so a missing
	// Monitor is a real problem again.
	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "block" {
		t.Fatalf("the gate stopped applying once the session held a scope: %+v", response)
	}
}

func addLiveMonitorForTest(t *testing.T, fixture goalListFixture, role string, projectID int64, goalID, taskID *int64) {
	t.Helper()
	addMonitorWithStateForTest(t, fixture, "healthy", role, projectID, goalID, taskID)
}

func addMonitorWithStateForTest(t *testing.T, fixture goalListFixture, state, role string, projectID int64, goalID, taskID *int64) {
	t.Helper()
	now := time.Now().UTC()
	health := store.MonitorHealth{
		CWD:              fixture.project.RootPath,
		Role:             role,
		State:            state,
		Reason:           "test",
		ProjectID:        projectID,
		GoalID:           goalID,
		TaskID:           taskID,
		PID:              os.Getpid(),
		ProcessStartedAt: now.Add(-time.Minute),
		TransitionedAt:   now,
		LastSeenAt:       now,
	}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
	if err := fixture.store.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatalf("UpsertMonitorHealth: %v", err)
	}
}

func TestMonitorCheckAllowsCommanderWithLiveMonitor(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	const sessionKey = "monitor-check-commander-live"
	sessionID := daemonTestSessionID(t, fixture.store, sessionKey)
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, sessionID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, sessionID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	addLiveMonitorForTest(t, fixture, "commander", fixture.project.ID, nil, nil)

	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "" {
		t.Fatalf("monitor_check denied commander with a live Monitor: %+v", response)
	}
}

func TestMonitorCheckAllowsSubcommanderWithLiveMonitor(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	goalID := fixture.active[0].ID
	commanderID := daemonTestSessionID(t, fixture.store, "monitor-check-subcommander-commander")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	if _, err := fixture.store.RequestGoalHandoff(ctx, "monitor-check-subcommander-goal", goalID, commanderID, "delegate goal"); err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}

	const sessionKey = "monitor-check-subcommander-live"
	sessionID := daemonTestSessionID(t, fixture.store, sessionKey)
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, sessionID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	params, err := json.Marshal(map[string]any{
		"handoff_id":  "monitor-check-subcommander-goal",
		"goal_id":     goalID,
		"received_by": sessionID,
	})
	if err != nil {
		t.Fatalf("marshal goal.handoff.receive params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "goal.handoff.receive", Params: params}); err != nil {
		t.Fatalf("goal.handoff.receive: %v", err)
	}
	addLiveMonitorForTest(t, fixture, "subcommander", fixture.project.ID, &goalID, nil)

	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "" {
		t.Fatalf("monitor_check denied subcommander with a live Monitor: %+v", response)
	}
}

func TestMonitorCheckAllowsExecutorWhenAnyAssignedGoalHasLiveMonitor(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	commanderID := daemonTestSessionID(t, fixture.store, "monitor-check-executor-commander")
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	secondGoal, err := fixture.store.CreateGoal(ctx, fixture.project.ID, "second monitor-check goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	secondTasks, err := fixture.store.CreateTasks(ctx, secondGoal.ID, "fixture-agent", "second-monitor-check-task", []string{"second task"}, []string{"second task"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}

	goalOneSubcommanderID := daemonTestSessionID(t, fixture.store, "monitor-check-executor-subcommander-one")
	goalTwoSubcommanderID := daemonTestSessionID(t, fixture.store, "monitor-check-executor-subcommander-two")
	for _, handoff := range []struct {
		id         string
		goalID     int64
		receivedBy int64
	}{
		{id: "monitor-check-executor-goal-one", goalID: fixture.taskGoal.ID, receivedBy: goalOneSubcommanderID},
		{id: "monitor-check-executor-goal-two", goalID: secondGoal.ID, receivedBy: goalTwoSubcommanderID},
	} {
		if _, err := fixture.store.RequestGoalHandoff(ctx, handoff.id, handoff.goalID, commanderID, "delegate goal"); err != nil {
			t.Fatalf("RequestGoalHandoff(%s): %v", handoff.id, err)
		}
		params, err := json.Marshal(map[string]any{
			"handoff_id":  handoff.id,
			"goal_id":     handoff.goalID,
			"received_by": handoff.receivedBy,
		})
		if err != nil {
			t.Fatalf("marshal goal.handoff.receive params: %v", err)
		}
		if _, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "goal.handoff.receive", Params: params}); err != nil {
			t.Fatalf("goal.handoff.receive(%s): %v", handoff.id, err)
		}
	}

	executorID := daemonTestSessionID(t, fixture.store, "monitor-check-executor-live")
	const sessionKey = "monitor-check-executor-live"
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, executorID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	for _, handoff := range []struct {
		id          string
		taskID      int64
		requestedBy int64
	}{
		{id: "monitor-check-executor-task-one", taskID: fixture.tasks[1].ID, requestedBy: goalOneSubcommanderID},
		{id: "monitor-check-executor-task-two", taskID: secondTasks[0].ID, requestedBy: goalTwoSubcommanderID},
	} {
		if _, err := fixture.store.RequestTaskHandoff(ctx, handoff.id, handoff.taskID, handoff.requestedBy, "delegate task"); err != nil {
			t.Fatalf("RequestTaskHandoff(%s): %v", handoff.id, err)
		}
		if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoff.id, handoff.taskID, executorID); err != nil {
			t.Fatalf("ReceiveTaskHandoff(%s): %v", handoff.id, err)
		}
	}

	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "block" {
		t.Fatalf("monitor_check allowed executor without a live assigned Monitor: %+v", response)
	}

	secondGoalID, secondTaskID := secondGoal.ID, secondTasks[0].ID
	addLiveMonitorForTest(t, fixture, "executor", fixture.project.ID, &secondGoalID, &secondTaskID)
	if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "" {
		t.Fatalf("monitor_check denied executor with one live assigned Monitor: %+v", response)
	}

	params, err := json.Marshal(map[string]any{"agent_session_id": executorID})
	if err != nil {
		t.Fatalf("marshal session.role params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "session.role", Params: params})
	if err != nil {
		t.Fatalf("session.role: %v", err)
	}
	var role executorRole
	if err := json.Unmarshal(raw, &role); err != nil {
		t.Fatalf("decode session.role: %v", err)
	}
	if role.Role != "executor" {
		t.Fatalf("session.role = %+v, want executor", role)
	}
}

// A `--once` watch ends in the middle of the agent's turn, so the rest of the
// turn's ATCT calls must pass until the grace runs out.
func TestMonitorCheckAllowsRearmingMonitorUntilGraceEnds(t *testing.T) {
	ctx := context.Background()
	for _, role := range []string{"subcommander", "executor"} {
		t.Run(role, func(t *testing.T) {
			fixture := newGoalListFixture(t)
			defer fixture.store.Close()

			goalID := fixture.active[0].ID
			if role == "executor" {
				goalID = fixture.taskGoal.ID
			}
			commanderID := daemonTestSessionID(t, fixture.store, "rearm-commander-"+role)
			if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
				t.Fatalf("ClaimProject: %v", err)
			}
			subID := daemonTestSessionID(t, fixture.store, "rearm-sub-"+role)
			if _, err := fixture.store.RequestGoalHandoff(ctx, "rearm-goal-"+role, goalID, commanderID, "delegate goal"); err != nil {
				t.Fatalf("RequestGoalHandoff: %v", err)
			}
			if _, err := fixture.store.ReceiveGoalHandoff(ctx, "rearm-goal-"+role, goalID, subID); err != nil {
				t.Fatalf("ReceiveGoalHandoff: %v", err)
			}
			sessionKey := "rearm-" + role
			sessionID := subID
			var taskID *int64
			if role == "executor" {
				sessionID = daemonTestSessionID(t, fixture.store, sessionKey)
				tid := fixture.tasks[1].ID
				taskID = &tid
				if _, err := fixture.store.RequestTaskHandoff(ctx, "rearm-task", tid, subID, "delegate task"); err != nil {
					t.Fatalf("RequestTaskHandoff: %v", err)
				}
				if _, err := fixture.store.ReceiveTaskHandoff(ctx, "rearm-task", tid, sessionID); err != nil {
					t.Fatalf("ReceiveTaskHandoff: %v", err)
				}
			}
			if _, _, err := fixture.store.IdentifyAgentSession(ctx, sessionID, sessionKey); err != nil {
				t.Fatalf("IdentifyAgentSession: %v", err)
			}

			addMonitorWithStateForTest(t, fixture, "rearming", role, fixture.project.ID, &goalID, taskID)
			if response := monitorCheckDecision(t, fixture, sessionKey); response.Decision != "" {
				t.Fatalf("monitor_check denied a %s right after --once ended: %+v", role, response)
			}

			original := store.MonitorRearmGrace
			store.MonitorRearmGrace = -time.Second
			defer func() { store.MonitorRearmGrace = original }()
			// A new row, because the grace is applied when the state is stored.
			fixture.store.DB().ExecContext(ctx, `DELETE FROM monitor_health`)
			addMonitorWithStateForTest(t, fixture, "rearming", role, fixture.project.ID, &goalID, taskID)
			response := monitorCheckDecision(t, fixture, sessionKey)
			if response.Decision != "block" {
				t.Fatalf("monitor_check allowed a %s after the grace ended: %+v", role, response)
			}
			for _, want := range []string{"## Watch", "atct watch --monitor --token"} {
				if !strings.Contains(response.Reason, want) {
					t.Fatalf("block reason %q does not mention %q", response.Reason, want)
				}
			}
		})
	}
}
