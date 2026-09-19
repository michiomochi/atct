package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

type stopCheckFixture struct {
	store                      *store.Store
	daemon                     *Daemon
	projectID, goalID          int64
	commanderID                int64
	subcommanderID, executorID int64
}

func newStopCheckFixture(t *testing.T) stopCheckFixture {
	t.Helper()
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "stop-check")
	goal, err := s.CreateGoal(ctx, project.ID, "stop-check goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID := daemonTestSessionID(t, s, "stop-check-matrix-commander")
	subcommanderID := daemonTestSessionID(t, s, "stop-check-matrix-subcommander")
	executorID := daemonTestSessionID(t, s, "stop-check-matrix-executor")
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	return stopCheckFixture{
		store:          s,
		daemon:         New(s),
		projectID:      project.ID,
		goalID:         goal.ID,
		commanderID:    commanderID,
		subcommanderID: subcommanderID,
		executorID:     executorID,
	}
}

func stopCheckGoalHandoff(t *testing.T, fixture stopCheckFixture, receive bool) store.GoalHandoff {
	t.Helper()
	ctx := context.Background()
	handoff, err := fixture.store.RequestGoalHandoff(ctx, "stop-check-goal-handoff", fixture.goalID, fixture.commanderID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if receive {
		handoff, err = fixture.store.ReceiveGoalHandoff(ctx, handoff.ID, fixture.goalID, fixture.subcommanderID)
		if err != nil {
			t.Fatalf("ReceiveGoalHandoff: %v", err)
		}
	}
	return handoff
}

func stopCheckGoalReview(t *testing.T, fixture stopCheckFixture) domain.Decision {
	t.Helper()
	ctx := context.Background()
	handoff := stopCheckGoalHandoff(t, fixture, true)
	if _, err := fixture.store.RequestGoalHandoffReview(ctx, handoff.ID, fixture.goalID, fixture.subcommanderID, "ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
	if _, err := fixture.store.ReceiveGoalHandoffReview(ctx, handoff.ID, fixture.goalID, fixture.commanderID); err != nil {
		t.Fatalf("ReceiveGoalHandoffReview: %v", err)
	}
	decision, err := fixture.store.RequestGoalReview(ctx, fixture.goalID, fixture.commanderID, domain.CompletionReport{
		WorkDone: "done", NowPossible: "now", HowToVerify: "verify", Surprises: "none", NeedsReview: "none", NextSteps: "next",
	})
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}
	return decision
}

func TestSessionStopCheckCommanderActionability(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(*testing.T, stopCheckFixture)
		wantBlock bool
	}{
		{
			name:      "commander-owned setup",
			wantBlock: true,
		},
		{
			name: "requested goal handoff is delegated",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				stopCheckGoalHandoff(t, fixture, false)
			},
		},
		{
			name: "received goal handoff is subcommander work",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				stopCheckGoalHandoff(t, fixture, true)
			},
		},
		{
			name: "unreceived plan review is commander work",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				ctx := context.Background()
				stopCheckGoalHandoff(t, fixture, true)
				if _, err := fixture.store.UpdateGoalRequestReport(ctx, fixture.goalID, "spec", "plan"); err != nil {
					t.Fatalf("UpdateGoalRequestReport: %v", err)
				}
				if _, err := fixture.store.RequestPlanHandoffReview(ctx, "stop-check-plan-review", fixture.goalID, fixture.subcommanderID, "ready"); err != nil {
					t.Fatalf("RequestPlanHandoffReview: %v", err)
				}
			},
			wantBlock: true,
		},
		{
			name: "unreceived goal review is commander work",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				ctx := context.Background()
				handoff := stopCheckGoalHandoff(t, fixture, true)
				if _, err := fixture.store.RequestGoalHandoffReview(ctx, handoff.ID, fixture.goalID, fixture.subcommanderID, "ready"); err != nil {
					t.Fatalf("RequestGoalHandoffReview: %v", err)
				}
			},
			wantBlock: true,
		},
		{
			name: "open human decision is not commander work",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				if _, err := fixture.store.AskDecision(context.Background(), store.AskInput{
					GoalID: fixture.goalID, Kind: domain.KindDecision, Question: "wait for human", AgentSessionID: fixture.subcommanderID,
				}); err != nil {
					t.Fatalf("AskDecision: %v", err)
				}
			},
		},
		{
			name: "executor-owned task handoff is not commander work",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				tasks, err := fixture.store.CreateTasks(context.Background(), fixture.goalID, "executor", "stop-check-task", []string{"task"}, []string{"description"})
				if err != nil {
					t.Fatalf("CreateTasks: %v", err)
				}
				if _, err := fixture.store.ClaimTask(context.Background(), tasks[0].ID, fixture.executorID); err != nil {
					t.Fatalf("ClaimTask: %v", err)
				}
			},
		},
		{
			name: "rejected goal review needs commander return",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				decision := stopCheckGoalReview(t, fixture)
				if err := fixture.store.RejectGoalReview(context.Background(), decision.ID, "revise"); err != nil {
					t.Fatalf("RejectGoalReview: %v", err)
				}
			},
			wantBlock: true,
		},
		{
			name: "approved goal review needs commander finalization",
			setup: func(t *testing.T, fixture stopCheckFixture) {
				decision := stopCheckGoalReview(t, fixture)
				if _, err := fixture.store.ApproveGoalReview(context.Background(), decision.ID); err != nil {
					t.Fatalf("ApproveGoalReview: %v", err)
				}
			},
			wantBlock: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newStopCheckFixture(t)
			if tc.setup != nil {
				tc.setup(t, fixture)
			}
			detail, err := fixture.daemon.stopCheckCommander(context.Background(), fixture.projectID)
			if err != nil {
				t.Fatalf("stopCheckCommander: %v", err)
			}
			if got := detail != ""; got != tc.wantBlock {
				t.Fatalf("stopCheckCommander detail = %q, blocked = %v, want blocked = %v", detail, got, tc.wantBlock)
			}
		})
	}
}

func TestSessionStopCheckUsesIdentifiedSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	ctx := context.Background()
	const sessionKey = "stop-check-session-key"
	commanderID := daemonTestSessionID(t, fixture.store, "stop-check-commander")
	if _, _, err := fixture.store.IdentifyAgentSession(ctx, commanderID, sessionKey); err != nil {
		t.Fatalf("IdentifyAgentSession: %v", err)
	}
	if _, err := fixture.store.ClaimProject(ctx, fixture.project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	params, err := json.Marshal(map[string]any{"session_key": sessionKey})
	if err != nil {
		t.Fatalf("marshal session.stop_check params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(ctx, rpc.Request{Method: "session.stop_check", Params: params})
	if err != nil {
		t.Fatalf("session.stop_check: %v", err)
	}
	var response struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode session.stop_check response: %v", err)
	}
	if response.Decision != "block" || response.Reason == "" {
		t.Fatalf("session.stop_check response = %+v, want blocking work", response)
	}
}
