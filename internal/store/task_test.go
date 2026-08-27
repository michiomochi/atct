package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func newTestGoal(t *testing.T, s *Store) int64 {
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

func newOrderTestGoals(t *testing.T, s *Store) (int64, int64) {
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

func declareOrderTestBatches(t *testing.T, s *Store, goalID int64) {
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
	insertTask := func(id int64, title, declareKey string, sortOrder int, createdAt string) {
		t.Helper()
		_, err := s.DB().ExecContext(ctx, `
INSERT INTO tasks (
  id, goal_id, title, description, status, agent, files, sort_order, declare_key,
  created_at, updated_at
)
VALUES (?, ?, ?, ?, 'todo', '', '[]', ?, ?, ?, ?)`,
			id, goalID, title, "Verify the stable sort-order ordering for this fixture.", sortOrder, declareKey, createdAt, createdAt)
		if err != nil {
			t.Fatalf("insert task %d: %v", id, err)
		}
	}
	insertTask(3, "sort three", "sort-three", 3, "2026-08-20T00:00:01Z")
	insertTask(1, "sort zero", "sort-zero", 0, "2026-08-20T00:00:04Z")
	insertTask(2, "sort two", "sort-two", 2, "2026-08-20T00:00:03Z")
	insertTask(4, "sort one", "sort-one", 1, "2026-08-20T00:00:02Z")

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	got := make([]int64, len(tasks))
	for i, task := range tasks {
		got[i] = task.ID
	}
	want := []int64{1, 4, 2, 3}
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
		t.Fatalf("task id changed on re-declare: %d -> %d", first[0].ID, all[0].ID)
	}
}

func TestDeclareTasksReportsWhichTasksWereCreated(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	declare := func(titles []string) []domain.Task {
		t.Helper()
		descriptions := make([]string, len(titles))
		for i, title := range titles {
			descriptions[i] = "Complete the task titled " + title + "."
		}
		tasks, err := s.DeclareTasks(ctx, goalID, "codex", "declared-status", titles, descriptions)
		if err != nil {
			t.Fatalf("DeclareTasks(%v): %v", titles, err)
		}
		return tasks
	}

	first := declare([]string{"Design", "Implement"})
	if len(first) != 2 {
		t.Fatalf("first returned %d tasks, want 2", len(first))
	}
	for _, task := range first {
		if task.Declared == nil || !*task.Declared {
			t.Fatalf("first declaration task %+v has Declared = %v, want true", task, task.Declared)
		}
	}

	second := declare([]string{"Design", "Implement"})
	if len(second) != 2 {
		t.Fatalf("second returned %d tasks, want 2", len(second))
	}
	for _, task := range second {
		if task.Declared == nil || *task.Declared {
			t.Fatalf("redeclared task %+v has Declared = %v, want false", task, task.Declared)
		}
	}

	third := declare([]string{"Design", "Implement", "Test"})
	if len(third) != 3 {
		t.Fatalf("partial redeclaration returned %d tasks, want 3", len(third))
	}
	for _, task := range third {
		want := task.DeclareKey == "declared-status#2"
		if task.Declared == nil || *task.Declared != want {
			t.Fatalf("partial redeclaration task %+v has Declared = %v, want %t", task, task.Declared, want)
		}
	}

	fourth := declare([]string{"Design", "Implement", "Test", "Document"})
	if len(fourth) != 4 {
		t.Fatalf("third redeclaration returned %d tasks, want 4", len(fourth))
	}
	for _, task := range fourth {
		want := task.DeclareKey == "declared-status#3"
		if task.Declared == nil || *task.Declared != want {
			t.Fatalf("third redeclaration task %+v has Declared = %v, want %t", task, task.Declared, want)
		}
	}
}

func TestDeclareTasksLeavesOtherDeclarationsWithoutDeclaredStatus(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	if _, err := s.DeclareTasks(ctx, goalID, "codex", "other-declaration", []string{"Other"}, []string{"Complete the other task."}); err != nil {
		t.Fatalf("other DeclareTasks: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goalID, "codex", "target-declaration", []string{"Target"}, []string{"Complete the target task."})
	if err != nil {
		t.Fatalf("target DeclareTasks: %v", err)
	}

	var other, target *domain.Task
	for i := range tasks {
		task := &tasks[i]
		switch task.DeclareKey {
		case "other-declaration#0":
			other = task
		case "target-declaration#0":
			target = task
		}
	}
	if other == nil || target == nil {
		t.Fatalf("DeclareTasks returned tasks = %+v, want both declarations", tasks)
	}
	if other.Declared != nil {
		t.Fatalf("other declaration has Declared = %v, want nil", other.Declared)
	}
	if target.Declared == nil || !*target.Declared {
		t.Fatalf("target declaration has Declared = %v, want true", target.Declared)
	}
}

func TestDeclareTasksListTasksJSONOmitsDeclared(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	if _, err := s.DeclareTasks(ctx, goalID, "codex", "json-declared", []string{"Task"}, []string{"Complete the task."}); err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}

	tasks, err := s.ListTasks(ctx, goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	payload, err := json.Marshal(tasks)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), `"declared"`) {
		t.Fatalf("ListTasks JSON = %s, must omit declared", payload)
	}
}

func declareOneTaskWithFiles(t *testing.T, s *Store, goalID int64, key, title string, files []string) int64 {
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
	return 0
}

func setTaskContentStatus(t *testing.T, s *Store, taskID int64, status domain.TaskStatus) {
	t.Helper()
	if _, err := s.DB().ExecContext(context.Background(), "UPDATE tasks SET status = ? WHERE id = ?", string(status), taskID); err != nil {
		t.Fatalf("set task %d status to %s: %v", taskID, status, err)
	}
}

func TestUpdateTaskContentAllowsTodo(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "content-todo-1", "first todo", nil)
	secondID := declareOneTaskWithFiles(t, s, goalID, "content-todo-2", "second todo", nil)

	wants := map[int64]string{
		firstID:  "updated first todo description",
		secondID: "updated second todo description",
	}
	for taskID, want := range wants {
		description := want
		updated, err := s.UpdateTaskContent(context.Background(), taskID, nil, &description, nil, 0)
		if err != nil {
			t.Fatalf("UpdateTaskContent(%d): %v", taskID, err)
		}
		if updated.Description != want || updated.Status != domain.TaskTodo {
			t.Fatalf("updated task = %+v, want description %q and status %q", updated, want, domain.TaskTodo)
		}
	}
}

func TestUpdateTaskContentAllowsDoing(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "content-doing-1", "first doing", nil)
	secondID := declareOneTaskWithFiles(t, s, goalID, "content-doing-2", "second doing", nil)
	setTaskContentStatus(t, s, firstID, domain.TaskStatus("doing"))
	setTaskContentStatus(t, s, secondID, domain.TaskStatus("doing"))

	wants := map[int64]string{
		firstID:  "updated first doing description",
		secondID: "updated second doing description",
	}
	for taskID, want := range wants {
		description := want
		updated, err := s.UpdateTaskContent(context.Background(), taskID, nil, &description, nil, 0)
		if err != nil {
			t.Fatalf("UpdateTaskContent(%d): %v", taskID, err)
		}
		if updated.Description != want || string(updated.Status) != "doing" {
			t.Fatalf("updated task = %+v, want description %q and status doing", updated, want)
		}
	}
}

func TestUpdateTaskContentUpdatesTitleOnly(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	const originalTitle = "original title"
	originalFiles := []string{"old.go"}
	taskID := declareOneTaskWithFiles(t, s, goalID, "content-title-only", originalTitle, originalFiles)
	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	original := tasks[0]
	newTitle := "updated title"

	updated, err := s.UpdateTaskContent(context.Background(), taskID, &newTitle, nil, nil, 0)
	if err != nil {
		t.Fatalf("UpdateTaskContent: %v", err)
	}
	if updated.Title != newTitle || updated.Description != original.Description || !reflect.DeepEqual(updated.Files, original.Files) {
		t.Fatalf("updated task = %+v, want only title changed from %+v", updated, original)
	}
}

func TestUpdateTaskContentUpdatesFilesOnly(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	originalFiles := []string{"old.go"}
	taskID := declareOneTaskWithFiles(t, s, goalID, "content-files-only", "original title", originalFiles)
	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	original := tasks[0]
	newFiles := []string{"new.go", "new_test.go"}

	updated, err := s.UpdateTaskContent(context.Background(), taskID, nil, nil, &newFiles, 0)
	if err != nil {
		t.Fatalf("UpdateTaskContent: %v", err)
	}
	if !reflect.DeepEqual(updated.Files, newFiles) || updated.Title != original.Title || updated.Description != original.Description {
		t.Fatalf("updated task = %+v, want only files changed from %+v", updated, original)
	}
}

func TestUpdateTaskContentPreservesOmittedFields(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	originalFiles := []string{"old.go"}
	taskID := declareOneTaskWithFiles(t, s, goalID, "content-partial", "original title", originalFiles)
	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	original := tasks[0]
	newDescription := "updated description only"

	updated, err := s.UpdateTaskContent(context.Background(), taskID, nil, &newDescription, nil, 0)
	if err != nil {
		t.Fatalf("UpdateTaskContent: %v", err)
	}
	if updated.Description != newDescription || updated.Title != original.Title || !reflect.DeepEqual(updated.Files, original.Files) {
		t.Fatalf("updated task = %+v, want description changed and omitted fields preserved from %+v", updated, original)
	}
}

func TestUpdateTaskContentRejectsDone(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "content-done-1", "first done", nil)
	secondID := declareOneTaskWithFiles(t, s, goalID, "content-done-2", "second done", nil)
	setTaskContentStatus(t, s, firstID, domain.TaskDone)
	setTaskContentStatus(t, s, secondID, domain.TaskDone)

	for _, taskID := range []int64{firstID, secondID} {
		title := "must not update"
		if _, err := s.UpdateTaskContent(context.Background(), taskID, &title, nil, nil, 0); !errors.Is(err, ErrTaskNotEditable) {
			t.Fatalf("UpdateTaskContent(%d) error = %v, want ErrTaskNotEditable", taskID, err)
		}
	}
}

func TestUpdateTaskContentRejectsDropped(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "content-dropped-1", "first dropped", nil)
	secondID := declareOneTaskWithFiles(t, s, goalID, "content-dropped-2", "second dropped", nil)
	setTaskContentStatus(t, s, firstID, domain.TaskDropped)
	setTaskContentStatus(t, s, secondID, domain.TaskDropped)

	for _, taskID := range []int64{firstID, secondID} {
		title := "must not update"
		if _, err := s.UpdateTaskContent(context.Background(), taskID, &title, nil, nil, 0); !errors.Is(err, ErrTaskNotEditable) {
			t.Fatalf("UpdateTaskContent(%d) error = %v, want ErrTaskNotEditable", taskID, err)
		}
	}
}

func TestUpdateTaskContentRejectsDoneAndDroppedFilesOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status domain.TaskStatus
	}{
		{name: "done-one", status: domain.TaskDone},
		{name: "done-two", status: domain.TaskDone},
		{name: "dropped-one", status: domain.TaskDropped},
		{name: "dropped-two", status: domain.TaskDropped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			goalID := newTestGoal(t, s)
			declaredFiles := []string{"declared-" + tc.name + ".go"}
			taskID := declareOneTaskWithFiles(t, s, goalID, "content-files-"+tc.name, "terminal files", declaredFiles)
			holderID := setupTaskContentTaskHandoff(t, s, taskID, "content-files-"+tc.name)
			setTaskContentStatus(t, s, taskID, tc.status)

			replacementFiles := []string{"must-not-update-" + tc.name + ".go"}
			_, err := s.UpdateTaskContent(ctx, taskID, nil, nil, &replacementFiles, holderID)
			if !errors.Is(err, ErrTaskNotEditable) {
				t.Fatalf("UpdateTaskContent error = %v, want ErrTaskNotEditable", err)
			}
			if !strings.Contains(err.Error(), string(tc.status)) {
				t.Fatalf("UpdateTaskContent error = %q, want status %q", err, tc.status)
			}

			var storedFiles string
			if err := s.DB().QueryRowContext(ctx, "SELECT files FROM tasks WHERE id = ?", taskID).Scan(&storedFiles); err != nil {
				t.Fatalf("select task files: %v", err)
			}
			wantFiles, err := json.Marshal(declaredFiles)
			if err != nil {
				t.Fatalf("marshal declared files: %v", err)
			}
			if storedFiles != string(wantFiles) {
				t.Fatalf("stored files = %q, want declared files %q", storedFiles, wantFiles)
			}
		})
	}
}

func TestUpdateTaskContentErrorIncludesStatus(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := declareOneTaskWithFiles(t, s, goalID, "content-error-status", "done task", nil)
	setTaskContentStatus(t, s, taskID, domain.TaskDone)
	title := "must not update"

	_, err := s.UpdateTaskContent(context.Background(), taskID, &title, nil, nil, 0)
	if err == nil {
		t.Fatal("UpdateTaskContent unexpectedly succeeded for done task")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", taskID)) || !strings.Contains(err.Error(), "done") {
		t.Fatalf("UpdateTaskContent error = %q, want task ID and status done", err)
	}
}

func TestUpdateTaskContentReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	missingID := int64(0)
	description := "updated description"

	if _, err := s.UpdateTaskContent(context.Background(), missingID, nil, &description, nil, 0); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("UpdateTaskContent error = %v, want ErrTaskNotFound", err)
	}
}

func TestUpdateTaskContentReturnsNotFoundForMissingNonZeroID(t *testing.T) {
	s := newTestStore(t)
	const missingID int64 = 999999
	description := "updated description"

	_, err := s.UpdateTaskContent(context.Background(), missingID, nil, &description, nil, 0)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("UpdateTaskContent error = %v, want ErrTaskNotFound", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(missingID)) {
		t.Fatalf("UpdateTaskContent error does not include task ID %d", missingID)
	}
}

func TestUpdateTaskContentRejectsEmptyUpdate(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := declareOneTaskWithFiles(t, s, goalID, "content-empty", "original title", []string{"old.go"})
	before, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks before: %v", err)
	}

	if _, err := s.UpdateTaskContent(context.Background(), taskID, nil, nil, nil, 0); err == nil {
		t.Fatal("UpdateTaskContent unexpectedly succeeded without fields")
	}
	after, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks after: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("task changed after empty update: before=%+v after=%+v", before, after)
	}
}

func setupTaskContentTaskHandoff(t *testing.T, s *Store, taskID int64, label string) int64 {
	t.Helper()
	ctx := context.Background()
	requesterLabel := label + "-goal-requester"
	holderLabel := label + "-task-holder"
	addLiveParentGoalClaim(t, s, taskID, requesterLabel)
	holderID := registerNamedTestAgentSession(t, s, holderLabel, os.Getpid())

	handoff, err := s.RequestTaskHandoff(ctx, label+"-task-handoff", taskID, testSessionID(requesterLabel), "")
	if err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	if _, err := s.ReceiveTaskHandoff(ctx, handoff.ID, taskID, holderID); err != nil {
		t.Fatalf("ReceiveTaskHandoff: %v", err)
	}
	return holderID
}

func setupTaskContentGoalHandoff(t *testing.T, s *Store, goalID int64, label string) int64 {
	t.Helper()
	ctx := context.Background()
	requesterLabel := label + "-project-requester"
	holderLabel := label + "-goal-holder"
	addLiveProjectClaim(t, s, goalID, requesterLabel)
	holderID := registerNamedTestAgentSession(t, s, holderLabel, os.Getpid())

	handoff, err := s.RequestGoalHandoff(ctx, label+"-goal-handoff", goalID, testSessionID(requesterLabel), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, holderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	return holderID
}

func setupTaskContentGoalHolderWithTaskHandoff(t *testing.T, s *Store, goalID, taskID int64, label string) int64 {
	t.Helper()
	setupTaskContentTaskHandoff(t, s, taskID, label)
	if _, err := s.CompleteGoalHandoffForGoal(context.Background(), goalID, "closing goal handoff to recreate the fixture"); err != nil {
		t.Fatalf("CompleteGoalHandoffForGoal: %v", err)
	}
	return setupTaskContentGoalHandoff(t, s, goalID, label)
}

func setupTaskContentLiveSession(t *testing.T, s *Store, _, _ int64, label string) int64 {
	t.Helper()
	return registerNamedTestAgentSession(t, s, label, os.Getpid())
}

func setupTaskContentOtherGoalHandoff(t *testing.T, s *Store, targetGoalID int64, label string) int64 {
	t.Helper()
	ctx := context.Background()
	targetGoal, err := s.GetGoal(ctx, targetGoalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, targetGoal.ProjectID, label+" other goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal other: %v", err)
	}
	requesterLabel := label + "-project-requester"
	targetHolderLabel := label + "-target-goal-holder"
	holderLabel := label + "-other-goal-holder"
	addLiveProjectClaim(t, s, otherGoal.ID, requesterLabel)
	targetHolderID := registerNamedTestAgentSession(t, s, targetHolderLabel, os.Getpid())
	holderID := registerNamedTestAgentSession(t, s, holderLabel, os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, holderID, targetGoal.ProjectID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}

	targetHandoff, err := s.RequestGoalHandoff(ctx, label+"-target-goal-handoff", targetGoalID, testSessionID(requesterLabel), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff target: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, targetHandoff.ID, targetGoalID, targetHolderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff target: %v", err)
	}
	handoff, err := s.RequestGoalHandoff(ctx, label+"-other-goal-handoff", otherGoal.ID, testSessionID(requesterLabel), "")
	if err != nil {
		t.Fatalf("RequestGoalHandoff other: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, otherGoal.ID, holderID); err != nil {
		t.Fatalf("ReceiveGoalHandoff other: %v", err)
	}
	return holderID
}

func setupTaskContentProjectOnly(t *testing.T, s *Store, goalID, taskID int64, label string, taskHandoff bool) int64 {
	t.Helper()
	ctx := context.Background()
	if taskHandoff {
		setupTaskContentTaskHandoff(t, s, taskID, label)
	} else {
		setupTaskContentGoalHandoff(t, s, goalID, label)
	}
	callerID := registerNamedTestAgentSession(t, s, label+"-project-only", os.Getpid())
	if err := s.AssociateAgentSessionWithProject(ctx, callerID, mustTestGoalProjectID(t, s, goalID)); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	return callerID
}

func mustTestGoalProjectID(t *testing.T, s *Store, goalID int64) int64 {
	t.Helper()
	goal, err := s.GetGoal(context.Background(), goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	return goal.ProjectID
}

func TestUpdateTaskContentHandoffAuthorization(t *testing.T) {
	tests := []struct {
		name                     string
		setup                    func(t *testing.T, s *Store, goalID, taskID int64, label string) int64
		wantDenied               bool
		assertTaskHolderDistinct bool
		requireNonZeroSession    bool
	}{
		{name: "task-holder-one", setup: func(t *testing.T, s *Store, _, taskID int64, label string) int64 {
			return setupTaskContentTaskHandoff(t, s, taskID, label)
		}},
		{name: "task-holder-two", setup: func(t *testing.T, s *Store, _, taskID int64, label string) int64 {
			return setupTaskContentTaskHandoff(t, s, taskID, label)
		}},
		{name: "goal-holder-one", setup: func(t *testing.T, s *Store, goalID, _ int64, label string) int64 {
			return setupTaskContentGoalHandoff(t, s, goalID, label)
		}},
		{name: "goal-holder-two", setup: func(t *testing.T, s *Store, goalID, _ int64, label string) int64 {
			return setupTaskContentGoalHandoff(t, s, goalID, label)
		}},
		{name: "goal-holder-with-task-handoff-one", assertTaskHolderDistinct: true, setup: setupTaskContentGoalHolderWithTaskHandoff},
		{name: "goal-holder-with-task-handoff-two", assertTaskHolderDistinct: true, setup: setupTaskContentGoalHolderWithTaskHandoff},
		{name: "other-goal-holder-one", wantDenied: true, setup: func(t *testing.T, s *Store, goalID, _ int64, label string) int64 {
			return setupTaskContentOtherGoalHandoff(t, s, goalID, label)
		}},
		{name: "other-goal-holder-two", wantDenied: true, setup: func(t *testing.T, s *Store, goalID, _ int64, label string) int64 {
			return setupTaskContentOtherGoalHandoff(t, s, goalID, label)
		}},
		{name: "project-only-with-task-handoff-one", wantDenied: true, setup: func(t *testing.T, s *Store, goalID, taskID int64, label string) int64 {
			return setupTaskContentProjectOnly(t, s, goalID, taskID, label, true)
		}},
		{name: "project-only-with-goal-handoff-two", wantDenied: true, setup: func(t *testing.T, s *Store, goalID, taskID int64, label string) int64 {
			return setupTaskContentProjectOnly(t, s, goalID, taskID, label, false)
		}},
		{name: "no-handoff-live-session-one", requireNonZeroSession: true, setup: setupTaskContentLiveSession},
		{name: "no-handoff-live-session-two", requireNonZeroSession: true, setup: setupTaskContentLiveSession},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			goalID := newTestGoal(t, s)
			taskID := declareOneTaskWithFiles(t, s, goalID, "content-auth-"+tc.name, "content authorization", []string{"before.go"})
			callerID := tc.setup(t, s, goalID, taskID, "content-auth-"+tc.name)
			if tc.requireNonZeroSession && callerID == 0 {
				t.Fatal("live session has zero ID")
			}
			if tc.assertTaskHolderDistinct {
				taskHandoff, handoffErr := s.openTaskHandoff(ctx, taskID)
				if handoffErr != nil {
					t.Fatalf("openTaskHandoff: %v", handoffErr)
				}
				if taskHandoff == nil {
					t.Fatal("task handoff is missing")
				}
				if taskHandoff.ReceivedBy == callerID {
					t.Fatalf("task handoff holder = caller %d, want a different session", callerID)
				}
			}
			before, err := s.ListTasks(ctx, goalID)
			if err != nil {
				t.Fatalf("ListTasks before: %v", err)
			}

			newFiles := []string{"after-" + tc.name + ".go"}
			updated, err := s.UpdateTaskContent(ctx, taskID, nil, nil, &newFiles, callerID)
			if tc.wantDenied {
				if !errors.Is(err, ErrTaskContentNotOwned) {
					t.Fatalf("UpdateTaskContent error = %v, want ErrTaskContentNotOwned", err)
				}
				if !strings.Contains(err.Error(), fmt.Sprint(taskID)) {
					t.Fatalf("UpdateTaskContent error = %q, want task ID %d", err, taskID)
				}
				after, listErr := s.ListTasks(ctx, goalID)
				if listErr != nil {
					t.Fatalf("ListTasks after: %v", listErr)
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("task changed after rejected update: before=%+v after=%+v", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("UpdateTaskContent: %v", err)
			}
			if !reflect.DeepEqual(updated.Files, newFiles) {
				t.Fatalf("updated files = %#v, want %#v", updated.Files, newFiles)
			}
		})
	}
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
		t.Fatalf("ListTasks returned %+v, want task %d", tasks, taskID)
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

	if _, err := s.ClaimTask(context.Background(), firstID, testSessionID("run-1")); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, testSessionID("run-2")); !errors.Is(err, ErrTaskFileConflict) {
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

	if _, err := s.ClaimTask(context.Background(), firstID, testSessionID("run-1")); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, testSessionID("run-2")); err != nil {
		t.Fatalf("second ClaimTask: %v", err)
	}
}

func TestClaimTaskAllowsOverlappingFilesForSameAgentSession(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	firstID := declareOneTaskWithFiles(t, s, goalID, "same-run-1", "first", []string{"internal/store/task.go"})
	secondID := declareOneTaskWithFiles(t, s, goalID, "same-run-2", "second", []string{"internal/store/task.go"})
	addTestAgentSession(t, s, "run-1")

	if _, err := s.ClaimTask(context.Background(), firstID, testSessionID("run-1")); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, testSessionID("run-1")); err != nil {
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

	if _, err := s.ClaimTask(context.Background(), firstID, testSessionID("run-1")); err != nil {
		t.Fatalf("first ClaimTask: %v", err)
	}
	if _, err := s.ClaimTask(context.Background(), secondID, testSessionID("run-2")); err != nil {
		t.Fatalf("second ClaimTask: %v", err)
	}
}

func TestClaimTaskUsesSelfHandoffWithoutWritingClaimedBy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := declareOneTaskWithFiles(t, s, goalID, "self-handoff", "self handoff", nil)
	const ownerLabel = "task-handoff-owner"
	const nextOwnerLabel = "task-handoff-next-owner"
	ownerID := registerNamedTestAgentSession(t, s, ownerLabel, os.Getpid())
	nextOwnerID := registerNamedTestAgentSession(t, s, nextOwnerLabel, os.Getpid())

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
		t.Fatalf("task self handoff = %+v, want received and open for %q", handoffs[0], ownerLabel)
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
		t.Fatalf("reclaimed task self handoff = %+v, want open for %q", handoffs, nextOwnerLabel)
	}
}

func TestClaimGoalUsesSelfHandoffWithoutWritingClaimedBy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	const ownerLabel = "goal-handoff-owner"
	const nextOwnerLabel = "goal-handoff-next-owner"
	ownerID := registerNamedTestAgentSession(t, s, ownerLabel, os.Getpid())
	nextOwnerID := registerNamedTestAgentSession(t, s, nextOwnerLabel, os.Getpid())

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
		t.Fatalf("goal self handoff = %+v, want received and open for %q", handoffs[0], ownerLabel)
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
		t.Fatalf("reclaimed goal self handoff = %+v, want open for %q", handoffs, nextOwnerLabel)
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

			if _, err := s.ClaimTask(context.Background(), ownerID, testSessionID("run-owner")); err != nil {
				t.Fatalf("owner ClaimTask: %v", err)
			}
			if _, err := s.DB().Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC().Format(time.RFC3339Nano), ownerID); err != nil {
				t.Fatalf("mark owner %s: %v", status, err)
			}
			if _, err := s.ClaimTask(context.Background(), candidateID, testSessionID("run-candidate")); err != nil {
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

	if _, err := s.ClaimTask(context.Background(), ownerID, testSessionID("run-owner")); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), candidateID, testSessionID("run-candidate"))
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{fmt.Sprintf("%d", ownerID), "owner task", "internal/store/task.go"} {
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

	if _, err := s.ClaimTask(context.Background(), ownerID, testSessionID("run-owner")); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, testSessionID("run-target"))
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{fmt.Sprintf("%d", alternativeID), "safe alternative", "alternatives"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), fmt.Sprintf("%d", blockedID)) {
		t.Fatalf("ClaimTask error %q includes a conflicting alternative %d", err, blockedID)
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

	if _, err := s.ClaimTask(context.Background(), ownerID, testSessionID("run-owner")); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, testSessionID("run-target"))
	if !errors.Is(err, ErrTaskFileConflict) {
		t.Fatalf("ClaimTask error = %v, want ErrTaskFileConflict", err)
	}
	for _, want := range []string{fmt.Sprintf("%d", fileAlternativeID), "file alternative", fmt.Sprintf("%d", emptyAlternativeID), "empty alternative"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClaimTask error %q does not contain candidate %q", err, want)
		}
	}
	for _, taskID := range []int64{fileAlternativeID, emptyAlternativeID} {
		if _, err := s.ClaimTask(context.Background(), taskID, testSessionID("run-target")); err != nil {
			t.Fatalf("candidate %d ClaimTask: %v", taskID, err)
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

	if _, err := s.ClaimTask(context.Background(), ownerID, testSessionID("run-owner")); err != nil {
		t.Fatalf("owner ClaimTask: %v", err)
	}
	_, err := s.ClaimTask(context.Background(), targetID, testSessionID("run-target"))
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

	tasks, err := s.ListTasks(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListTasks after migration: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != 1 || tasks[0].Title != "old task" {
		t.Fatalf("migrated tasks = %+v, want original task", tasks)
	}
	if len(tasks[0].Files) != 0 {
		t.Fatalf("migrated task files = %#v, want empty", tasks[0].Files)
	}
	var storedFiles string
	if err := s.DB().QueryRow(`SELECT files FROM tasks WHERE id = 1`).Scan(&storedFiles); err != nil {
		t.Fatalf("read migrated files column: %v", err)
	}
	if storedFiles != "[]" {
		t.Fatalf("migrated files value = %q, want []", storedFiles)
	}
}

type taskStatusClaimFixture struct {
	store      *Store
	ctx        context.Context
	taskID     int64
	goalID     int64
	projectID  int64
	holderID   int64
	otherID    int64
	peerID     int64
	strangerID int64
}

func newTaskStatusClaimFixture(t *testing.T, holderPID, otherPID int) taskStatusClaimFixture {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct-status-claims", filepath.Join(t.TempDir(), "atct-status-claims"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	strangerProject, err := s.CreateProject(ctx, "atct-status-claims-stranger", filepath.Join(t.TempDir(), "atct-status-claims-stranger"))
	if err != nil {
		t.Fatalf("CreateProject stranger: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "status claim guard", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "status-claim-guard", []string{"Guard the status update"}, []string{"Ensure status updates respect the task claim owner and liveness."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	const holderLabel = "status-holder"
	const otherLabel = "status-other"
	const peerLabel = "status-peer"
	const strangerLabel = "status-stranger"
	holderID := testSessionID(holderLabel)
	otherID := testSessionID(otherLabel)
	peerID := testSessionID(peerLabel)
	strangerID := testSessionID(strangerLabel)
	for _, session := range []struct {
		label string
		id    int64
		pid   int
	}{
		{label: holderLabel, id: holderID, pid: holderPID},
		{label: otherLabel, id: otherID, pid: otherPID},
		{label: peerLabel, id: peerID, pid: os.Getpid()},
		{label: strangerLabel, id: strangerID, pid: os.Getpid()},
	} {
		registerNamedTestAgentSession(t, s, session.label, session.pid)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, holderID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject(%s): %v", holderLabel, err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, peerID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject(%s): %v", peerLabel, err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, strangerID, strangerProject.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject(%s): %v", strangerLabel, err)
	}
	if _, err := s.ClaimTask(ctx, tasks[0].ID, holderID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	return taskStatusClaimFixture{
		store:      s,
		ctx:        ctx,
		taskID:     tasks[0].ID,
		goalID:     goal.ID,
		projectID:  project.ID,
		holderID:   holderID,
		otherID:    otherID,
		peerID:     peerID,
		strangerID: strangerID,
	}
}

func askOpenTaskDecision(t *testing.T, fixture taskStatusClaimFixture, question string) domain.Decision {
	t.Helper()
	decision, err := fixture.store.AskDecision(fixture.ctx, AskInput{
		GoalID:         fixture.goalID,
		TaskID:         fixture.taskID,
		Kind:           domain.DecisionKind("test"),
		Question:       question,
		AgentSessionID: fixture.holderID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	return decision
}

func TestUpdateTaskAllowsDoneWithAppliedDefaultDecision(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())
	decision := askOpenTaskDecision(t, fixture, "record this status")

	if _, err := fixture.store.DB().ExecContext(fixture.ctx,
		"UPDATE decisions SET default_option = ?, default_after_ms = ? WHERE id = ?",
		"record", int64(0), decision.ID,
	); err != nil {
		t.Fatalf("set decision default: %v", err)
	}
	if applied, err := fixture.store.ApplyExpiredDefaults(fixture.ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("ApplyExpiredDefaults: %v", err)
	} else if applied != 1 {
		t.Fatalf("ApplyExpiredDefaults applied %d decisions, want 1", applied)
	}

	task, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.holderID)
	if err != nil {
		t.Fatalf("UpdateTask(done): %v", err)
	}
	if task.Status != domain.TaskDone {
		t.Fatalf("task status = %q, want %q", task.Status, domain.TaskDone)
	}
}

func TestUpdateTaskErrorIncludesAllOpenDecisionIDs(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())
	first := askOpenTaskDecision(t, fixture, "first question")
	second := askOpenTaskDecision(t, fixture, "second question")

	_, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.holderID)
	if err == nil {
		t.Fatal("UpdateTask(done) succeeded with open decisions")
	}
	for _, id := range []int64{first.ID, second.ID} {
		if !strings.Contains(err.Error(), fmt.Sprintf("%d", id)) {
			t.Fatalf("UpdateTask(done) error %q does not contain decision id %d", err, id)
		}
	}
}

func TestUpdateTaskErrorIncludesOpenDecisionQuestion(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())
	question := "Which release path should the worker use?"
	askOpenTaskDecision(t, fixture, question)

	_, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.holderID)
	if err == nil {
		t.Fatal("UpdateTask(done) succeeded with an open decision")
	}
	if !strings.Contains(err.Error(), question) {
		t.Fatalf("UpdateTask(done) error %q does not contain question %q", err, question)
	}
}

func TestUpdateTaskErrorOffersOpenDecisionExits(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())
	askOpenTaskDecision(t, fixture, "Should this be recorded or answered?")

	_, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.holderID)
	if err == nil {
		t.Fatal("UpdateTask(done) succeeded with an open decision")
	}
	for _, want := range []string{"wait for a human answer", "withdraw", "default_option", "default_after_ms=0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("UpdateTask(done) error %q does not contain %q", err, want)
		}
	}
}

func TestUpdateTaskStillRejectsOpenDecision(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())
	askOpenTaskDecision(t, fixture, "A human answer is required")

	_, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.holderID)
	if err == nil || !errors.Is(err, ErrTaskHasOpenDecision) {
		t.Fatalf("UpdateTask(done) error = %v, want ErrTaskHasOpenDecision", err)
	}
}

func TestUpdateTaskIgnoresOpenDecisionForOtherTask(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())
	otherTasks, err := fixture.store.DeclareTasks(fixture.ctx, fixture.goalID, "agent", "other-task", []string{"Other task"}, []string{"A task with an independent decision."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	var otherTaskID int64
	for _, task := range otherTasks {
		if task.ID != fixture.taskID {
			otherTaskID = task.ID
			break
		}
	}
	if otherTaskID == 0 {
		t.Fatalf("DeclareTasks returned no task other than %d", fixture.taskID)
	}
	if _, err := fixture.store.AskDecision(fixture.ctx, AskInput{
		GoalID:         fixture.goalID,
		TaskID:         otherTaskID,
		Kind:           domain.DecisionKind("test"),
		Question:       "Other task question",
		AgentSessionID: fixture.holderID,
	}); err != nil {
		t.Fatalf("AskDecision for other task: %v", err)
	}

	if _, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.holderID); err != nil {
		t.Fatalf("UpdateTask(done) was blocked by another task decision: %v", err)
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

func TestReleaseTaskAllowsHumanPathWithRunningClaim(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	released, err := fixture.store.ReleaseTaskForHuman(fixture.ctx, fixture.taskID)
	if err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	handoff, err := fixture.store.openTaskHandoff(fixture.ctx, released.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if released.Status != domain.TaskTodo || handoff != nil {
		t.Fatalf("human release did not clear running claim: status=%s handoff=%v", released.Status, handoff)
	}
}

func TestUpdateTaskAllowsTodoForClaimHolder(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	updated, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskTodo, fixture.holderID)
	if err != nil {
		t.Fatalf("UpdateTask(todo): %v", err)
	}
	handoff, err := fixture.store.openTaskHandoff(fixture.ctx, updated.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if updated.Status != domain.TaskTodo || handoff != nil {
		t.Fatalf("holder release did not clear claim: status=%s handoff=%v", updated.Status, handoff)
	}
}

func TestUpdateTaskAllowsTodoForProjectBoundPeer(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	updated, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskTodo, fixture.peerID)
	if err != nil {
		t.Fatalf("UpdateTask(todo): %v", err)
	}
	handoff, err := fixture.store.openTaskHandoff(fixture.ctx, updated.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if updated.Status != domain.TaskTodo || handoff != nil {
		t.Fatalf("project-bound peer release did not clear claim: status=%s handoff=%v", updated.Status, handoff)
	}
}

func TestUpdateTaskAllowsDoneForProjectBoundPeer(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	updated, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.peerID)
	if err != nil {
		t.Fatalf("UpdateTask(done): %v", err)
	}
	handoff, err := fixture.store.openTaskHandoff(fixture.ctx, updated.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if updated.Status != domain.TaskDone || handoff != nil {
		t.Fatalf("project-bound peer completion did not clear claim: status=%s handoff=%v", updated.Status, handoff)
	}
}

func TestUpdateTaskRejectsTodoForDifferentProjectCaller(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	if _, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskTodo, fixture.strangerID); err == nil {
		t.Fatal("UpdateTask(todo) succeeded for a caller bound to another project")
	}
}

func TestUpdateTaskRejectsDoneForDifferentProjectCaller(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	if _, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskDone, fixture.strangerID); err == nil {
		t.Fatal("UpdateTask(done) succeeded for a caller bound to another project")
	}
}

func TestUpdateTaskRejectsTodoForEmptyCaller(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	if _, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskTodo, 0); err == nil {
		t.Fatal("UpdateTask(todo) succeeded for an empty agent session")
	}
}

func TestUpdateTaskRejectsTodoForUnassociatedCaller(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	if _, err := fixture.store.UpdateTask(fixture.ctx, fixture.taskID, domain.TaskTodo, fixture.otherID); err == nil {
		t.Fatal("UpdateTask(todo) succeeded for an unassociated agent session")
	}
}

func TestReleaseTaskAsAllowsProjectBoundPeer(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	released, err := fixture.store.ReleaseTaskAs(fixture.ctx, fixture.taskID, fixture.peerID)
	if err != nil {
		t.Fatalf("ReleaseTaskAs: %v", err)
	}
	handoff, err := fixture.store.openTaskHandoff(fixture.ctx, released.ID)
	if err != nil {
		t.Fatalf("load task handoff: %v", err)
	}
	if released.Status != domain.TaskTodo || handoff != nil {
		t.Fatalf("project-bound peer release did not clear claim: status=%s handoff=%v", released.Status, handoff)
	}
}

func TestReleaseTaskAsRejectsDifferentProjectCaller(t *testing.T) {
	fixture := newTaskStatusClaimFixture(t, os.Getpid(), os.Getpid())

	if _, err := fixture.store.ReleaseTaskAs(fixture.ctx, fixture.taskID, fixture.strangerID); err == nil {
		t.Fatal("ReleaseTaskAs succeeded for a caller bound to another project")
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
	unclaimedID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, unclaimedID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}

	updated, err := s.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, unclaimedID)
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

func newTaskCommitTestTask(t *testing.T, s *Store, key string) int64 {
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
	if err := newTestStore(t).LinkTaskCommit(context.Background(), 0, c); err == nil {
		t.Fatal("LinkTaskCommit succeeded for a missing task")
	}
}
