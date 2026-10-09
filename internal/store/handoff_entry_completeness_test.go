package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// handoffThread is the scope-neutral view of one task or goal handoff that the
// completeness tests need.
type handoffThread struct {
	name    string
	s       *Store
	table   string // entry table, used to inject write failures
	entries func(id string) []HandoffEntry
	times   func(id string) (handoffTimes, bool)
	snap    func(id string) any // everything a failed transition must leave untouched
}

type handoffTimes struct {
	requested, received, reviewRequested, reviewReceived, reviewRejected, completed bool
}

func newTaskThread(s *Store) handoffThread {
	ctx := context.Background()
	return handoffThread{
		name: "task", s: s, table: "task_handoff_entries",
		entries: func(id string) []HandoffEntry {
			page, err := s.ListTaskHandoffEntries(ctx, id, 0, HandoffHistoryMaxLimit)
			if errors.Is(err, ErrHandoffEntryNotFound) {
				return nil
			}
			if err != nil {
				panic(err)
			}
			return page.Entries
		},
		times: func(id string) (handoffTimes, bool) {
			h, err := s.GetTaskHandoff(ctx, id)
			if err != nil {
				return handoffTimes{}, false
			}
			return handoffTimes{h.RequestedAt != nil, h.ReceivedAt != nil, h.ReviewRequestedAt != nil, h.ReviewReceivedAt != nil, h.ReviewRejectedAt != nil, h.CompletedReportAt != nil}, true
		},
		snap: func(id string) any {
			h, err := s.GetTaskHandoff(ctx, id)
			if err != nil {
				return "absent"
			}
			status := ""
			if task, err := s.loadTask(ctx, h.TaskID); err == nil {
				status = string(task.Status)
			}
			return []any{h, status}
		},
	}
}

func newGoalThread(s *Store) handoffThread {
	ctx := context.Background()
	return handoffThread{
		name: "goal", s: s, table: "goal_handoff_entries",
		entries: func(id string) []HandoffEntry {
			page, err := s.ListGoalHandoffEntries(ctx, id, 0, HandoffHistoryMaxLimit)
			if errors.Is(err, ErrHandoffEntryNotFound) {
				return nil
			}
			if err != nil {
				panic(err)
			}
			return page.Entries
		},
		times: func(id string) (handoffTimes, bool) {
			h, err := s.GetGoalHandoff(ctx, id)
			if err != nil {
				return handoffTimes{}, false
			}
			return handoffTimes{h.RequestedAt != nil, h.ReceivedAt != nil, h.ReviewRequestedAt != nil, h.ReviewReceivedAt != nil, h.ReviewRejectedAt != nil, h.CompletedReportAt != nil}, true
		},
		snap: func(id string) any {
			h, err := s.GetGoalHandoff(ctx, id)
			if err != nil {
				return "absent"
			}
			return h
		},
	}
}

// requireEntryInvariant: a timestamp that is set on the handoff implies at
// least one entry of the matching kind. review_received_at is cleared by a
// rejection, so a review_received entry only implies a review_requested one.
func (h handoffThread) requireEntryInvariant(t *testing.T, id string) {
	t.Helper()
	times, ok := h.times(id)
	if !ok {
		t.Fatalf("%s handoff %q not found", h.name, id)
	}
	kinds := map[string]bool{}
	for _, e := range h.entries(id) {
		kinds[e.Kind] = true
	}
	for _, c := range []struct {
		set  bool
		kind string
	}{
		{times.requested, HandoffEntryKindRequest},
		{times.received, HandoffEntryKindReceived},
		{times.reviewRequested, HandoffEntryKindReviewRequested},
		{times.reviewRejected, HandoffEntryKindReviewRejected},
		{times.completed, HandoffEntryKindCompleted},
		{times.reviewReceived, HandoffEntryKindReviewReceived},
		{kinds[HandoffEntryKindReviewReceived], HandoffEntryKindReviewRequested},
	} {
		if c.set && !kinds[c.kind] {
			t.Fatalf("%s handoff %q has state but no %q entry (kinds: %v)", h.name, id, c.kind, kinds)
		}
	}
}

// requireOneMoreEntry runs transition and checks it appended exactly one entry
// with the wanted kind, body and author.
func (h handoffThread) requireOneMoreEntry(t *testing.T, label, id string, transition func() error, kind, body string, author int64) {
	t.Helper()
	before := len(h.entries(id))
	if err := transition(); err != nil {
		t.Fatalf("%s %s: %v", h.name, label, err)
	}
	got := h.entries(id)
	if len(got) != before+1 {
		t.Fatalf("%s %s: entries %d -> %d, want +1", h.name, label, before, len(got))
	}
	last := got[len(got)-1]
	if last.Kind != kind || last.Body != body || last.AuthorSessionID != author {
		t.Fatalf("%s %s: last entry = %s/%q/author %d, want %s/%q/author %d", h.name, label, last.Kind, last.Body, last.AuthorSessionID, kind, body, author)
	}
	h.requireEntryInvariant(t, id)
}

// requireAtomic makes every entry write fail, runs transition, and checks that
// it errors and leaves the handoff (and task) exactly as it was.
func (h handoffThread) requireAtomic(t *testing.T, label, id string, transition func() error) {
	t.Helper()
	ctx := context.Background()
	before, entriesBefore := h.snap(id), len(h.entries(id))
	if _, err := h.s.DB().ExecContext(ctx, `CREATE TRIGGER test_fail_entry BEFORE INSERT ON `+h.table+` BEGIN SELECT RAISE(ABORT, 'entry write rejected'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	err := transition()
	if _, dropErr := h.s.DB().ExecContext(ctx, `DROP TRIGGER test_fail_entry`); dropErr != nil {
		t.Fatalf("drop failure trigger: %v", dropErr)
	}
	if err == nil {
		t.Fatalf("%s %s: succeeded although the entry write failed", h.name, label)
	}
	if after := h.snap(id); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s %s: state changed although the entry write failed:\nbefore %+v\nafter  %+v", h.name, label, before, after)
	}
	if n := len(h.entries(id)); n != entriesBefore {
		t.Fatalf("%s %s: entries %d -> %d after failure", h.name, label, entriesBefore, n)
	}
}

type lifecycleStep struct {
	name   string
	kind   string
	body   string
	author int64
	run    func() error
	// setup runs once before the step, outside the entry assertions.
	setup func() error
}

// lifecycleSteps is request -> receive -> review -> reject -> reject receive ->
// review again -> complete -> amend, through the public Store API only.
func lifecycleSteps(t *testing.T, kind string) (handoffThread, string, []lifecycleStep) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	const id = "completeness-lifecycle"
	if kind == "task" {
		taskID := addTestTasks(t, s, 1)[0]
		addLiveParentGoalClaim(t, s, taskID, "cl-task-req")
		addTestAgentSession(t, s, "cl-task-recv")
		req, recv := testSessionID("cl-task-req"), testSessionID("cl-task-recv")
		do := func(f func() error) func() error { return f }
		return newTaskThread(s), id, []lifecycleStep{
			{"request", HandoffEntryKindRequest, "go", req, do(func() error { _, err := s.RequestTaskHandoff(ctx, id, taskID, req, "go"); return err }), nil},
			{"receive", HandoffEntryKindReceived, "received", recv, do(func() error { _, err := s.ReceiveTaskHandoff(ctx, id, taskID, recv); return err }), nil},
			{"review request", HandoffEntryKindReviewRequested, "r1", recv, do(func() error { _, err := s.RequestTaskHandoffReview(ctx, id, taskID, recv, "r1"); return err }), nil},
			{"review receive", HandoffEntryKindReviewReceived, "received", req, do(func() error { _, err := s.ReceiveTaskHandoffReview(ctx, id, taskID, req); return err }), nil},
			{"reject", HandoffEntryKindReviewRejected, "no", req, do(func() error { _, err := s.RejectTaskHandoffReview(ctx, id, taskID, req, "no"); return err }), nil},
			{"reject receive", HandoffEntryKindReceived, "review rejection received", recv, do(func() error { _, err := s.ReceiveTaskHandoffReviewRejection(ctx, id, taskID, recv); return err }), nil},
			{"review request again", HandoffEntryKindReviewRequested, "r2", recv, do(func() error { _, err := s.RequestTaskHandoffReview(ctx, id, taskID, recv, "r2"); return err }), nil},
			{"review receive again", HandoffEntryKindReviewReceived, "received", req, do(func() error { _, err := s.ReceiveTaskHandoffReview(ctx, id, taskID, req); return err }), nil},
			{"complete", HandoffEntryKindCompleted, "done", req, do(func() error { _, err := s.CompleteTaskHandoffByReviewer(ctx, id, taskID, req, "done"); return err }), nil},
			{"amend", HandoffEntryKindCompleted, "fixed", recv, do(func() error { _, err := s.AmendTaskHandoffReport(ctx, id, taskID, "fixed"); return err }), nil},
		}
	}
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "cl-goal-req")
	addTestAgentSession(t, s, "cl-goal-recv")
	req, recv := testSessionID("cl-goal-req"), testSessionID("cl-goal-recv")
	// A commander that takes over the project re-receives the review its
	// predecessor received; the claim then returns so the rest is unchanged.
	takeover := registerNamedTestAgentSession(t, s, "cl-goal-takeover", os.Getpid())
	claimProject := func(commander int64) func() error {
		return func() error {
			goal, err := s.GetGoal(ctx, goalID)
			if err != nil {
				return err
			}
			_, err = s.DB().ExecContext(ctx, `UPDATE projects SET claimed_by = ? WHERE id = ?`, commander, goal.ProjectID)
			return err
		}
	}
	return newGoalThread(s), id, []lifecycleStep{
		{"request", HandoffEntryKindRequest, "go", req, func() error { _, err := s.RequestGoalHandoff(ctx, id, goalID, req, "go"); return err }, nil},
		{"receive", HandoffEntryKindReceived, "received", recv, func() error { _, err := s.ReceiveGoalHandoff(ctx, id, goalID, recv); return err }, nil},
		{"review request", HandoffEntryKindReviewRequested, "r1", recv, func() error { _, err := s.RequestGoalHandoffReview(ctx, id, goalID, recv, "r1"); return err }, nil},
		{"review receive", HandoffEntryKindReviewReceived, "received", req, func() error { _, err := s.ReceiveGoalHandoffReview(ctx, id, goalID, req); return err }, nil},
		{"reject", HandoffEntryKindReviewRejected, "no", req, func() error { _, err := s.RejectGoalHandoffReview(ctx, id, goalID, req, "no"); return err }, nil},
		{"reject receive", HandoffEntryKindReceived, "review rejection received", recv, func() error { _, err := s.ReceiveGoalHandoffReviewRejection(ctx, id, goalID, recv); return err }, nil},
		{"review request again", HandoffEntryKindReviewRequested, "r2", recv, func() error { _, err := s.RequestGoalHandoffReview(ctx, id, goalID, recv, "r2"); return err }, nil},
		{"review receive again", HandoffEntryKindReviewReceived, "received", req, func() error { _, err := s.ReceiveGoalHandoffReview(ctx, id, goalID, req); return err }, nil},
		{"review re-receive after takeover", HandoffEntryKindReviewReceived, "received", takeover, func() error { _, err := s.ReceiveGoalHandoffReview(ctx, id, goalID, takeover); return err }, claimProject(takeover)},
		{"review re-receive after claim returns", HandoffEntryKindReviewReceived, "received", req, func() error { _, err := s.ReceiveGoalHandoffReview(ctx, id, goalID, req); return err }, claimProject(req)},
		// A delegated goal handoff closes only through an approved goal review, and the
		// completed entry carries the handoff's last review request report.
		{name: "complete", kind: HandoffEntryKindCompleted, body: "r2", author: req,
			setup: func() error {
				review, err := s.RequestGoalReview(ctx, goalID, req, goalReviewRequestTestReport())
				if err != nil {
					return err
				}
				_, err = s.ApproveGoalReview(ctx, review.ID)
				return err
			},
			run: func() error { _, err := s.FinalizeGoalReview(ctx, goalID, req); return err }},
		{"amend", HandoffEntryKindCompleted, "fixed", recv, func() error { _, err := s.AmendGoalHandoffReport(ctx, id, goalID, "fixed"); return err }, nil},
	}
}

func TestHandoffEntriesFollowEveryLifecycleTransition(t *testing.T) {
	for _, scope := range []string{"task", "goal"} {
		t.Run(scope, func(t *testing.T) {
			h, id, steps := lifecycleSteps(t, scope)
			for _, step := range steps {
				if step.setup != nil {
					if err := step.setup(); err != nil {
						t.Fatalf("%s setup: %v", step.name, err)
					}
				}
				h.requireOneMoreEntry(t, step.name, id, step.run, step.kind, step.body, step.author)
			}
		})
	}
}

// A failed entry write must roll the state change back, for every transition.
func TestHandoffTransitionsAreAtomicWithTheirEntry(t *testing.T) {
	for _, scope := range []string{"task", "goal"} {
		t.Run(scope, func(t *testing.T) {
			h, id, steps := lifecycleSteps(t, scope)
			for _, step := range steps {
				if step.setup != nil {
					if err := step.setup(); err != nil {
						t.Fatalf("%s setup: %v", step.name, err)
					}
				}
				h.requireAtomic(t, step.name, id, step.run)
				h.requireOneMoreEntry(t, step.name+" after failure", id, step.run, step.kind, step.body, step.author)
			}
		})
	}
}

type recoverEnv struct {
	h       handoffThread
	id      string
	caller  int64
	recover func() error
	// seeded is true when the fixture inserted the handoff row directly, so it
	// has no entries of its own and the invariant cannot hold for it.
	seeded bool
}

func recoverEnvs(t *testing.T) map[string]recoverEnv {
	t.Helper()
	ctx := context.Background()
	envs := map[string]recoverEnv{}

	newTask := func(label string) (*Store, handoffThread, int64, int64) {
		s := newTestStore(t)
		goalID, holder := newTaskRecoveryGoal(t, s, label)
		tasks, err := s.CreateTasks(ctx, goalID, "worker", "recovery", []string{"x"}, []string{"x"})
		if err != nil {
			t.Fatal(err)
		}
		return s, newTaskThread(s), tasks[0].ID, holder
	}
	{
		s, h, taskID, holder := newTask("cr-task-requested")
		stale := addStaleRecoverySession(t, s, "cr-task-requested-stale")
		addTaskHandoffDirect(t, s, "cr-task-requested", taskID, stale, "")
		envs["task requested"] = recoverEnv{h, "cr-task-requested", holder, func() error {
			_, err := s.RecoverTaskHandoff(ctx, "cr-task-requested", taskID, holder, "gone")
			return err
		}, true}
	}
	{
		s, h, taskID, holder := newTask("cr-task-received")
		stale := addStaleRecoverySession(t, s, "cr-task-received-stale")
		if _, err := s.RequestTaskHandoff(ctx, "cr-task-received", taskID, holder, "go"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReceiveTaskHandoff(ctx, "cr-task-received", taskID, stale); err != nil {
			t.Fatal(err)
		}
		envs["task received"] = recoverEnv{h, "cr-task-received", holder, func() error {
			_, err := s.RecoverTaskHandoff(ctx, "cr-task-received", taskID, holder, "gone")
			return err
		}, false}
	}
	{
		s, h, taskID, holder := newTask("cr-task-review")
		stale := addStaleRecoverySession(t, s, "cr-task-review-stale")
		executor := registerNamedTestAgentSession(t, s, "cr-task-review-executor", os.Getpid())
		addTaskHandoffDirect(t, s, "cr-task-review", taskID, stale, executor)
		if _, err := s.DB().ExecContext(ctx, `UPDATE task_handoffs SET review_requested_by = ?, review_requested_at = ?, review_request_report = 'r', review_received_by = ?, review_received_at = ? WHERE id = ?`,
			executor, formatTimestamp(time.Now()), stale, formatTimestamp(time.Now()), "cr-task-review"); err != nil {
			t.Fatal(err)
		}
		envs["task review_received"] = recoverEnv{h, "cr-task-review", holder, func() error {
			_, err := s.RecoverTaskHandoff(ctx, "cr-task-review", taskID, holder, "gone")
			return err
		}, true}
	}
	{
		s := newTestStore(t)
		goalID := newTestGoal(t, s)
		addLiveProjectClaim(t, s, goalID, "cr-goal-requested-cur")
		cur := testSessionID("cr-goal-requested-cur")
		addStaleRecoverySession(t, s, "cr-goal-requested-stale")
		addRequestOnlyGoalHandoff(t, s, "cr-goal-requested", goalID, "cr-goal-requested-stale")
		envs["goal requested"] = recoverEnv{newGoalThread(s), "cr-goal-requested", cur, func() error {
			_, err := s.RecoverGoalHandoff(ctx, "cr-goal-requested", goalID, cur, "gone")
			return err
		}, true}
	}
	{
		s := newTestStore(t)
		goalID := newTestGoal(t, s)
		addLiveProjectClaim(t, s, goalID, "cr-goal-received-cur")
		cur := testSessionID("cr-goal-received-cur")
		stale := addStaleRecoverySession(t, s, "cr-goal-received-stale")
		if _, err := s.RequestGoalHandoff(ctx, "cr-goal-received", goalID, cur, "go"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReceiveGoalHandoff(ctx, "cr-goal-received", goalID, stale); err != nil {
			t.Fatal(err)
		}
		envs["goal received"] = recoverEnv{newGoalThread(s), "cr-goal-received", cur, func() error { _, err := s.RecoverGoalHandoff(ctx, "cr-goal-received", goalID, cur, "gone"); return err }, false}
	}
	{
		s := newTestStore(t)
		goalID := newTestGoal(t, s)
		goal, err := s.GetGoal(ctx, goalID)
		if err != nil {
			t.Fatal(err)
		}
		staleCmd := registerNamedTestAgentSession(t, s, "cr-goal-review-stale", os.Getpid())
		fresh := registerNamedTestAgentSession(t, s, "cr-goal-review-fresh", os.Getpid())
		sub := registerNamedTestAgentSession(t, s, "cr-goal-review-sub", os.Getpid())
		claim := func(id int64) {
			if _, err := s.DB().ExecContext(ctx, `UPDATE projects SET claimed_by = ? WHERE id = ?`, id, goal.ProjectID); err != nil {
				t.Fatal(err)
			}
		}
		claim(staleCmd)
		for _, step := range []func() error{
			func() error {
				_, err := s.RequestGoalHandoff(ctx, "cr-goal-review", goalID, staleCmd, "go")
				return err
			},
			func() error { _, err := s.ReceiveGoalHandoff(ctx, "cr-goal-review", goalID, sub); return err },
			func() error {
				_, err := s.RequestGoalHandoffReview(ctx, "cr-goal-review", goalID, sub, "r")
				return err
			},
			func() error {
				_, err := s.ReceiveGoalHandoffReview(ctx, "cr-goal-review", goalID, staleCmd)
				return err
			},
		} {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
		claim(fresh)
		expireTestSessionLease(t, s, staleCmd)
		envs["goal review_received"] = recoverEnv{newGoalThread(s), "cr-goal-review", fresh, func() error { _, err := s.RecoverGoalHandoff(ctx, "cr-goal-review", goalID, fresh, "gone"); return err }, false}
	}
	return envs
}

func TestRecoverAppendsRequestEntryInEveryPhase(t *testing.T) {
	for name, e := range recoverEnvs(t) {
		t.Run(name, func(t *testing.T) {
			e.h.requireAtomic(t, "recover", e.id, e.recover)
			before := len(e.h.entries(e.id))
			if err := e.recover(); err != nil {
				t.Fatalf("recover: %v", err)
			}
			got := e.h.entries(e.id)
			last := got[len(got)-1]
			if len(got) != before+1 || last.Kind != HandoffEntryKindRequest || last.Body != "recovered: gone" || last.AuthorSessionID != e.caller {
				t.Fatalf("recover entries %d -> %d, last = %s/%q/author %d; want +1 request/%q/author %d", before, len(got), last.Kind, last.Body, last.AuthorSessionID, "recovered: gone", e.caller)
			}
			if !e.seeded {
				e.h.requireEntryInvariant(t, e.id)
			}
			if strings.HasSuffix(name, "review_received") {
				return // review recovery only clears the receipt; it is not terminal, so a retry is a second recovery
			}
			// An idempotent retry returns early and must not write again.
			if err := e.recover(); err != nil {
				t.Fatalf("recover retry: %v", err)
			}
			if n := len(e.h.entries(e.id)); n != before+1 {
				t.Fatalf("recover retry changed entries to %d, want %d", n, before+1)
			}
		})
	}
}

// Reclaim, task release and goal withdraw close a handoff outside the review
// flow; each must leave a completed entry in the same transaction.
func TestHandoffClosedOutsideTheReviewFlowAppendsCompletedEntry(t *testing.T) {
	ctx := context.Background()
	t.Run("task reclaim", func(t *testing.T) {
		s := newTestStore(t)
		taskID := addTestTasks(t, s, 1)[0]
		addLiveParentGoalClaim(t, s, taskID, "cc-reclaim-req")
		addTestAgentSession(t, s, "cc-reclaim-recv")
		req, recv := testSessionID("cc-reclaim-req"), testSessionID("cc-reclaim-recv")
		h := newTaskThread(s)
		if _, err := s.RequestTaskHandoff(ctx, "cc-reclaim-old", taskID, req, "go"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReceiveTaskHandoff(ctx, "cc-reclaim-old", taskID, recv); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?`, 999999, "dead", recv); err != nil {
			t.Fatal(err)
		}
		expireTestSessionLease(t, s, recv)
		h.requireOneMoreEntry(t, "reclaim closes the old handoff", "cc-reclaim-old", func() error {
			_, err := s.RequestTaskHandoff(ctx, "cc-reclaim-new", taskID, req, "again")
			return err
		}, HandoffEntryKindCompleted, taskHandoffReclaimedReport, recv)
		if got := h.entries("cc-reclaim-new"); len(got) != 1 || got[0].Kind != HandoffEntryKindRequest {
			t.Fatalf("new handoff entries = %+v, want one request", got)
		}
		h.requireEntryInvariant(t, "cc-reclaim-new")
	})
	t.Run("goal reclaim", func(t *testing.T) {
		s := newTestStore(t)
		goalID := newTestGoal(t, s)
		addLiveProjectClaim(t, s, goalID, "cc-greclaim-req")
		addTestAgentSession(t, s, "cc-greclaim-recv")
		req, recv := testSessionID("cc-greclaim-req"), testSessionID("cc-greclaim-recv")
		h := newGoalThread(s)
		if _, err := s.RequestGoalHandoff(ctx, "cc-greclaim-old", goalID, req, "go"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReceiveGoalHandoff(ctx, "cc-greclaim-old", goalID, recv); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().ExecContext(ctx, `UPDATE agent_sessions SET pid = ?, started_at = ? WHERE id = ?`, 999999, "dead", recv); err != nil {
			t.Fatal(err)
		}
		expireTestSessionLease(t, s, recv)
		h.requireOneMoreEntry(t, "reclaim closes the old handoff", "cc-greclaim-old", func() error {
			_, err := s.RequestGoalHandoff(ctx, "cc-greclaim-new", goalID, req, "again")
			return err
		}, HandoffEntryKindCompleted, goalHandoffReclaimedReport, recv)
		h.requireEntryInvariant(t, "cc-greclaim-new")
	})
	t.Run("task release", func(t *testing.T) {
		s := newTestStore(t)
		taskID := addTestTasks(t, s, 1)[0]
		addLiveParentGoalClaim(t, s, taskID, "cc-release-parent")
		addTestAgentSession(t, s, "cc-release-agent")
		owner := testSessionID("cc-release-agent")
		if _, err := s.UpdateTask(ctx, taskID, "doing", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ClaimTask(ctx, taskID, owner); err != nil {
			t.Fatal(err)
		}
		hs, err := s.ListTaskHandoffs(ctx, taskID)
		if err != nil || len(hs) != 1 {
			t.Fatalf("handoffs after claim = %+v, err=%v", hs, err)
		}
		h := newTaskThread(s)
		h.requireEntryInvariant(t, hs[0].ID) // claim-made handoff: empty request report
		h.requireOneMoreEntry(t, "release", hs[0].ID, func() error { _, err := s.UpdateTask(ctx, taskID, "todo", owner); return err },
			HandoffEntryKindCompleted, taskHandoffReleasedReport, owner)
	})
	t.Run("goal withdraw", func(t *testing.T) {
		s := newTestStore(t)
		taskID := addTestTasks(t, s, 1)[0]
		addLiveParentGoalClaim(t, s, taskID, "cc-withdraw-req")
		addTestAgentSession(t, s, "cc-withdraw-recv")
		req, recv := testSessionID("cc-withdraw-req"), testSessionID("cc-withdraw-recv")
		goalID, err := sqlcgen.New(s.DB()).GetTaskGoalID(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RequestTaskHandoff(ctx, "cc-withdraw", taskID, req, "go"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReceiveTaskHandoff(ctx, "cc-withdraw", taskID, recv); err != nil {
			t.Fatal(err)
		}
		h := newTaskThread(s)
		h.requireOneMoreEntry(t, "withdraw", "cc-withdraw", func() error { return s.WithdrawActiveGoal(ctx, goalID, "dropped") },
			HandoffEntryKindCompleted, "dropped", recv)
	})
}

// Every query that writes task_handoffs / goal_handoffs must be classified
// here. A new one fails this test until someone says which transition owns it
// and that the transition writes an entry (or why it need not).
var handoffWriteQueries = map[string]string{
	"RequestTaskHandoff":                "request (also reclaim's new handoff, task claim)",
	"ReceiveTaskHandoff":                "receive",
	"RequestTaskHandoffReview":          "review request",
	"ReceiveTaskHandoffReview":          "review receive",
	"RecoverTaskHandoffRequester":       "recover (requested)",
	"RecoverTaskHandoffReceiver":        "recover (received)",
	"RecoverTaskHandoffReview":          "recover (review_received)",
	"RejectTaskHandoffReview":           "reject",
	"ReceiveTaskHandoffReviewRejection": "reject receive",
	"CompleteTaskHandoffByReviewer":     "complete by reviewer",
	"CompleteTaskHandoff":               "complete / reclaim / task release / goal withdraw",
	"AmendTaskHandoffReport":            "amend",
	"RequestGoalHandoff":                "request (also reclaim's new handoff)",
	"ReceiveGoalHandoff":                "receive",
	"RequestGoalHandoffReview":          "review request",
	"ReceiveGoalHandoffReview":          "review receive",
	"ReReceiveGoalHandoffReview":        "review re-receive after takeover",
	"RecoverGoalHandoffRequester":       "recover (requested)",
	"RecoverGoalHandoffReceiver":        "recover (received)",
	"RecoverGoalHandoffReview":          "recover (review_received)",
	"RejectGoalHandoffReview":           "reject",
	"ReceiveGoalHandoffReviewRejection": "reject receive",
	"CompleteGoalHandoffByReviewer":     "complete by reviewer",
	"CompleteGoalHandoff":               "complete / reclaim",
	"AmendGoalHandoffReport":            "amend",
}

func TestEveryHandoffWriteQueryIsClassified(t *testing.T) {
	files, err := os.ReadDir("queries")
	if err != nil {
		t.Fatal(err)
	}
	nameRE := regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)`)
	writeRE := regexp.MustCompile(`(?is)\b(?:UPDATE|INSERT\s+(?:OR\s+\w+\s+)?INTO|REPLACE\s+INTO|DELETE\s+FROM)\s+(?:task_handoffs|goal_handoffs)\b`)
	found := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile("queries/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		locs := nameRE.FindAllStringSubmatchIndex(src, -1)
		for i, loc := range locs {
			end := len(src)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			if writeRE.MatchString(src[loc[1]:end]) {
				found[src[loc[2]:loc[3]]] = true
			}
		}
	}
	var unclassified, stale []string
	for name := range found {
		if _, ok := handoffWriteQueries[name]; !ok {
			unclassified = append(unclassified, name)
		}
	}
	for name := range handoffWriteQueries {
		if !found[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 || len(stale) > 0 {
		t.Fatalf("handoff write queries and classification differ.\nunclassified (add to handoffWriteQueries after making the transition write an entry): %v\nclassified but no longer present: %v", unclassified, stale)
	}
}

// A request without a report still has to leave a request entry, whether it
// comes from a claim's self-handoff or from a caller passing an empty report.
func TestRequestWithEmptyReportStillWritesRequestEntry(t *testing.T) {
	ctx := context.Background()
	t.Run("task request", func(t *testing.T) {
		s := newTestStore(t)
		taskID := addTestTasks(t, s, 1)[0]
		addLiveParentGoalClaim(t, s, taskID, "er-task-req")
		req := testSessionID("er-task-req")
		h := newTaskThread(s)
		h.requireOneMoreEntry(t, "empty request", "er-task", func() error { _, err := s.RequestTaskHandoff(ctx, "er-task", taskID, req, "  "); return err },
			HandoffEntryKindRequest, "requested", req)
	})
	t.Run("goal request", func(t *testing.T) {
		s := newTestStore(t)
		goalID := newTestGoal(t, s)
		addLiveProjectClaim(t, s, goalID, "er-goal-req")
		req := testSessionID("er-goal-req")
		h := newGoalThread(s)
		h.requireOneMoreEntry(t, "empty request", "er-goal", func() error { _, err := s.RequestGoalHandoff(ctx, "er-goal", goalID, req, ""); return err },
			HandoffEntryKindRequest, "requested", req)
	})
	t.Run("task claim", func(t *testing.T) {
		s := newTestStore(t)
		taskID := addTestTasks(t, s, 1)[0]
		addLiveParentGoalClaim(t, s, taskID, "er-claim-parent")
		addTestAgentSession(t, s, "er-claim-agent")
		if _, err := s.ClaimTask(ctx, taskID, testSessionID("er-claim-agent")); err != nil {
			t.Fatal(err)
		}
		hs, err := s.ListTaskHandoffs(ctx, taskID)
		if err != nil || len(hs) != 1 {
			t.Fatalf("handoffs = %+v, err=%v", hs, err)
		}
		newTaskThread(s).requireEntryInvariant(t, hs[0].ID)
	})
	t.Run("goal claim", func(t *testing.T) {
		s := newTestStore(t)
		goalID := newTestGoal(t, s)
		addLiveProjectClaim(t, s, goalID, "er-gclaim-cmd")
		addTestAgentSession(t, s, "er-gclaim-agent")
		if _, err := s.ClaimGoal(ctx, goalID, testSessionID("er-gclaim-agent")); err != nil {
			t.Fatal(err)
		}
		hs, err := s.ListGoalHandoffs(ctx, goalID)
		if err != nil || len(hs) == 0 {
			t.Fatalf("handoffs = %+v, err=%v", hs, err)
		}
		for _, h := range hs {
			newGoalThread(s).requireEntryInvariant(t, h.ID)
		}
	})
}
