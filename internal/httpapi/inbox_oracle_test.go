package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// inboxOracleFixture is a populated project for /api/inbox: goals in the states the
// batch reads cover, and an archived project's goal that the inbox must leave out.
type inboxOracleFixture struct {
	t         *testing.T
	ctx       context.Context
	s         *store.Store
	project   int64
	commander int64
	sub       int64
	exec      int64
}

func newInboxOracleFixture(t *testing.T) *inboxOracleFixture {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	project, err := s.CreateProject(ctx, "inbox-oracle", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &inboxOracleFixture{t: t, ctx: ctx, s: s, project: project.ID}
	f.commander = f.session()
	if _, err := s.ClaimProject(ctx, project.ID, f.commander); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	f.sub = f.session()
	f.exec = f.session()

	f.goal("plain")
	f.handedOff("empty")
	f.reviewRequested("review")
	f.attention("attention")
	f.tasks("tasks")
	f.planRejected("planrej")
	f.planRequested("planreq")
	f.rejectedPending("rejected-pending")
	f.statusGoal("done", "done")
	f.statusGoal("dropped", "dropped")
	f.archivedGoal()
	return f
}

func (f *inboxOracleFixture) session() int64 {
	f.t.Helper()
	id, err := f.s.RegisterAgentSession(f.ctx, os.Getpid())
	if err != nil {
		f.t.Fatalf("RegisterAgentSession: %v", err)
	}
	return id
}

func (f *inboxOracleFixture) goal(label string) int64 {
	f.t.Helper()
	g, err := f.s.CreateGoal(f.ctx, f.project, "oracle "+label, "human")
	if err != nil {
		f.t.Fatalf("CreateGoal %s: %v", label, err)
	}
	return g.ID
}

func (f *inboxOracleFixture) handedOff(label string) int64 {
	f.t.Helper()
	goalID := f.goal(label)
	if _, err := f.s.RequestGoalHandoff(f.ctx, label+"-gh", goalID, f.commander, "delegate"); err != nil {
		f.t.Fatalf("RequestGoalHandoff %s: %v", label, err)
	}
	if _, err := f.s.ReceiveGoalHandoff(f.ctx, label+"-gh", goalID, f.sub); err != nil {
		f.t.Fatalf("ReceiveGoalHandoff %s: %v", label, err)
	}
	return goalID
}

func (f *inboxOracleFixture) reviewRequested(label string) {
	f.t.Helper()
	goalID := f.handedOff(label)
	if _, err := f.s.RequestGoalHandoffReview(f.ctx, label+"-gh", goalID, f.sub, "ready"); err != nil {
		f.t.Fatalf("RequestGoalHandoffReview %s: %v", label, err)
	}
}

// attention asks a question on one of the goal's tasks, which puts the task on the inbox.
func (f *inboxOracleFixture) attention(label string) {
	f.t.Helper()
	goalID := f.handedOff(label)
	tasks, err := f.s.CreateTasks(f.ctx, goalID, "worker", "oracle-"+label, []string{"one"}, []string{"desc one"})
	if err != nil {
		f.t.Fatalf("CreateTasks %s: %v", label, err)
	}
	if _, err := f.s.AskDecision(f.ctx, store.AskInput{GoalID: goalID, TaskID: tasks[0].ID, Kind: domain.DecisionKind("question"), Question: "Which?", AgentSessionID: f.commander}); err != nil {
		f.t.Fatalf("AskDecision %s: %v", label, err)
	}
}

// tasks gives the goal three tasks in sort order: one with an open handoff, one
// whose handoff completed, and one still todo with no handoff.
func (f *inboxOracleFixture) tasks(label string) {
	f.t.Helper()
	goalID := f.handedOff(label)
	tasks, err := f.s.CreateTasks(f.ctx, goalID, "worker", "oracle-"+label, []string{"open", "completed", "todo"}, []string{"desc open", "desc completed", "desc todo"})
	if err != nil {
		f.t.Fatalf("CreateTasks %s: %v", label, err)
	}
	if _, err := f.s.RequestTaskHandoff(f.ctx, label+"-open", tasks[0].ID, f.sub, "open"); err != nil {
		f.t.Fatalf("RequestTaskHandoff open: %v", err)
	}
	if _, err := f.s.RequestTaskHandoff(f.ctx, label+"-done", tasks[1].ID, f.sub, "completed"); err != nil {
		f.t.Fatalf("RequestTaskHandoff completed: %v", err)
	}
	if _, err := f.s.ReceiveTaskHandoff(f.ctx, label+"-done", tasks[1].ID, f.exec); err != nil {
		f.t.Fatalf("ReceiveTaskHandoff completed: %v", err)
	}
	if _, err := f.s.CompleteTaskHandoff(f.ctx, label+"-done", tasks[1].ID, "finished"); err != nil {
		f.t.Fatalf("CompleteTaskHandoff: %v", err)
	}
}

// planRequested is a goal whose plan review is waiting for the commander.
func (f *inboxOracleFixture) planRequested(label string) int64 {
	f.t.Helper()
	goalID := f.handedOff(label)
	if _, err := f.s.UpdateGoalRequestReport(f.ctx, goalID, "# Spec", "# Plan"); err != nil {
		f.t.Fatalf("UpdateGoalRequestReport %s: %v", label, err)
	}
	if _, err := f.s.RequestPlanHandoffReview(f.ctx, label+"-plan", goalID, f.sub, "plan ready"); err != nil {
		f.t.Fatalf("RequestPlanHandoffReview %s: %v", label, err)
	}
	return goalID
}

// planRejected is a goal whose plan review the commander rejected.
func (f *inboxOracleFixture) planRejected(label string) {
	f.t.Helper()
	goalID := f.planRequested(label)
	if _, err := f.s.ReceivePlanHandoffReview(f.ctx, label+"-plan", goalID, f.commander); err != nil {
		f.t.Fatalf("ReceivePlanHandoffReview %s: %v", label, err)
	}
	if _, err := f.s.RejectPlanHandoffReview(f.ctx, label+"-plan", goalID, f.commander, "revise the plan"); err != nil {
		f.t.Fatalf("RejectPlanHandoffReview %s: %v", label, err)
	}
}

// rejectedPending is a goal whose goal review the commander rejected, before the
// goal handoff itself is rejected: the pending state reads the goal handoff.
func (f *inboxOracleFixture) rejectedPending(label string) {
	f.t.Helper()
	goalID := f.handedOff(label)
	if _, err := f.s.RequestGoalHandoffReview(f.ctx, label+"-gh", goalID, f.sub, "ready"); err != nil {
		f.t.Fatalf("RequestGoalHandoffReview %s: %v", label, err)
	}
	if _, err := f.s.ReceiveGoalHandoffReview(f.ctx, label+"-gh", goalID, f.commander); err != nil {
		f.t.Fatalf("ReceiveGoalHandoffReview %s: %v", label, err)
	}
	report := domain.CompletionReport{WorkDone: "w", NowPossible: "n", HowToVerify: "v", Surprises: "s", NeedsReview: "r"}
	review, err := f.s.RequestGoalReview(f.ctx, goalID, f.commander, report)
	if err != nil {
		f.t.Fatalf("RequestGoalReview %s: %v", label, err)
	}
	if err := f.s.RejectGoalReview(f.ctx, review.ID, "needs another pass"); err != nil {
		f.t.Fatalf("RejectGoalReview %s: %v", label, err)
	}
}

func (f *inboxOracleFixture) statusGoal(label, status string) {
	f.t.Helper()
	goalID := f.goal(label)
	if _, err := f.s.DB().ExecContext(f.ctx, `UPDATE goals SET status = ?, work_done = 'w', now_possible = 'n', how_to_verify = 'v', surprises = 's', needs_review = 'r' WHERE id = ?`, status, goalID); err != nil {
		f.t.Fatalf("set status %s: %v", label, err)
	}
}

// archivedGoal has an open decision and is then archived with its project.
func (f *inboxOracleFixture) archivedGoal() {
	f.t.Helper()
	project, err := f.s.CreateProject(f.ctx, "archived-oracle", f.t.TempDir())
	if err != nil {
		f.t.Fatalf("CreateProject archived: %v", err)
	}
	goal, err := f.s.CreateGoal(f.ctx, project.ID, "archived goal", "human")
	if err != nil {
		f.t.Fatalf("CreateGoal archived: %v", err)
	}
	if _, err := f.s.AskDecision(f.ctx, store.AskInput{GoalID: goal.ID, Kind: domain.DecisionKind("question"), Question: "Hidden?", AgentSessionID: f.commander}); err != nil {
		f.t.Fatalf("AskDecision archived: %v", err)
	}
	if _, err := f.s.ArchiveProject(f.ctx, project.ID); err != nil {
		f.t.Fatalf("ArchiveProject: %v", err)
	}
}

// legacyAwaitingReview is the review-wait predicate before the batch read, per goal.
func (f *inboxOracleFixture) legacyAwaitingReview(goalID int64) bool {
	f.t.Helper()
	goalHandoffs, err := f.s.ListGoalHandoffs(f.ctx, goalID)
	if err != nil {
		f.t.Fatalf("ListGoalHandoffs: %v", err)
	}
	for _, h := range goalHandoffs {
		if h.ReviewRequestedAt != nil && h.ReviewReceivedAt == nil && h.ReviewRejectedAt == nil && h.CompletedReportAt == nil && h.RecoveredAt == nil {
			return true
		}
	}
	planHandoffs, err := f.s.ListPlanHandoffs(f.ctx, goalID)
	if err != nil {
		f.t.Fatalf("ListPlanHandoffs: %v", err)
	}
	for _, h := range planHandoffs {
		if h.ReviewRequestedAt != nil && h.ReviewReceivedAt == nil && h.ReviewRejectedAt == nil && h.CompletedReportAt == nil {
			return true
		}
	}
	return false
}

// oracleInbox builds the inbox from per-goal reads only: each goal's tasks, open
// task handoffs and open decisions, with the legacy review predicate. It is shaped
// like inboxResponse, so the same JSON projection compares both.
func (f *inboxOracleFixture) oracleInbox() map[string]any {
	f.t.Helper()
	goals, err := f.s.ListAllGoalsForInbox(f.ctx)
	if err != nil {
		f.t.Fatalf("ListAllGoalsForInbox: %v", err)
	}
	projects, err := f.s.ListProjects(f.ctx)
	if err != nil {
		f.t.Fatalf("ListProjects: %v", err)
	}
	names := make(map[int64]string, len(projects))
	for _, p := range projects {
		names[p.ID] = p.Name
	}

	activeGoals := make([]goalView, 0)
	attention := make([]TaskView, 0)
	for _, goal := range goals {
		tasks, err := f.s.ListTasks(f.ctx, goal.ID)
		if err != nil {
			f.t.Fatalf("ListTasks: %v", err)
		}
		handoffs, err := f.s.ListOpenTaskHandoffsForGoal(f.ctx, goal.ID)
		if err != nil {
			f.t.Fatalf("ListOpenTaskHandoffsForGoal: %v", err)
		}
		open, err := f.s.ListOpenDecisions(f.ctx, goal.ID)
		if err != nil {
			f.t.Fatalf("ListOpenDecisions: %v", err)
		}
		byTask := indexDecisions(open)
		if goal.Status == domain.GoalActive {
			next, err := f.s.GoalNextAction(f.ctx, goal)
			if err != nil {
				f.t.Fatalf("GoalNextAction: %v", err)
			}
			views := make([]TaskView, 0, len(tasks))
			for _, task := range tasks {
				views = append(views, newTaskView(task, handoffs[task.ID], byTask[task.ID]))
			}
			activeGoals = append(activeGoals, goalView{
				Goal:             goal,
				AwaitingDecision: len(open) > 0,
				AwaitingReview:   f.legacyAwaitingReview(goal.ID),
				NextAction:       next,
				ProjectName:      names[goal.ProjectID],
				Tasks:            views,
			})
		}
		for _, task := range tasks {
			decisions := byTask[task.ID]
			if len(decisions) == 0 || !isProjectableTask(task.Status) {
				continue
			}
			view := newTaskView(task, handoffs[task.ID], decisions)
			view.ProjectID = goal.ProjectID
			view.ProjectName = names[goal.ProjectID]
			attention = append(attention, view)
		}
	}
	return jsonObject(f.t, inboxResponse{ActiveGoals: activeGoals, AttentionTasks: nonNilTaskViews(attention)})
}

// jsonObject round-trips v through JSON, so expected and served values compare as the same plain data.
func jsonObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// inboxProjection keeps what the batch reads feed: each active goal's id, next
// action, review wait, decision flag and tasks, and the attention tasks. The
// held_for_seconds field is measured against the clock, so it is dropped.
func inboxProjection(t *testing.T, inbox map[string]any) map[string]any {
	t.Helper()
	goals, ok := inbox["active_goals"].([]any)
	if !ok {
		t.Fatalf("active_goals is %T, want a list", inbox["active_goals"])
	}
	projected := make([]any, 0, len(goals))
	for _, raw := range goals {
		goal := raw.(map[string]any)
		projected = append(projected, map[string]any{
			"id":                goal["id"],
			"next_action":       goal["next_action"],
			"awaiting_review":   goal["awaiting_review"],
			"awaiting_decision": goal["awaiting_decision"],
			"tasks":             withoutHeldSeconds(goal["tasks"]),
		})
	}
	return map[string]any{
		"active_goals":    projected,
		"attention_tasks": withoutHeldSeconds(inbox["attention_tasks"]),
	}
}

func withoutHeldSeconds(v any) any {
	switch value := v.(type) {
	case []any:
		for _, item := range value {
			withoutHeldSeconds(item)
		}
	case map[string]any:
		delete(value, "held_for_seconds")
		for _, item := range value {
			withoutHeldSeconds(item)
		}
	}
	return v
}

// TestInboxBatchReadsMatchPerGoalOracle serves /api/inbox over a populated project
// and compares the active goals' next actions, review waits and tasks, and the
// attention tasks, with the inbox built from per-goal reads.
func TestInboxBatchReadsMatchPerGoalOracle(t *testing.T) {
	f := newInboxOracleFixture(t)
	server := httptest.NewServer(New(f.s).Handler())
	t.Cleanup(server.Close)

	response, err := server.Client().Get(server.URL + "/api/inbox")
	if err != nil {
		t.Fatalf("GET /api/inbox: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var served map[string]any
	if err := json.NewDecoder(response.Body).Decode(&served); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}

	want := inboxProjection(t, f.oracleInbox())
	got := inboxProjection(t, served)
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("inbox differs from the per-goal oracle\nserved:\n%s\noracle:\n%s", gotJSON, wantJSON)
	}
	// The oracle agreeing is not enough on its own: the populated state must be there.
	if n := len(got["active_goals"].([]any)); n != 8 {
		t.Fatalf("active goals = %d, want 8 (plain, empty, review, attention, tasks, planrej, planreq, rejected-pending; done, dropped and archived are not active)", n)
	}
	if n := len(got["attention_tasks"].([]any)); n != 1 {
		t.Fatalf("attention tasks = %d, want the one task with an open decision", n)
	}
}
