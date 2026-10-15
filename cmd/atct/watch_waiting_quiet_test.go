package main

import (
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// A subcommander whose only open item is a goal review the commander has not
// answered is waiting, not stuck. Its watch must stay quiet for hours: the
// liveness prompt would wake it every time it expired, and the answer arrives
// on the --once watch as soon as it exists.
func TestWatchWaitingQuietForGoalReviewForFiveHours(t *testing.T) {
	scope := watchScope{ProjectID: "1", Role: "subcommander", GoalID: "7"}
	snapshot := watchReconciliation{
		Goals: []watchReconciliationGoal{{ID: "7", Status: "active"}},
		GoalHandoffs: []watchReconciliationHandoff{{
			ID:                "goal-7-handoff",
			GoalID:            7,
			RequestedAt:       stringPtr("2026-10-14T10:00:00.000000000Z"),
			ReceivedAt:        stringPtr("2026-10-14T10:01:00.000000000Z"),
			ReviewRequestedAt: stringPtr("2026-10-14T10:30:00.000000000Z"),
		}},
	}
	start := time.Now()
	state := newWatchLivenessState(start)
	for minute := 1; minute <= 5*60; minute++ {
		if line, ok := state.PromptDue(start.Add(time.Duration(minute)*time.Minute), scope, snapshot); ok {
			t.Fatalf("minute %d prompted a subcommander that is only waiting: %q", minute, line)
		}
	}

	// A rejection is news: the same handoff now has work for the subcommander.
	rejected := snapshot
	rejected.GoalHandoffs = []watchReconciliationHandoff{snapshot.GoalHandoffs[0]}
	rejected.GoalHandoffs[0].ReviewRejectedAt = stringPtr("2026-10-14T15:01:00.000000000Z")
	if _, ok := state.PromptDue(start.Add(5*time.Hour+time.Minute), scope, rejected); !ok {
		t.Fatal("a goal review rejection after the quiet wait must be sent")
	}
}

// The same wait end to end, through the daemon. A subcommander waiting for a
// goal review keeps a --once watch quiet; the commander's rejection arrives
// while no watch runs; a fresh --once watch then delivers it.
func TestWatchWaitingQuietEndToEnd(t *testing.T) {
	ctx, cancel, db, server, client, root := startWorkflowMonitorDaemon(t)
	defer cancel()
	const handoffID = "gh-watch-waiting-e2e"
	const subToken = "watch-waiting-e2e-sub-token"
	commanderID := registerWorkflowMonitorSession(t, ctx, client, root, "watch-waiting-e2e-commander", "watch-waiting-e2e-commander-token")
	projects, err := db.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = (%#v, %v), want one project", projects, err)
	}
	callWorkflowMonitorRPC(t, ctx, client, "project.claim", map[string]any{
		"project_id": projects[0].ID, "agent_session_id": commanderID,
	}, &domain.Project{})
	var goal domain.Goal
	callWorkflowMonitorRPC(t, ctx, client, "goal.create", map[string]any{
		"cwd": root, "content": "waiting for a goal review", "creator": "human",
	}, &goal)
	subID := registerWorkflowMonitorSession(t, ctx, client, root, "watch-waiting-e2e-sub", subToken)
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.request", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "requested_by": commanderID,
		"request_report": "goal ready for implementation",
	}, &store.GoalHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.receive", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "received_by": subID,
	}, &store.GoalHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.review.request", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "requested_by": subID,
		"review_request_report": "goal is ready for review",
	}, &store.GoalHandoff{})

	// (a) waiting for the commander's answer: the --once watch stays quiet.
	waiting := startBoundLoop(ctx, server, server.URL, t.TempDir(), root, subToken, true)
	waiting.assertQuiet(t, 2*time.Second)
	waiting.stop(t)

	// (b) the commander receives and rejects while no watch of the subcommander runs.
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.review.receive", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "received_by": commanderID,
	}, &store.GoalHandoff{})
	callWorkflowMonitorRPC(t, ctx, client, "goal.handoff.review.reject", map[string]any{
		"handoff_id": handoffID, "goal_id": goal.ID, "reviewer_id": commanderID,
		"reject_report": "fix the goal",
	}, &store.GoalHandoff{})

	// (c) a fresh --once watch delivers the rejection and returns.
	rearmed := startBoundLoop(ctx, server, server.URL, t.TempDir(), root, subToken, true)
	rearmed.waitOutput(t, handoffID)
	rearmed.waitReturn(t)
}
