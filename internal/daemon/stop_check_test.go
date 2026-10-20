package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

	// No subcommander Monitor: nothing would report an executor that never
	// receives, so the request-only handoff blocks.
	detail, err := fixture.daemon.stopCheckSubcommander(ctx, subcommanderID, fixture.taskGoal.ID)
	if err != nil {
		t.Fatalf("stopCheckSubcommander(request-only, no monitor): %v", err)
	}
	if !strings.Contains(detail, "unreceived task handoff") {
		t.Fatalf("stopCheckSubcommander(request-only, no monitor) = %q, want unreceived task handoff", detail)
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
		{"task review alone is not waiting", true, true, func(t *testing.T, w waitingFixture) { w.taskHandoff(t, true, true) }, "task review handoff"},
		{"task review handoff still blocks while goal review pends", true, true, func(t *testing.T, w waitingFixture) {
			w.goalReview(t)
			w.taskHandoff(t, true, true)
		}, "task review handoff"},
		{"unreceived task handoff with monitor is the executor starting", true, true, func(t *testing.T, w waitingFixture) { w.taskHandoff(t, false, false) }, ""},
		{"unreceived task handoff without monitor", true, false, func(t *testing.T, w waitingFixture) { w.taskHandoff(t, false, false) }, "unreceived task handoff"},
		{"unreceived task handoff without monitor while goal review pends", true, false, func(t *testing.T, w waitingFixture) {
			w.goalReview(t)
			w.taskHandoff(t, false, false)
		}, "unreceived task handoff"},
		{"rejected plan handoff is not waiting", true, true, func(t *testing.T, w waitingFixture) {
			w.planReview(t)
			ctx := context.Background()
			if _, err := w.store.ReceivePlanHandoffReview(ctx, "stop-wait-plan", w.taskGoal.ID, w.commanderID); err != nil {
				t.Fatalf("ReceivePlanHandoffReview: %v", err)
			}
			if _, err := w.store.RejectPlanHandoffReview(ctx, "stop-wait-plan", w.taskGoal.ID, w.commanderID, "fix"); err != nil {
				t.Fatalf("RejectPlanHandoffReview: %v", err)
			}
			if _, err := w.store.ReceivePlanHandoffReviewRejection(ctx, "stop-wait-plan", w.taskGoal.ID, w.subID); err != nil {
				t.Fatalf("ReceivePlanHandoffReviewRejection: %v", err)
			}
		}, "rejected plan handoff"},
		{"open task-create handoff is not waiting", true, true, func(t *testing.T, w waitingFixture) {
			w.planReview(t)
			ctx := context.Background()
			if _, err := w.store.ReceivePlanHandoffReview(ctx, "stop-wait-plan", w.taskGoal.ID, w.commanderID); err != nil {
				t.Fatalf("ReceivePlanHandoffReview: %v", err)
			}
			if _, err := w.store.CompletePlanHandoff(ctx, "stop-wait-plan", w.taskGoal.ID, w.commanderID, "ok"); err != nil {
				t.Fatalf("CompletePlanHandoff: %v", err)
			}
			createHandoff, err := w.store.GetTaskCreateHandoffForGoal(ctx, w.taskGoal.ID)
			if err != nil {
				t.Fatalf("GetTaskCreateHandoffForGoal: %v", err)
			}
			if _, err := w.store.ReceiveTaskCreateHandoff(ctx, createHandoff.ID, w.subID); err != nil {
				t.Fatalf("ReceiveTaskCreateHandoff: %v", err)
			}
		}, "open task-create handoff"},
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

// stopRepeat identifies the fixture's sessions and runs stopCheck as them.
type stopRepeat struct {
	stopCheckFixture
	daemon *Daemon
}

func newStopRepeat(t *testing.T) stopRepeat {
	t.Helper()
	f := newStopCheckFixture(t)
	t.Cleanup(func() { _ = f.store.Close() })
	for _, id := range []int64{f.commanderID, f.subcommanderID, f.executorID} {
		if _, _, err := f.store.IdentifyAgentSession(context.Background(), id, stopRepeatKey(id)); err != nil {
			t.Fatalf("IdentifyAgentSession(%d): %v", id, err)
		}
	}
	return stopRepeat{stopCheckFixture: f, daemon: f.daemon}
}

func stopRepeatKey(id int64) string { return fmt.Sprintf("stop-repeat-key-%d", id) }

func (r stopRepeat) blocks(t *testing.T, id int64) bool {
	t.Helper()
	// stopCheckWork, not stopCheck: these tests are about the repeat check, not the missing-watch block.
	agentSessionID, err := r.store.AgentSessionIDByKey(context.Background(), stopRepeatKey(id))
	if err != nil {
		t.Fatalf("AgentSessionIDByKey(%d): %v", id, err)
	}
	role, err := r.daemon.deriveSessionRole(context.Background(), agentSessionID)
	if err != nil {
		t.Fatalf("deriveSessionRole(%d): %v", id, err)
	}
	response, err := r.daemon.stopCheckWork(context.Background(), agentSessionID, role)
	if err != nil {
		t.Fatalf("stopCheckWork(%d): %v", id, err)
	}
	return response.Decision == "block"
}

func (r stopRepeat) watch(t *testing.T, role string) {
	t.Helper()
	now := time.Now().UTC()
	health := store.MonitorHealth{
		CWD: t.TempDir(), Role: role, State: "healthy", Reason: "test", ProjectID: r.projectID,
		PID: 1, ProcessStartedAt: now.Add(-time.Minute), TransitionedAt: now, LastSeenAt: now,
	}
	if role == "subcommander" {
		health.GoalID = &r.goalID
	}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
	if err := r.store.UpsertMonitorHealth(context.Background(), health); err != nil {
		t.Fatalf("UpsertMonitorHealth: %v", err)
	}
}

func (r stopRepeat) createTask(t *testing.T) domain.Task {
	t.Helper()
	tasks, err := r.store.CreateTasks(context.Background(), r.goalID, "agent", fmt.Sprintf("stop-repeat-%d", time.Now().UnixNano()), []string{"task"}, []string{"description"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	return tasks[0]
}

// taskHandoff requests a task handoff from the subcommander and receives it as the executor.
func (r stopRepeat) taskHandoff(t *testing.T, task domain.Task) string {
	t.Helper()
	id := fmt.Sprintf("stop-repeat-th-%d", task.ID)
	addTaskHandoffDirect(t, r.store, id, task.ID, r.subcommanderID, 0)
	if _, err := r.store.ReceiveTaskHandoff(context.Background(), id, task.ID, r.executorID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	return id
}

func (r stopRepeat) answerDecision(t *testing.T) {
	t.Helper()
	d, err := r.store.AskDecision(context.Background(), store.AskInput{
		GoalID: r.goalID, Kind: domain.KindDecision, Question: "q",
		Options: []domain.Option{{Label: "yes"}}, AgentSessionID: r.subcommanderID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := r.store.AnswerDecision(context.Background(), store.AnswerInput{DecisionID: d.ID, AnswerLabel: "yes", AnswerText: "yes"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
}

// Each role has its own way to hold a stop reason that stays the same while
// the state around it changes.
func stopRepeatRoles() map[string]struct {
	session func(stopRepeat) int64
	setup   func(*testing.T, stopRepeat)
} {
	return map[string]struct {
		session func(stopRepeat) int64
		setup   func(*testing.T, stopRepeat)
	}{
		"commander":    {func(r stopRepeat) int64 { return r.commanderID }, func(*testing.T, stopRepeat) {}},
		"subcommander": {func(r stopRepeat) int64 { return r.subcommanderID }, func(t *testing.T, r stopRepeat) { stopCheckGoalHandoff(t, r.stopCheckFixture, true) }},
		"executor": {func(r stopRepeat) int64 { return r.executorID }, func(t *testing.T, r stopRepeat) {
			stopCheckGoalHandoff(t, r.stopCheckFixture, true)
			r.taskHandoff(t, r.createTask(t))
		}},
	}
}

func TestStopCheckRepeatSameStateDoesNotBlockAgain(t *testing.T) {
	for name, role := range stopRepeatRoles() {
		t.Run(name, func(t *testing.T) {
			r := newStopRepeat(t)
			role.setup(t, r)
			id := role.session(r)
			if !r.blocks(t, id) {
				t.Fatal("first stop did not block")
			}
			if r.blocks(t, id) || r.blocks(t, id) {
				t.Fatal("a repeat stop in the same state blocked again")
			}
		})
	}
}

func TestStopCheckBlocksAgainWhenStateChanges(t *testing.T) {
	for _, tc := range []struct {
		role   string
		change string
		do     func(*testing.T, stopRepeat)
	}{
		{"commander", "task created", func(t *testing.T, r stopRepeat) { r.createTask(t) }},
		{"commander", "task done", func(t *testing.T, r stopRepeat) {
			if _, err := r.store.DB().ExecContext(context.Background(), "UPDATE tasks SET status = 'done' WHERE goal_id = ?", r.goalID); err != nil {
				t.Fatalf("mark task done: %v", err)
			}
		}},
		{"subcommander", "task handoff received", func(t *testing.T, r stopRepeat) { r.taskHandoff(t, r.createTask(t)) }},
		{"subcommander", "decision answered", func(t *testing.T, r stopRepeat) { r.answerDecision(t) }},
		{"executor", "review requested", func(t *testing.T, r stopRepeat) {
			task, err := r.store.ListTasks(context.Background(), r.goalID)
			if err != nil || len(task) != 1 {
				t.Fatalf("ListTasks = %v, %v", task, err)
			}
			if _, err := r.store.RequestTaskHandoffReview(context.Background(), fmt.Sprintf("stop-repeat-th-%d", task[0].ID), task[0].ID, r.executorID, "ready"); err != nil {
				t.Fatalf("RequestTaskHandoffReview: %v", err)
			}
		}},
	} {
		t.Run(tc.role+"/"+tc.change, func(t *testing.T) {
			r := newStopRepeat(t)
			role := stopRepeatRoles()[tc.role]
			if tc.change == "task done" {
				r.createTask(t)
			}
			role.setup(t, r)
			id := role.session(r)
			if !r.blocks(t, id) {
				t.Fatal("first stop did not block")
			}
			tc.do(t, r)
			if !r.blocks(t, id) {
				t.Fatalf("stop after %q did not block", tc.change)
			}
		})
	}
}

func TestStopCheckBlocksAgainWhenReasonChanges(t *testing.T) {
	r := newStopRepeat(t)
	stopCheckGoalHandoff(t, r.stopCheckFixture, true)
	if !r.blocks(t, r.subcommanderID) {
		t.Fatal("first stop did not block")
	}
	// Requested and not received, with no Monitor: a different reason.
	task := r.createTask(t)
	addTaskHandoffDirect(t, r.store, "stop-repeat-unreceived", task.ID, r.subcommanderID, 0)
	if !r.blocks(t, r.subcommanderID) {
		t.Fatal("stop with a different reason did not block")
	}
}

func TestStopCheckRestartedDaemonBlocksAgain(t *testing.T) {
	r := newStopRepeat(t)
	if !r.blocks(t, r.commanderID) || r.blocks(t, r.commanderID) {
		t.Fatal("want block then pass in the same state")
	}
	r.daemon = New(r.store)
	if !r.blocks(t, r.commanderID) {
		t.Fatal("a new daemon did not block in the same state")
	}
}

func TestStopCheckConcurrentSessions(t *testing.T) {
	r := newStopRepeat(t)
	var wg sync.WaitGroup
	for _, id := range []int64{r.commanderID, r.subcommanderID, r.executorID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := r.daemon.stopCheck(context.Background(), stopRepeatKey(id), false); err != nil {
					t.Errorf("stopCheck(%d): %v", id, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// A `--once` watch that has ended and is only inside its re-arm grace does not
// deliver anything, so it must not count as a live watch for a stop.
func TestStopCheckRearmingWatchDoesNotCountAsLive(t *testing.T) {
	for _, role := range []string{"commander", "subcommander"} {
		t.Run(role, func(t *testing.T) {
			r := newStopRepeat(t)
			stopCheckGoalHandoff(t, r.stopCheckFixture, true)
			id := r.commanderID
			if role == "subcommander" {
				id = r.subcommanderID
			}
			key := stopRepeatKey(id)
			ctx := context.Background()
			if _, err := r.store.DB().ExecContext(ctx, "DELETE FROM monitor_health"); err != nil {
				t.Fatalf("delete monitor_health: %v", err)
			}
			now := time.Now().UTC()
			health := store.MonitorHealth{
				CWD: t.TempDir(), Role: role, State: "rearming", Reason: "once: re-arm expected", ProjectID: r.projectID,
				PID: 1, ProcessStartedAt: now.Add(-time.Minute), TransitionedAt: now, LastSeenAt: now,
			}
			if role == "subcommander" {
				health.GoalID = &r.goalID
			}
			health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt)
			if err := r.store.UpsertMonitorHealth(ctx, health); err != nil {
				t.Fatalf("UpsertMonitorHealth: %v", err)
			}
			// stop_hook_active skips the work checks, so only the watch check decides.
			response, err := r.daemon.stopCheck(ctx, key, true)
			if err != nil || response.Decision != "block" || !strings.Contains(response.Reason, "atct watch --monitor --token <monitor_token>") {
				t.Fatalf("stop with only a rearming row = %+v, %v, want a block naming the watch command", response, err)
			}
			// Re-arming the watch lets the session stop.
			r.watch(t, role)
			if response, err := r.daemon.stopCheck(ctx, key, true); err != nil || response.Decision != "" {
				t.Fatalf("stop with a live watch = %+v, %v, want pass", response, err)
			}
		})
	}
}

func TestStopCheckBlocksSessionWithoutWatch(t *testing.T) {
	for _, role := range []string{"commander", "subcommander"} {
		t.Run(role, func(t *testing.T) {
			r := newStopRepeat(t)
			stopCheckGoalHandoff(t, r.stopCheckFixture, true)
			id := r.commanderID
			if role == "subcommander" {
				id = r.subcommanderID
			}
			key := stopRepeatKey(id)
			if _, err := r.store.DB().ExecContext(context.Background(), "DELETE FROM monitor_health"); err != nil {
				t.Fatalf("delete monitor_health: %v", err)
			}
			// Stops that follow a block (stop_hook_active) are blocked for the
			// missing watch up to the limit, and the next one passes: a session
			// that cannot arm a watch is not held forever.
			for i := 0; i < stopNoWatchLimit; i++ {
				response, err := r.daemon.stopCheck(context.Background(), key, true)
				if err != nil || response.Decision != "block" || !strings.Contains(response.Reason, "atct watch --monitor --token <monitor_token>") {
					t.Fatalf("stop %d = %+v, %v, want a block naming the watch command", i, response, err)
				}
			}
			if response, err := r.daemon.stopCheck(context.Background(), key, true); err != nil || response.Decision != "" {
				t.Fatalf("stop after the limit = %+v, %v, want pass", response, err)
			}
			// Arming the watch lets the session stop.
			r.watch(t, role)
			if response, err := r.daemon.stopCheck(context.Background(), key, true); err != nil || response.Decision != "" {
				t.Fatalf("stop with a live watch = %+v, %v, want pass", response, err)
			}
		})
	}
}
