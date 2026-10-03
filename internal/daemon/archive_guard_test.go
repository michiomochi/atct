package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

// archiveGuardedMethods lists every method that is refused for an archived
// project. A method the handler dispatches must be here or in
// archiveExemptMethods.
var archiveGuardedMethods = []string{
	"project.claim",
	"goal.create", "goal.claim", "goal.release", "goal.withdraw", "goal.update_request_report",
	"goal.set_derived_from", "goal.complete", "goal.review.request", "goal.review.complete",
	"task.update_content", "task.create", "task.create_handoff.receive", "task.update",
	"task.handoff.request", "handoff.request", "handoff.entry.append", "task.handoff.receive",
	"handoff.receive", "handoff.complete", "task.handoff.review.request", "task.handoff.review.receive",
	"task.handoff.review.reject", "task.handoff.review.reject.receive", "task.handoff.complete",
	"task.handoff.report.amend",
	"goal.handoff.request", "goal.handoff.entry.append", "goal.handoff.receive",
	"goal.handoff.review.request", "goal.handoff.review.receive", "goal.handoff.review.reject",
	"goal.handoff.review.reject.receive", "goal.handoff.complete", "goal.handoff.report.amend",
	"plan.handoff.review.request", "plan.handoff.review.receive", "plan.handoff.review.reject",
	"plan.handoff.review.reject.receive", "plan.handoff.complete",
	"decision.ask", "decision.poll", "decision.withdraw",
}

func dispatchedMethods(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "handler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatchMethodWithPeer" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if sel, ok := sw.Tag.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Method" {
				return true
			}
			for _, stmt := range sw.Body.List {
				for _, e := range stmt.(*ast.CaseClause).List {
					if lit, ok := e.(*ast.BasicLit); ok {
						name, _ := strconv.Unquote(lit.Value)
						methods = append(methods, name)
					}
				}
			}
			return false
		})
		return false
	})
	if len(methods) == 0 {
		t.Fatal("found no dispatched methods in handler.go")
	}
	return methods
}

func TestArchiveGuardClassifiesEveryMethod(t *testing.T) {
	guarded := map[string]bool{}
	for _, m := range archiveGuardedMethods {
		if archiveExemptMethods[m] {
			t.Errorf("%q is in both archiveExemptMethods and archiveGuardedMethods", m)
		}
		guarded[m] = true
	}
	dispatched := map[string]bool{}
	var unclassified []string
	for _, m := range dispatchedMethods(t) {
		dispatched[m] = true
		if !archiveExemptMethods[m] && !guarded[m] {
			unclassified = append(unclassified, m)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("methods in neither archiveExemptMethods nor archiveGuardedMethods: %s", strings.Join(unclassified, ", "))
	}
	for m := range archiveExemptMethods {
		if !dispatched[m] {
			t.Errorf("archiveExemptMethods names %q, which the handler does not dispatch", m)
		}
	}
	for m := range guarded {
		if !dispatched[m] {
			t.Errorf("archiveGuardedMethods names %q, which the handler does not dispatch", m)
		}
	}
}

type archiveGuardFixture struct {
	d                           *Daemon
	s                           *store.Store
	project                     domain.Project
	goal                        domain.Goal
	taskID, decisionID, session int64
}

func newArchiveGuardFixture(t *testing.T) archiveGuardFixture {
	t.Helper()
	ctx := context.Background()
	s := openPendingResponseTestStore(t)
	project := createPendingResponseProject(t, s, t.TempDir(), "guarded-proj")
	goal, err := s.CreateGoal(ctx, project.ID, "guarded goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.CreateTasks(ctx, goal.ID, "agent", "k", []string{"t"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.AskDecision(ctx, store.AskInput{GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision, Question: "q",
		Options: []domain.Option{{Label: "A"}, {Label: "B"}}})
	if err != nil {
		t.Fatal(err)
	}
	return archiveGuardFixture{d: New(s), s: s, project: project, goal: goal, taskID: tasks[0].ID, decisionID: decision.ID,
		session: daemonTestSessionID(t, s, "archive-guard")}
}

func (f archiveGuardFixture) call(t *testing.T, method string, params map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.d.dispatchMethod(context.Background(), rpc.Request{Method: method, Params: raw})
	return err
}

func TestArchivedProjectRefusesChangesAndAllowsReads(t *testing.T) {
	ctx := context.Background()
	f := newArchiveGuardFixture(t)
	if _, err := f.s.ArchiveProject(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	refused := map[string]map[string]any{
		"goal.create":                 {"cwd": f.project.RootPath, "content": "x", "creator": "human"},
		"goal.claim":                  {"goal_id": f.goal.ID, "agent_session_id": f.session},
		"task.create":                 {"goal_id": f.goal.ID},
		"task.handoff.request":        {"task_id": f.taskID},
		"decision.ask":                {"goal_id": f.goal.ID},
		"decision.poll":               {"decision_id": f.decisionID},
		"goal.handoff.review.request": {"goal_id": f.goal.ID},
		"plan.handoff.review.request": {"goal_id": f.goal.ID},
		"project.claim":               {"project_id": f.project.ID, "agent_session_id": f.session},
	}
	for method, params := range refused {
		err := f.call(t, method, params)
		if !errors.Is(err, store.ErrProjectArchived) {
			t.Errorf("%s: err = %v, want ErrProjectArchived", method, err)
			continue
		}
		for _, want := range []string{"guarded-proj", "atct project unarchive"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q lacks %q", method, err, want)
			}
		}
	}
	allowed := map[string]map[string]any{
		"goal.get":         {"goal_id": f.goal.ID},
		"goal.list":        {"cwd": f.project.RootPath},
		"project.release":  {"project_id": f.project.ID, "agent_session_id": f.session},
		"session.identify": {"session_key": "archive-guard-key", "cwd": f.project.RootPath},
	}
	for method, params := range allowed {
		if err := f.call(t, method, params); errors.Is(err, store.ErrProjectArchived) {
			t.Errorf("%s: refused for archived project: %v", method, err)
		}
	}

	if _, err := f.s.UnarchiveProject(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	for method, params := range refused {
		if err := f.call(t, method, params); errors.Is(err, store.ErrProjectArchived) {
			t.Errorf("%s: still refused after unarchive: %v", method, err)
		}
	}
}

func TestProjectArchiveRPCRoundTrip(t *testing.T) {
	f := newArchiveGuardFixture(t)
	call := func(method string) domain.Project {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"project_id": f.project.ID})
		out, err := f.d.dispatchMethod(context.Background(), rpc.Request{Method: method, Params: raw})
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		var p domain.Project
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := call("project.archive")
	if first.ArchivedAt == nil {
		t.Fatal("project.archive returned no archived_at")
	}
	if again := call("project.archive"); again.ArchivedAt == nil || !again.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("second archive = %+v, want original archived_at", again)
	}
	out, err := f.d.dispatchMethod(context.Background(), rpc.Request{Method: "project.list"})
	if err != nil || !strings.Contains(string(out), `"archived_at"`) {
		t.Fatalf("project.list = %s, %v; want archived_at", out, err)
	}
	if un := call("project.unarchive"); un.ArchivedAt != nil {
		t.Fatalf("project.unarchive = %+v", un)
	}
	if un := call("project.unarchive"); un.ArchivedAt != nil {
		t.Fatalf("second unarchive = %+v", un)
	}
}

func TestStopCheckDoesNotBlockForArchivedProject(t *testing.T) {
	ctx := context.Background()
	fixture := newStopCheckFixture(t)
	d, s := fixture.daemon, fixture.store
	commander := func() (string, error) { return d.stopCheckCommander(ctx, fixture.projectID) }
	// The commander blocks only while no handoff delegates the goal, so check it first.
	if detail, err := commander(); err != nil || detail == "" {
		t.Fatalf("commander before archive: detail=%q err=%v, want a block", detail, err)
	}
	stopCheckGoalHandoff(t, fixture, true)
	tasks, err := s.CreateTasks(ctx, fixture.goalID, "agent", "stop-k", []string{"t"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestTaskHandoff(ctx, "stop-archive-task", tasks[0].ID, fixture.subcommanderID, "go"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, "stop-archive-task", tasks[0].ID, fixture.executorID); err != nil {
		t.Fatal(err)
	}

	roles := map[string]func() (string, error){
		"subcommander": func() (string, error) { return d.stopCheckSubcommander(ctx, fixture.subcommanderID, fixture.goalID) },
		"executor":     func() (string, error) { return d.stopCheckExecutor(ctx, fixture.executorID) },
	}
	for name, check := range roles {
		if detail, err := check(); err != nil || detail == "" {
			t.Fatalf("%s before archive: detail=%q err=%v, want a block", name, detail, err)
		}
	}
	if _, err := s.ArchiveProject(ctx, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	roles["commander"] = commander
	for name, check := range roles {
		if detail, err := check(); err != nil || detail != "" {
			t.Errorf("%s archived: detail=%q err=%v, want no block", name, detail, err)
		}
	}
}

func TestWakeupSkipsArchivedProject(t *testing.T) {
	ctx := context.Background()
	s := newWakeupTestStore(t)
	projectID, _ := newWakeupTestGoal(t, s, "arch")
	evaluated := map[int64]bool{}
	evaluate := func(_ context.Context, id int64) (store.WakeupState, error) {
		evaluated[id] = true
		return store.WakeupState{}, nil
	}
	now := time.Now()
	tracker := newWakeupTracker(now)
	if _, err := tracker.evaluateWith(ctx, s, now, evaluate); err != nil {
		t.Fatal(err)
	}
	if !evaluated[projectID] {
		t.Fatal("active project was not evaluated")
	}
	delete(evaluated, projectID)
	if _, err := s.ArchiveProject(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.evaluateWith(ctx, s, now.Add(time.Hour), evaluate); err != nil {
		t.Fatal(err)
	}
	if evaluated[projectID] {
		t.Fatal("archived project was evaluated")
	}
}
