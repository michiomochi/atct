package daemon

import (
	"context"
	"encoding/json"
	"strings"
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
		WorkDone: "done", NowPossible: "now", HowToVerify: "verify", Surprises: "none", NeedsReview: "none", NextGoalIDs: []int64{},
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

func TestStopCheckSubcommander(t *testing.T) {
	fixture := newGoalListFixture(t)
	t.Cleanup(func() {
		if err := fixture.store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})

	ctx := context.Background()
	const handoffID = "stop-check-subcommander-task-handoff"
	subcommanderID := daemonTestSessionID(t, fixture.store, "stop-check-subcommander")
	executorID := daemonTestSessionID(t, fixture.store, "stop-check-subcommander-executor")
	addTaskHandoffDirect(t, fixture.store, handoffID, fixture.tasks[0].ID, subcommanderID, 0)

	detail, err := fixture.daemon.stopCheckSubcommander(ctx, subcommanderID, fixture.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander(request-only): %v", err)
	}
	if detail == "" {
		t.Fatal("stopCheckSubcommander(request-only) returned no blocking detail")
	}

	if _, err := fixture.store.ReceiveTaskHandoff(ctx, handoffID, fixture.tasks[0].ID, executorID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	detail, err = fixture.daemon.stopCheckSubcommander(ctx, subcommanderID, fixture.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander(received): %v", err)
	}
	if detail != "" {
		t.Fatalf("stopCheckSubcommander(received) = %q, want no block", detail)
	}

	if _, err := fixture.store.RequestTaskHandoffReview(ctx, handoffID, fixture.tasks[0].ID, executorID, "ready"); err != nil {
		t.Fatalf("RequestTaskHandoffReview: %v", err)
	}
	detail, err = fixture.daemon.stopCheckSubcommander(ctx, subcommanderID, fixture.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander(review): %v", err)
	}
	if detail == "" {
		t.Fatal("stopCheckSubcommander(review) returned no blocking detail")
	}
}

// waitingFixture holds a subcommander that received the goal handoff on the
// task goal, with a commander and an executor session around it.
type waitingFixture struct {
	goalListFixture
	commanderID, subID, execID int64
	goalHandoffID              string
}

func newWaitingFixture(t *testing.T, holdGoalHandoff bool) waitingFixture {
	t.Helper()
	f := newGoalListFixture(t)
	t.Cleanup(func() { _ = f.store.Close() })
	ctx := context.Background()
	// A fresh goal and task: the fixture's own task goal already carries a claimed task.
	goal, err := f.store.CreateGoal(ctx, f.project.ID, "stop-wait goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := f.store.CreateTasks(ctx, goal.ID, "fixture-agent", "stop-wait-tasks", []string{"task"}, []string{"description"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	f.taskGoal, f.tasks = goal, tasks
	w := waitingFixture{
		goalListFixture: f,
		commanderID:     daemonTestSessionID(t, f.store, "stop-wait-commander"),
		subID:           daemonTestSessionID(t, f.store, "stop-wait-sub"),
		execID:          daemonTestSessionID(t, f.store, "stop-wait-exec"),
		goalHandoffID:   "stop-wait-goal-handoff",
	}
	if holdGoalHandoff {
		if _, err := f.store.ClaimProject(ctx, f.project.ID, w.commanderID); err != nil {
			t.Fatalf("ClaimProject: %v", err)
		}
		if _, err := f.store.RequestGoalHandoff(ctx, w.goalHandoffID, f.taskGoal.ID, w.commanderID, "delegate"); err != nil {
			t.Fatalf("RequestGoalHandoff: %v", err)
		}
		if _, err := f.store.ReceiveGoalHandoff(ctx, w.goalHandoffID, f.taskGoal.ID, w.subID); err != nil {
			t.Fatalf("ReceiveGoalHandoff: %v", err)
		}
	}
	return w
}

func (w waitingFixture) liveMonitor(t *testing.T, role string) {
	t.Helper()
	goalID, taskID := w.taskGoal.ID, w.tasks[0].ID
	if role != "executor" { // an executor Monitor is bound to a task as well
		addLiveMonitorForTest(t, w.goalListFixture, role, w.project.ID, &goalID, nil)
		return
	}
	addLiveMonitorForTest(t, w.goalListFixture, role, w.project.ID, &goalID, &taskID)
}

func (w waitingFixture) goalReview(t *testing.T) {
	t.Helper()
	if _, err := w.store.RequestGoalHandoffReview(context.Background(), w.goalHandoffID, w.taskGoal.ID, w.subID, "ready"); err != nil {
		t.Fatalf("RequestGoalHandoffReview: %v", err)
	}
}

func (w waitingFixture) planReview(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := w.store.UpdateGoalRequestReport(ctx, w.taskGoal.ID, "spec", "plan"); err != nil {
		t.Fatalf("UpdateGoalRequestReport: %v", err)
	}
	if _, err := w.store.RequestPlanHandoffReview(ctx, "stop-wait-plan", w.taskGoal.ID, w.subID, "ready"); err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
}

// taskHandoff creates a task handoff on tasks[0]: unreceived, received (the
// executor is working), or review requested.
func (w waitingFixture) taskHandoff(t *testing.T, receive, review bool) {
	t.Helper()
	ctx := context.Background()
	const id = "stop-wait-task-handoff"
	addTaskHandoffDirect(t, w.store, id, w.tasks[0].ID, w.subID, 0)
	if receive {
		if _, err := w.store.ReceiveTaskHandoff(ctx, id, w.tasks[0].ID, w.execID); err != nil {
			t.Fatalf("ReceiveTaskHandoff: %v", err)
		}
	}
	if review {
		if _, err := w.store.RequestTaskHandoffReview(ctx, id, w.tasks[0].ID, w.execID, "ready"); err != nil {
			t.Fatalf("RequestTaskHandoffReview: %v", err)
		}
	}
}

func TestStopCheckSubcommanderWaitingOnPeer(t *testing.T) {
	cases := []struct {
		name       string
		hold       bool // the subcommander holds the goal handoff
		monitor    bool
		setup      func(*testing.T, waitingFixture)
		wantReason string // empty: no block; otherwise a substring of the block detail
	}{
		{"plan review pending with monitor", true, true, func(t *testing.T, w waitingFixture) { w.planReview(t) }, ""},
		{"plan review pending without monitor", true, false, func(t *testing.T, w waitingFixture) { w.planReview(t) }, "open goal handoff"},
		{"goal review pending with monitor", true, true, func(t *testing.T, w waitingFixture) { w.goalReview(t) }, ""},
		{"goal review rejected is not waiting", true, true, func(t *testing.T, w waitingFixture) {
			w.goalReview(t)
			ctx := context.Background()
			if _, err := w.store.ReceiveGoalHandoffReview(ctx, w.goalHandoffID, w.taskGoal.ID, w.commanderID); err != nil {
				t.Fatalf("ReceiveGoalHandoffReview: %v", err)
			}
			if _, err := w.store.RejectGoalHandoffReview(ctx, w.goalHandoffID, w.taskGoal.ID, w.commanderID, "fix"); err != nil {
				t.Fatalf("RejectGoalHandoffReview: %v", err)
			}
		}, "open goal handoff"},
		{"executor working with monitor", true, true, func(t *testing.T, w waitingFixture) { w.taskHandoff(t, true, false) }, ""},
		{"executor working without monitor", true, false, func(t *testing.T, w waitingFixture) { w.taskHandoff(t, true, false) }, "open goal handoff"},
		{"task review alone is not waiting", true, true, func(t *testing.T, w waitingFixture) { w.taskHandoff(t, true, true) }, "open goal handoff"},
		{"task review handoff still blocks while goal review pends", true, true, func(t *testing.T, w waitingFixture) {
			w.goalReview(t)
			w.taskHandoff(t, true, true)
		}, "task review handoff"},
		{"unreceived task handoff still blocks while goal review pends", true, true, func(t *testing.T, w waitingFixture) {
			w.goalReview(t)
			w.taskHandoff(t, false, false)
		}, "unreceived task handoff"},
		{"nothing to wait for with monitor", true, true, func(t *testing.T, w waitingFixture) {}, "open goal handoff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWaitingFixture(t, tc.hold)
			tc.setup(t, w)
			if tc.monitor {
				w.liveMonitor(t, "subcommander")
			}
			detail, err := w.daemon.stopCheckSubcommander(context.Background(), w.subID, w.taskGoal.ID)
			if err != nil {
				t.Fatalf("stopCheckSubcommander: %v", err)
			}
			if tc.wantReason == "" && detail != "" {
				t.Fatalf("stopCheckSubcommander = %q, want no block", detail)
			}
			if tc.wantReason != "" && !strings.Contains(detail, tc.wantReason) {
				t.Fatalf("stopCheckSubcommander = %q, want a block containing %q", detail, tc.wantReason)
			}
		})
	}
}

func TestStopCheckExecutorWaitingOnReview(t *testing.T) {
	cases := []struct {
		name      string
		monitor   bool
		receive   bool
		review    bool
		reject    bool
		wantBlock bool
	}{
		{"review requested with monitor", true, true, true, false, false},
		{"review requested without monitor", false, true, true, false, true},
		{"review rejected with monitor", true, true, true, true, true},
		{"before review request with monitor", true, true, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWaitingFixture(t, false)
			w.taskHandoff(t, tc.receive, tc.review)
			if tc.reject {
				ctx := context.Background()
				if _, err := w.store.ReceiveTaskHandoffReview(ctx, "stop-wait-task-handoff", w.tasks[0].ID, w.subID); err != nil {
					t.Fatalf("ReceiveTaskHandoffReview: %v", err)
				}
				if _, err := w.store.RejectTaskHandoffReview(ctx, "stop-wait-task-handoff", w.tasks[0].ID, w.subID, "fix"); err != nil {
					t.Fatalf("RejectTaskHandoffReview: %v", err)
				}
			}
			if tc.monitor {
				w.liveMonitor(t, "executor")
			}
			detail, err := w.daemon.stopCheckExecutor(context.Background(), w.execID)
			if err != nil {
				t.Fatalf("stopCheckExecutor: %v", err)
			}
			if (detail != "") != tc.wantBlock {
				t.Fatalf("stopCheckExecutor = %q, wantBlock %v", detail, tc.wantBlock)
			}
		})
	}
}

// A Monitor of another executor on the same goal (another task) is not this
// task's Monitor: the waiting executor must still be blocked.
func TestStopCheckExecutorIgnoresOtherTaskMonitor(t *testing.T) {
	w := newWaitingFixture(t, false)
	w.taskHandoff(t, true, true)
	goalID, otherTaskID := w.taskGoal.ID, w.tasks[0].ID+1000
	addLiveMonitorForTest(t, w.goalListFixture, "executor", w.project.ID, &goalID, &otherTaskID)
	detail, err := w.daemon.stopCheckExecutor(context.Background(), w.execID)
	if err != nil {
		t.Fatalf("stopCheckExecutor: %v", err)
	}
	if detail == "" {
		t.Fatal("stopCheckExecutor allowed stop on another task's Monitor, want block")
	}
	w.liveMonitor(t, "executor")
	detail, err = w.daemon.stopCheckExecutor(context.Background(), w.execID)
	if err != nil {
		t.Fatalf("stopCheckExecutor: %v", err)
	}
	if detail != "" {
		t.Fatalf("stopCheckExecutor = %q, want pass with own task's Monitor", detail)
	}
}
