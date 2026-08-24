package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func newTestGoal(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	ns, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	g, err := s.CreateGoal(ctx, ns.ID, "goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	return g.ID
}

func newOrderTestGoals(t *testing.T, s *Store) (string, string) {
	t.Helper()
	ctx := context.Background()
	ns, err := s.CreateProject(ctx, "atct-order", "/repos/atct-order")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	first, err := s.CreateGoal(ctx, ns.ID, "first order goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal first: %v", err)
	}
	second, err := s.CreateGoal(ctx, ns.ID, "second order goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal second: %v", err)
	}
	return first.ID, second.ID
}

func declareOrderTestBatches(t *testing.T, s *Store, goalID string) {
	t.Helper()
	ctx := context.Background()
	firstTitles := []string{"Collect the requirements", "Implement the store change", "Verify the behavior"}
	firstDescriptions := []string{
		"Record the ordering requirements and affected store queries.",
		"Assign each new task the next available order within the goal.",
		"Exercise repeated declarations and check the persisted order values.",
	}
	secondTitles := []string{"Document the result", "Run the full verification"}
	secondDescriptions := []string{
		"Summarize the final ordering behavior for the implementation handoff.",
		"Run the build, tests, and wrapper checks before reporting completion.",
	}
	if _, err := s.DeclareTasks(ctx, goalID, "codex", "order-batch-1", firstTitles, firstDescriptions); err != nil {
		t.Fatalf("first DeclareTasks: %v", err)
	}
	if _, err := s.DeclareTasks(ctx, goalID, "codex", "order-batch-2", secondTitles, secondDescriptions); err != nil {
		t.Fatalf("second DeclareTasks: %v", err)
	}
}

func TestDeclareTasksContinuesSortOrderAcrossBatches(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID, otherGoalID := newOrderTestGoals(t, s)
	declareOrderTestBatches(t, s, goalID)

	otherTitles := []string{"Start the independent goal"}
	otherDescriptions := []string{"Begin the other goal at its own first task order."}
	if _, err := s.DeclareTasks(ctx, otherGoalID, "codex", "other-goal", otherTitles, otherDescriptions); err != nil {
		t.Fatalf("other goal DeclareTasks: %v", err)
	}

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	got := make([]int, len(tasks))
	for i, task := range tasks {
		got[i] = task.Order
	}
	if want := []int{0, 1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("task orders = %v, want %v", got, want)
	}
}

func TestDeclareTasksDoesNotDuplicateSortOrderWithinGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID, _ := newOrderTestGoals(t, s)
	declareOrderTestBatches(t, s, goalID)

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	orders := make(map[int]struct{}, len(tasks))
	for _, task := range tasks {
		orders[task.Order] = struct{}{}
	}
	if len(orders) != len(tasks) {
		t.Fatalf("task order values contain duplicates: %v", tasks)
	}
}

func TestDeclareTasksDoesNotResetSortOrderOnRedeclare(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID, _ := newOrderTestGoals(t, s)
	declareOrderTestBatches(t, s, goalID)

	before, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks before re-declare: %v", err)
	}
	changedTitles := []string{"Replacement documentation", "Replacement verification"}
	changedDescriptions := []string{
		"This changed input must not replace the original stored documentation task.",
		"This changed input must not replace the original stored verification task.",
	}
	if _, err := s.DeclareTasks(ctx, goalID, "codex", "order-batch-2", changedTitles, changedDescriptions); err != nil {
		t.Fatalf("re-declare: %v", err)
	}
	after, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks after re-declare: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("task count after re-declare = %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].Order != before[i].Order {
			t.Fatalf("task %d changed on re-declare: before=%+v after=%+v", i, before[i], after[i])
		}
	}
}

func TestDeclareTasksKeepsSortOrderIndependentAcrossGoals(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID, otherGoalID := newOrderTestGoals(t, s)
	declareOrderTestBatches(t, s, goalID)

	titles := []string{"Independent first task", "Independent second task"}
	descriptions := []string{
		"Create the first task in the independent goal.",
		"Create the second task in the independent goal.",
	}
	if _, err := s.DeclareTasks(ctx, otherGoalID, "codex", "independent-goal", titles, descriptions); err != nil {
		t.Fatalf("independent goal DeclareTasks: %v", err)
	}
	otherTasks, err := s.ListTasks(ctx, otherGoalID)
	if err != nil {
		t.Fatalf("ListTasks independent goal: %v", err)
	}
	if got, want := []int{otherTasks[0].Order, otherTasks[1].Order}, []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("independent goal task orders = %v, want %v", got, want)
	}
}

func TestListTasksUsesSortOrderAndIDAsTieBreakers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	insertTask := func(id, title, declareKey string, sortOrder int, createdAt string) {
		t.Helper()
		_, err := s.DB().ExecContext(ctx, `
INSERT INTO tasks (
  id, goal_id, title, description, status, agent, files, sort_order, declare_key,
  claimed_by, claimed_at, created_at, updated_at
)
VALUES (?, ?, ?, ?, 'todo', '', '[]', ?, ?, '', NULL, ?, ?)`,
			id, goalID, title, "Verify the stable sort-order ordering for this fixture.", sortOrder, declareKey, createdAt, createdAt)
		if err != nil {
			t.Fatalf("insert task %s: %v", id, err)
		}
	}
	insertTask("task-sort-three", "sort three", "sort-three", 3, "2026-08-20T00:00:01Z")
	insertTask("task-sort-zero", "sort zero", "sort-zero", 0, "2026-08-20T00:00:04Z")
	insertTask("task-sort-two", "sort two", "sort-two", 2, "2026-08-20T00:00:03Z")
	insertTask("task-sort-one", "sort one", "sort-one", 1, "2026-08-20T00:00:02Z")

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	got := make([]string, len(tasks))
	for i, task := range tasks {
		got[i] = task.ID
	}
	want := []string{"task-sort-zero", "task-sort-one", "task-sort-two", "task-sort-three"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("task IDs = %v, want %v", got, want)
	}
}

func TestDeclareTasksRejectsEmptyDescription(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	_, err := s.DeclareTasks(context.Background(), goalID, "codex", "empty-description", []string{"Implement the task"}, []string{""})
	if err == nil || !strings.HasPrefix(err.Error(), "declare tasks:") {
		t.Fatalf("DeclareTasks error = %v, want declare tasks error for empty description", err)
	}
}

func TestDeclareTasksRejectsWhitespaceOnlyDescription(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	_, err := s.DeclareTasks(context.Background(), goalID, "codex", "whitespace-description", []string{"Implement the task"}, []string{" \t\n"})
	if err == nil || !strings.HasPrefix(err.Error(), "declare tasks:") {
		t.Fatalf("DeclareTasks error = %v, want declare tasks error for whitespace-only description", err)
	}
}

func TestDeclareTasksRejectsDescriptionTitleLengthMismatch(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	_, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-length", []string{"Design the change", "Implement the change"}, []string{"Describe the schema change"})
	if err == nil || !strings.HasPrefix(err.Error(), "declare tasks:") {
		t.Fatalf("DeclareTasks error = %v, want declare tasks error for mismatched descriptions", err)
	}
}

func TestDeclareTasksKeepsOriginalDescriptionOnRedeclare(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	titles := []string{"Add the task column", "Validate task declarations"}
	original := []string{
		"Persist the task explanation alongside its title.",
		"Reject declarations that do not explain completion and assumptions.",
	}
	changed := []string{
		"This replacement explanation must not overwrite the stored value.",
		"A second declaration keeps the first explanation for each task.",
	}

	first, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-idempotency", titles, original)
	if err != nil {
		t.Fatalf("first DeclareTasks: %v", err)
	}
	second, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-idempotency", titles, changed)
	if err != nil {
		t.Fatalf("second DeclareTasks: %v", err)
	}
	if len(first) != len(original) || len(second) != len(original) {
		t.Fatalf("DeclareTasks returned %d and %d tasks, want %d", len(first), len(second), len(original))
	}
	for i, want := range original {
		if second[i].Description != want {
			t.Fatalf("re-declared task %d description = %q, want original %q", i, second[i].Description, want)
		}
	}
}

func TestDeclareTasksPersistsDescriptions(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	titles := []string{"Add the migration", "Read descriptions from the store"}
	want := []string{
		"Add a non-null default so existing task rows remain valid.",
		"Return each stored explanation when tasks are listed.",
	}

	if _, err := s.DeclareTasks(context.Background(), goalID, "codex", "description-persistence", titles, want); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != len(want) {
		t.Fatalf("ListTasks returned %d tasks, want %d", len(tasks), len(want))
	}
	for i, description := range want {
		if tasks[i].Description != description {
			t.Fatalf("task %d description = %q, want %q", i, tasks[i].Description, description)
		}
	}
}

func TestDeclareTasksIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	titles := []string{"Design", "Implement", "Test"}
	descriptions := []string{
		"Define the implementation boundaries and data flow.",
		"Implement the requested change in the Go store.",
		"Verify the behavior with focused and full tests.",
	}

	first, err := s.DeclareTasks(ctx, goalID, "codex", "key-1", titles, descriptions)
	if err != nil {
		t.Fatalf("first DeclareTasks: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("first returned %d tasks, want 3", len(first))
	}

	second, err := s.DeclareTasks(ctx, goalID, "codex", "key-1", titles, descriptions)
	if err != nil {
		t.Fatalf("second DeclareTasks: %v", err)
	}
	if len(second) != 3 {
		t.Fatalf("second returned %d tasks, want 3", len(second))
	}

	all, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("stored %d tasks after duplicate declare, want 3", len(all))
	}
	if all[0].ID != first[0].ID {
		t.Fatalf("task id changed on re-declare: %s -> %s", first[0].ID, all[0].ID)
	}
}

func declareOneTaskWithFiles(t *testing.T, s *Store, goalID, key, title string, files []string) string {
	t.Helper()
	description := "Complete the task titled " + title + " and verify its declared files."
	tasks, err := s.DeclareTasks(context.Background(), goalID, "codex", key, []string{title}, []string{description}, [][]string{files})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	for _, task := range tasks {
		if task.DeclareKey == key+"#0" {
			return task.ID
		}
	}
	t.Fatalf("DeclareTasks did not return task for key %q: %+v", key, tasks)
	return ""
}

func TestDeclareTasksPersistsFiles(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	want := []string{"internal/store/task.go", "internal/domain/model.go"}
	taskID := declareOneTaskWithFiles(t, s, goalID, "files-1", "declare files", want)

	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("ListTasks returned %+v, want task %s", tasks, taskID)
	}
	if !reflect.DeepEqual(tasks[0].Files, want) {
		t.Fatalf("task files = %#v, want %#v", tasks[0].Files, want)
	}
}

func TestClaimTaskRejectsOverlappingFilesAcrossAgentSessions(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "conflict-1", "first", []string{"internal/store/task.go"})
	secondID := declareOneTaskWithFiles(t, s, goalID, "conflict-2", "second", []string{"internal/store/task.go"})
	addTestAgentSession(t, s, "run-1")
	addTestAgentSession(t, s, "run-2")

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-2"); !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("second ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
}

func TestClaimTaskAllowsNonOverlappingFilesAcrossAgentSessions(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "non-overlap-1", "first", []string{"internal/store/task.go"})
	secondID := declareOneTaskWithFiles(t, s, goalID, "non-overlap-2", "second", []string{"internal/store/schema.go"})
	addTestAgentSession(t, s, "run-1")
	addTestAgentSession(t, s, "run-2")

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-2"); err != nil {
		t.Fatalf("second ClaimTask: %v", err)
	}
}

func TestClaimTaskAllowsOverlappingFilesForSameAgentSession(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "same-run-1", "first", []string{"internal/store/task.go"})
	secondID := declareOneTaskWithFiles(t, s, goalID, "same-run-2", "second", []string{"internal/store/task.go"})
	addTestAgentSession(t, s, "run-1")

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-1"); err != nil {
		t.Fatalf("same-run ClaimTask: %v", err)
	}
}

func TestClaimTaskIgnoresUndeclaredFiles(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "empty-files-1", "first", nil)
	secondID := declareOneTaskWithFiles(t, s, goalID, "empty-files-2", "second", []string{"internal/store/task.go"})
	addTestAgentSession(t, s, "run-1")
	addTestAgentSession(t, s, "run-2")

	if _, err := s.ClaimTask(context.Background(), firstID, "run-1"); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, "run-2"); err != nil {
		t.Fatalf("second ClaimTask: %v", err)
	}
}

func TestClaimTaskUsesSelfHandoffWithoutWritingClaimedBy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := declareOneTaskWithFiles(t, s, goalID, "self-handoff", "self handoff", nil)
	const ownerID = "task-handoff-owner"
	const nextOwnerID = "task-handoff-next-owner"
	if err := s.RegisterAgentSession(ctx, ownerID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession(%s): %v", ownerID, err)
	}
	if err := s.RegisterAgentSession(ctx, nextOwnerID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession(%s): %v", nextOwnerID, err)
	}

	if _, err := s.ClaimTask(ctx, taskID, ownerID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	handoffs, err := s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		t.Fatalf("ListTaskHandoffs after claim: %v", err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("task handoffs after claim = %d, want 1", len(handoffs))
	}
	if handoffs[0].RequestedBy != ownerID || handoffs[0].ReceivedBy != ownerID || handoffs[0].RequestedAt == nil || handoffs[0].ReceivedAt == nil || handoffs[0].CompletedReportAt != nil {
		t.Fatalf("task self handoff = %+v, want received and open for %q", handoffs[0], ownerID)
	}

	if _, err := s.ClaimTask(ctx, taskID, nextOwnerID); !errors.Is(err, ErrTaskAlreadyClaimed) {
		t.Fatalf("second ClaimTask error = %v, want ErrTaskAlreadyClaimed", err)
	}

	if _, err := s.UpdateTask(ctx, taskID, domain.TaskTodo, ownerID); err != nil {
		t.Fatalf("release task through UpdateTask: %v", err)
	}
	handoffs, err = s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		t.Fatalf("ListTaskHandoffs after release: %v", err)
	}
	if len(handoffs) != 1 || handoffs[0].CompletedReportAt == nil {
		t.Fatalf("task handoffs after release = %+v, want one completed handoff", handoffs)
	}

	if _, err := s.ClaimTask(ctx, taskID, nextOwnerID); err != nil {
		t.Fatalf("ClaimTask after release: %v", err)
	}
	handoffs, err = s.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		t.Fatalf("ListTaskHandoffs after reclaim: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("task handoffs after reclaim = %d, want 2", len(handoffs))
	}
	var reclaimed *TaskHandoff
	for i := range handoffs {
		if handoffs[i].RequestedBy == nextOwnerID && handoffs[i].ReceivedBy == nextOwnerID && handoffs[i].CompletedReportAt == nil {
			handoff := handoffs[i]
			reclaimed = &handoff
			break
		}
	}
	if reclaimed == nil {
		t.Fatalf("reclaimed task self handoff = %+v, want open for %q", handoffs, nextOwnerID)
	}
}

func TestClaimGoalUsesSelfHandoffWithoutWritingClaimedBy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	const ownerID = "goal-handoff-owner"
	const nextOwnerID = "goal-handoff-next-owner"
	if err := s.RegisterAgentSession(ctx, ownerID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession(%s): %v", ownerID, err)
	}
	if err := s.RegisterAgentSession(ctx, nextOwnerID, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession(%s): %v", nextOwnerID, err)
	}

	if _, err := s.ClaimGoal(ctx, goalID, ownerID); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs after claim: %v", err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("goal handoffs after claim = %d, want 1", len(handoffs))
	}
	if handoffs[0].RequestedBy != ownerID || handoffs[0].ReceivedBy != ownerID || handoffs[0].RequestedAt == nil || handoffs[0].ReceivedAt == nil || handoffs[0].CompletedReportAt != nil {
		t.Fatalf("goal self handoff = %+v, want received and open for %q", handoffs[0], ownerID)
	}

	if _, err := s.ClaimGoal(ctx, goalID, nextOwnerID); !errors.Is(err, ErrGoalAlreadyClaimed) {
		t.Fatalf("second ClaimGoal error = %v, want ErrGoalAlreadyClaimed", err)
	}

	if err := s.ReleaseGoal(ctx, goalID); err != nil {
		t.Fatalf("ReleaseGoal: %v", err)
	}
	handoffs, err = s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs after release: %v", err)
	}
	if len(handoffs) != 1 || handoffs[0].CompletedReportAt == nil {
		t.Fatalf("goal handoffs after release = %+v, want one completed handoff", handoffs)
	}

	if _, err := s.ClaimGoal(ctx, goalID, nextOwnerID); err != nil {
		t.Fatalf("ClaimGoal after release: %v", err)
	}
	handoffs, err = s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		t.Fatalf("ListGoalHandoffs after reclaim: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("goal handoffs after reclaim = %d, want 2", len(handoffs))
	}
	var reclaimed *GoalHandoff
	for i := range handoffs {
		if handoffs[i].RequestedBy == nextOwnerID && handoffs[i].ReceivedBy == nextOwnerID && handoffs[i].CompletedReportAt == nil {
			handoff := handoffs[i]
			reclaimed = &handoff
			break
		}
	}
	if reclaimed == nil {
		t.Fatalf("reclaimed goal self handoff = %+v, want open for %q", handoffs, nextOwnerID)
	}
}

func TestClaimTaskIgnoresTerminalClaims(t *testing.T) {
	for _, status := range []string{"done", "dropped"} {
		t.Run(status, func(t *testing.T) {
			s := newTestStore(t)
			goalID := newTestGoal(t, s)
			ownerID := declareOneTaskWithFiles(t, s, goalID, "terminal-owner-"+status, "owner", []string{"internal/store/task.go"})
			candidateID := declareOneTaskWithFiles(t, s, goalID, "terminal-candidate-"+status, "candidate", []string{"internal/store/task.go"})
			addTestAgentSession(t, s, "run-owner")
			addTestAgentSession(t, s, "run-candidate")

			if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
				t.Fatalf("owner ClaimTask: %v", err)
			}
			if _, err := s.DB().Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC().Format(time.RFC3339Nano), ownerID); err != nil {
				t.Fatalf("mark owner %s: %v", status, err)
			}
			if _, err := s.ClaimTask(context.Background(), candidateID, "run-candidate"); err != nil {
				t.Fatalf("candidate ClaimTask: %v", err)
			}
		})
	}
}

func TestClaimTaskConflictErrorNamesTaskAndFile(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "error-owner", "owner task", []string{"internal/store/task.go"})
	candidateID := declareOneTaskWithFiles(t, s, goalID, "error-candidate", "candidate task", []string{"internal/store/task.go"})
	addTestAgentSession(t, s, "run-owner")
	addTestAgentSession(t, s, "run-candidate")

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), candidateID, "run-candidate")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{ownerID, "owner task", "internal/store/task.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain %q", err, want)
		}
	}
}

func TestClaimTaskConflictErrorReturnsClaimableCandidates(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "candidate-owner", "owner task", []string{"internal/store/task.go"})
	blockedID := declareOneTaskWithFiles(t, s, goalID, "candidate-blocked", "blocked alternative", []string{"internal/store/task.go"})
	alternativeID := declareOneTaskWithFiles(t, s, goalID, "candidate-safe", "safe alternative", []string{"internal/store/schema.go"})
	targetID := declareOneTaskWithFiles(t, s, goalID, "candidate-target", "target task", []string{"internal/store/task.go"})
	addTestAgentSession(t, s, "run-owner")
	addTestAgentSession(t, s, "run-target")

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, "run-target")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{alternativeID, "safe alternative", "alternatives"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), blockedID) {
		t.Fatalf("ClaimTask error %q includes a conflicting alternative %s", err, blockedID)
	}
}

func TestClaimTaskConflictCandidatesAreClaimable(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "claimable-owner", "owner task", []string{"internal/store/task.go"})
	targetID := declareOneTaskWithFiles(t, s, goalID, "claimable-target", "target task", []string{"internal/store/task.go"})
	fileAlternativeID := declareOneTaskWithFiles(t, s, goalID, "claimable-file", "file alternative", []string{"internal/store/schema.go"})
	emptyAlternativeID := declareOneTaskWithFiles(t, s, goalID, "claimable-empty", "empty alternative", nil)
	addTestAgentSession(t, s, "run-owner")
	addTestAgentSession(t, s, "run-target")

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, "run-target")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{fileAlternativeID, "file alternative", emptyAlternativeID, "empty alternative"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain candidate %q", err, want)
		}
	}
	for _, taskID := range []string{fileAlternativeID, emptyAlternativeID} {
		if _, err := s.ClaimTask(context.Background(), taskID, "run-target"); err != nil {
			t.Fatalf("candidate %s ClaimTask: %v", taskID, err)
		}
	}
}

func TestClaimTaskConflictErrorReportsNoCandidates(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	ownerID := declareOneTaskWithFiles(t, s, goalID, "no-candidate-owner", "owner task", []string{"internal/store/task.go"})
	targetID := declareOneTaskWithFiles(t, s, goalID, "no-candidate-target", "target task", []string{"internal/store/task.go"})
	addTestAgentSession(t, s, "run-owner")
	addTestAgentSession(t, s, "run-target")

	if _, err := s.ClaimTask(context.Background(), ownerID, "run-owner"); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, "run-target")
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	if !strings.Contains(err.Error(), "alternatives: []") {
		t.Fatalf("ClaimTask error %q does not report that no alternatives are available", err)
	}
}

func TestOpenMigratesTasksFilesColumnWithoutLosingData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "atct.db")
	oldDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open old DB: %v", err)
	}
	oldSchema := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, root_path TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL)`,
		`CREATE TABLE goals (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, result_summary TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, goal_id TEXT NOT NULL REFERENCES goals(id), title TEXT NOT NULL, status TEXT NOT NULL, agent TEXT NOT NULL DEFAULT '', sort_order INTEGER NOT NULL DEFAULT 0, declare_key TEXT NOT NULL, claimed_by TEXT NOT NULL DEFAULT '', claimed_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	}
	for _, statement := range oldSchema {
		if _, err := oldDB.Exec(statement); err != nil {
			t.Fatalf("create old schema: %v", err)
		}
	}
	if _, err := oldDB.Exec(`INSERT INTO projects(id, name, root_path, created_at) VALUES ('project-old', 'old', '/repos/old', '2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert old project: %v", err)
	}
	if _, err := oldDB.Exec(`INSERT INTO goals(id, project_id, title, description, status, created_at, updated_at) VALUES ('goal-old', 'project-old', 'old goal', '', 'active', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert old goal: %v", err)
	}
	if _, err := oldDB.Exec(`INSERT INTO tasks(id, goal_id, title, status, agent, sort_order, declare_key, claimed_by, claimed_at, created_at, updated_at) VALUES ('task-old', 'goal-old', 'old task', 'todo', 'old-agent', 0, 'old-key', '', NULL, '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert old task: %v", err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatalf("close old DB: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open migrated DB: %v", err)
	}
	defer s.Close()

	tasks, err := s.ListTasks(context.Background(), "goal-old")
	if err != nil {
		t.Fatalf("ListTasks after migration: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "task-old" || tasks[0].Title != "old task" {
		t.Fatalf("migrated tasks = %+v, want original task", tasks)
	}
	if len(tasks[0].Files) != 0 {
		t.Fatalf("migrated task files = %#v, want empty", tasks[0].Files)
	}
	var storedFiles string
	if err := s.DB().QueryRow(`SELECT files FROM tasks WHERE id = 'task-old'`).Scan(&storedFiles); err != nil {
		t.Fatalf("read migrated files column: %v", err)
	}
	if storedFiles != "[]" {
		t.Fatalf("migrated files value = %q, want []", storedFiles)
	}
}

type taskStatusClaimFixture struct {
	store     *Store
	ctx       context.Context
	taskID    string
	projectID string
	holderID  string
	otherID   string
}

func newTaskStatusClaimFixture(t *testing.T, holderPID, otherPID int) taskStatusClaimFixture {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct-status-claims", filepath.Join(t.TempDir(), "atct-status-claims"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "status claim guard", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "status-claim-guard", []string{"Guard the status update"}, []string{"Ensure status updates respect the task claim owner and liveness."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	const holderID = "status-holder"
	const otherID = "status-other"
	for _, session := range []struct {
		id  string
		pid int
	}{
		{id: holderID, pid: holderPID},
		{id: otherID, pid: otherPID},
	} {
		if err := s.RegisterAgentSession(ctx, session.id, session.pid); err != nil {
			t.Fatalf("RegisterAgentSession(%s): %v", session.id, err)
		}
	}
	if err := s.AssociateAgentSessionWithProject(ctx, holderID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject(%s): %v", holderID, err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, holderID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	return taskStatusClaimFixture{
		store:     s,
		ctx:       ctx,
		taskID:    tasks[0].ID,
		projectID: project.ID,
		holderID:  holderID,
		otherID:   otherID,
	}
}

func TestUpdateTaskRejectsDoneForOtherClaimHolder(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	_, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.otherID)
	if err == nil {
		t.Fatal("UpdateTask(done) succeeded for a non-holder")
	}
	for _, want := range []string{"work lock held by another agent session", "only the lock holder", "todo"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("UpdateTask(done) error %q does not contain %q", err, want)
		}
	}
}

func TestUpdateTaskRejectsDroppedForOtherClaimHolder(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	_, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDropped, fixture.otherID)
	if err == nil {
		t.Fatal("UpdateTask(dropped) succeeded for a non-holder")
	}
	for _, want := range []string{"work lock held by another agent session", "only the lock holder", "todo"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("UpdateTask(dropped) error %q does not contain %q", err, want)
		}
	}
}

func TestUpdateTaskRejectsTodoForRunningOtherClaim(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	_, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskTodo, fixture.otherID)
	if err == nil {
		t.Fatal("UpdateTask(todo) succeeded while another claim was running")
	}
	for _, want := range []string{"still running", "todo"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("UpdateTask(todo) error %q does not contain %q", err, want)
		}
	}
}

func TestUpdateTaskAllowsTodoForStaleOtherClaim(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, 999999, os.Getpid())

	updated, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskTodo, fixture.otherID)
	if err != nil {
		t.Fatalf("UpdateTask(todo): %v", err)
	}
	handoff, err := fixture.store.openTaskHandoff(fixture.ctx, updated.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if updated.Status != domain.TaskTodo || handoff != nil {
		t.Fatalf("stale claim was not released with todo status: status=%s handoff=%v", updated.Status, handoff)
	}
}

func TestUpdateTaskAllowsDoneForClaimHolder(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	updated, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.holderID)
	if err != nil {
		t.Fatalf("UpdateTask(done): %v", err)
	}
	handoff, err := fixture.store.openTaskHandoff(fixture.ctx, updated.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if updated.Status != domain.TaskDone || handoff != nil {
		t.Fatalf("holder completion did not release its claim: status=%s handoff=%v", updated.Status, handoff)
	}
}

func TestUpdateTaskAllowsStatusChangeForUnclaimedTask(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct-unclaimed-status", filepath.Join(t.TempDir(), "atct-unclaimed-status"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "unclaimed status guard", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "unclaimed-status", []string{"Update an unclaimed task"}, []string{"Allow a session without a claim to update an unclaimed task."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if err := s.RegisterAgentSession(ctx, "unclaimed-other", os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, "unclaimed-other", project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}

	updated, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, "unclaimed-other")
	if err != nil {
		t.Fatalf("UpdateTask(done): %v", err)
	}
	handoff, err := s.openTaskHandoff(ctx, updated.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if updated.Status != domain.TaskDone || handoff != nil {
		t.Fatalf("unclaimed task was not updated: status=%s handoff=%v", updated.Status, handoff)
	}
}

func newTaskCommitTestTask(t *testing.T, s *Store, key string) string {
	t.Helper()
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(context.Background(), goalID, "agent", key, []string{"Task"}, []string{"Task description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	return tasks[0].ID
}

func TestLinkTaskCommitDoesNotDuplicateSameSHA(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	taskID := newTaskCommitTestTask(t, s, "same-sha")

	first := domain.TaskCommit{
		SHA:          "abc123",
		Subject:      "first subject",
		FilesChanged: 1,
		Insertions:   2,
		Deletions:    3,
		CreatedAt:    time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC),
	}
	second := first
	second.Subject = "replacement subject"
	second.FilesChanged = 4
	second.CreatedAt = first.CreatedAt.Add(time.Minute)
	if err := s.LinkTaskCommit(ctx, taskID, first); err != nil {
		t.Fatalf("LinkTaskCommit first: %v", err)
	}
	if err := s.LinkTaskCommit(ctx, taskID, second); err != nil {
		t.Fatalf("LinkTaskCommit second: %v", err)
	}

	commits, err := s.ListTaskCommits(ctx, taskID)
	if err != nil {
		t.Fatalf("ListTaskCommits: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("commit count = %d, want 1", len(commits))
	}
	if commits[0] != second {
		t.Fatalf("stored commit = %+v, want %+v", commits[0], second)
	}
}

func TestListTaskCommitsDoesNotMixTasks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	tasks, err := s.DeclareTasks(ctx, goalID, "agent", "different-tasks", []string{"First", "Second"}, []string{"First description", "Second description"})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	first := domain.TaskCommit{SHA: "first-sha", Subject: "first", CreatedAt: time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)}
	second := domain.TaskCommit{SHA: "second-sha", Subject: "second", CreatedAt: time.Date(2026, 8, 21, 2, 0, 0, 0, time.UTC)}
	if err := s.LinkTaskCommit(ctx, tasks[0].ID, first); err != nil {
		t.Fatalf("LinkTaskCommit first: %v", err)
	}
	if err := s.LinkTaskCommit(ctx, tasks[1].ID, second); err != nil {
		t.Fatalf("LinkTaskCommit second: %v", err)
	}

	firstCommits, err := s.ListTaskCommits(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("ListTaskCommits first: %v", err)
	}
	secondCommits, err := s.ListTaskCommits(ctx, tasks[1].ID)
	if err != nil {
		t.Fatalf("ListTaskCommits second: %v", err)
	}
	if len(firstCommits) != 1 || firstCommits[0] != first {
		t.Fatalf("first task commits = %+v, want [%+v]", firstCommits, first)
	}
	if len(secondCommits) != 1 || secondCommits[0] != second {
		t.Fatalf("second task commits = %+v, want [%+v]", secondCommits, second)
	}
}

func TestLinkTaskCommitRejectsMissingTask(t *testing.T) {
	c := domain.TaskCommit{
		SHA:       "missing-task-sha",
		Subject:   "missing task",
		CreatedAt: time.Date(2026, 8, 21, 3, 0, 0, 0, time.UTC),
	}
	if err := newTestStore(t).LinkTaskCommit(context.Background(), "missing-task", c); err == nil {
		t.Fatal("LinkTaskCommit succeeded for a missing task")
	}
}
