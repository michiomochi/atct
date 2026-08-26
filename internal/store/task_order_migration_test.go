package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type taskOrderMigrationTask struct {
	id          string
	title       string
	description string
	sortOrder   int
}

type taskOrderMigrationBatch struct {
	createdAt string
	tasks     []taskOrderMigrationTask
}

type taskOrderMigrationGoal struct {
	id      string
	title   string
	batches []taskOrderMigrationBatch
}

func TestUniqueTaskSortOrderMigrationRenumbersExistingTasks(t *testing.T) {
	db := openMigrationTestDB(t)
	migrations, err := loadEmbeddedMigrations()
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	if len(migrations) < 3 {
		t.Fatalf("embedded migrations = %d, want at least 3", len(migrations))
	}
	for _, migration := range migrations[:2] {
		if _, err := db.Exec(migration.sql); err != nil {
			t.Fatalf("execute %s: %v", migration.filename, err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 6`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	for _, migration := range migrations[:2] {
		if _, err := db.Exec(
			`INSERT INTO schema_migrations(filename, applied_at) VALUES (?, ?)`,
			migration.filename,
			"2026-08-20T00:00:00Z",
		); err != nil {
			t.Fatalf("record %s: %v", migration.filename, err)
		}
	}

	fixture := []taskOrderMigrationGoal{
		{
			id:    "goal-order-migration-a",
			title: "Preserve the first goal's task order",
			batches: []taskOrderMigrationBatch{
				{
					createdAt: "2026-08-17T14:04:40Z",
					tasks: []taskOrderMigrationTask{
						{id: "a-b1-second", title: "Gather the ordering evidence", description: "Collect the legacy task ordering evidence before migration.", sortOrder: 1},
						{id: "a-b1-first", title: "Record the legacy task state", description: "Record the first goal's legacy task state for comparison.", sortOrder: 0},
					},
				},
				{
					createdAt: "2026-08-17T14:04:41Z",
					tasks: []taskOrderMigrationTask{
						{id: "a-b2-third", title: "Check the migration result", description: "Check the migrated values after the second declaration batch.", sortOrder: 2},
						{id: "a-b2-first", title: "Prepare the unique index", description: "Prepare the rows that will receive the unique sort-order index.", sortOrder: 0},
						{id: "a-b2-second", title: "Run the repair query", description: "Run the repair query against the second legacy batch.", sortOrder: 1},
					},
				},
				{
					createdAt: "2026-08-17T14:04:42Z",
					tasks: []taskOrderMigrationTask{
						{id: "a-b3-second", title: "Confirm the stable tie breaker", description: "Confirm that task IDs settle equal sort-order values deterministically.", sortOrder: 1},
						{id: "a-b3-first", title: "Compare both goal histories", description: "Compare the first goal's batches without mixing another goal's rows.", sortOrder: 0},
					},
				},
			},
		},
		{
			id:    "goal-order-migration-b",
			title: "Preserve the second goal's task order",
			batches: []taskOrderMigrationBatch{
				{
					createdAt: "2026-08-17T14:04:50Z",
					tasks: []taskOrderMigrationTask{
						{id: "b-b1-third", title: "Inspect the second goal", description: "Inspect the second goal's legacy ordering before repair.", sortOrder: 2},
						{id: "b-b1-first", title: "List the second goal's batches", description: "List every declaration batch belonging to the second goal.", sortOrder: 0},
						{id: "b-b1-second", title: "Mark the duplicate positions", description: "Mark duplicate positions that must be renumbered per goal.", sortOrder: 1},
					},
				},
				{
					createdAt: "2026-08-17T14:04:51Z",
					tasks: []taskOrderMigrationTask{
						{id: "b-b2-first", title: "Apply the second batch repair", description: "Apply the repair ordering to the second goal's next batch.", sortOrder: 0},
					},
				},
				{
					createdAt: "2026-08-17T14:04:52Z",
					tasks: []taskOrderMigrationTask{
						{id: "b-b3-second", title: "Validate the final position", description: "Validate the final task position after all legacy batches are merged.", sortOrder: 1},
						{id: "b-b3-first", title: "Keep the goals separate", description: "Keep the second goal's repaired sequence separate from the first goal.", sortOrder: 0},
					},
				},
			},
		},
	}

	if _, err := db.Exec(`
INSERT INTO projects (id, name, root_path, created_at)
VALUES ('project-order-migration', 'order migration fixture', '/tmp/order-migration-fixture', '2026-08-17T14:04:00Z')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	beforeTitles := make(map[string][]string, len(fixture))
	beforeCount := 0
	for _, goal := range fixture {
		if _, err := db.Exec(`
INSERT INTO goals (id, project_id, title, status, created_at, updated_at)
VALUES (?, 'project-order-migration', ?, 'active', ?, ?)`,
			goal.id, goal.title, "2026-08-17T14:03:00Z", "2026-08-17T14:03:00Z"); err != nil {
			t.Fatalf("insert goal %s: %v", goal.id, err)
		}
		for _, batch := range goal.batches {
			for _, task := range batch.tasks {
				if _, err := db.Exec(`
INSERT INTO tasks (
  id, goal_id, title, description, status, agent, files, sort_order, declare_key,
  claimed_by, claimed_at, created_at, updated_at
)
VALUES (?, ?, ?, ?, 'todo', 'legacy-agent', '[]', ?, ?, '', NULL, ?, ?)`,
					task.id,
					goal.id,
					task.title,
					task.description,
					task.sortOrder,
					task.id,
					batch.createdAt,
					batch.createdAt,
				); err != nil {
					t.Fatalf("insert task %s: %v", task.id, err)
				}
				beforeCount++
			}
		}
		rows, err := db.Query(`SELECT id, title FROM tasks WHERE goal_id = ? ORDER BY created_at, sort_order, id`, goal.id)
		if err != nil {
			t.Fatalf("query pre-migration tasks for %s: %v", goal.id, err)
		}
		for rows.Next() {
			var id, title string
			if err := rows.Scan(&id, &title); err != nil {
				rows.Close()
				t.Fatalf("scan pre-migration task for %s: %v", goal.id, err)
			}
			beforeTitles[goal.id] = append(beforeTitles[goal.id], title)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("read pre-migration tasks for %s: %v", goal.id, err)
		}
		rows.Close()
	}

	var gotBeforeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&gotBeforeCount); err != nil {
		t.Fatalf("count pre-migration tasks: %v", err)
	}
	if gotBeforeCount != beforeCount {
		t.Fatalf("pre-migration task count = %d, want %d", gotBeforeCount, beforeCount)
	}
	if err := applyEmbeddedMigrations(db); err != nil {
		t.Fatalf("apply unique sort-order migration: %v", err)
	}
	assertMigrationRecorded(t, db, "0003_unique_task_sort_order.sql")

	var gotAfterCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&gotAfterCount); err != nil {
		t.Fatalf("count post-migration tasks: %v", err)
	}
	if gotAfterCount != beforeCount {
		t.Fatalf("post-migration task count = %d, want %d", gotAfterCount, beforeCount)
	}
	for _, goal := range fixture {
		var goalID int64
		if err := db.QueryRow(`SELECT id FROM goals WHERE content = ?`, goal.title).Scan(&goalID); err != nil {
			t.Fatalf("find migrated goal %s: %v", goal.id, err)
		}
		rows, err := db.Query(`
SELECT t.title, t.sort_order
FROM tasks AS t
WHERE t.goal_id = ?
ORDER BY t.sort_order, t.id`, goalID)
		if err != nil {
			t.Fatalf("query post-migration tasks for %s: %v", goal.id, err)
		}
		var gotTitles []string
		for expectedOrder := 0; rows.Next(); expectedOrder++ {
			var title string
			var sortOrder int
			if err := rows.Scan(&title, &sortOrder); err != nil {
				rows.Close()
				t.Fatalf("scan post-migration task for %s: %v", goal.id, err)
			}
			if sortOrder != expectedOrder {
				rows.Close()
				t.Fatalf("task %q sort_order = %d, want %d", title, sortOrder, expectedOrder)
			}
			gotTitles = append(gotTitles, title)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("read post-migration tasks for %s: %v", goal.id, err)
		}
		rows.Close()
		if !reflect.DeepEqual(gotTitles, beforeTitles[goal.id]) {
			t.Fatalf("post-migration order for %s = %v, want %v", goal.id, gotTitles, beforeTitles[goal.id])
		}
	}
}

func TestUniqueTaskSortOrderRejectsDuplicateWithinGoal(t *testing.T) {
	s := newTestStore(t)
	goalID, _ := newOrderTestGoals(t, s)
	if err := insertSortOrderConstraintTask(s, goalID, "duplicate-sort-first", 0); err != nil {
		t.Fatalf("insert first task: %v", err)
	}
	if err := insertSortOrderConstraintTask(s, goalID, "duplicate-sort-second", 0); err == nil {
		t.Fatal("inserted duplicate sort_order within one goal")
	}
}

func TestUniqueTaskSortOrderAllowsSamePositionInAnotherGoal(t *testing.T) {
	s := newTestStore(t)
	firstGoalID, secondGoalID := newOrderTestGoals(t, s)
	if err := insertSortOrderConstraintTask(s, firstGoalID, "first-goal-sort-zero", 0); err != nil {
		t.Fatalf("insert first goal task: %v", err)
	}
	if err := insertSortOrderConstraintTask(s, secondGoalID, "second-goal-sort-zero", 0); err != nil {
		t.Fatalf("insert second goal task: %v", err)
	}
}

func TestDeclareTasksSerializesConcurrentSortAllocation(t *testing.T) {
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	if got := s.DB().Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1 for serialized declarations", got)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := s.DeclareTasks(
			context.Background(),
			goalID,
			"agent-one",
			"concurrent-batch-one",
			[]string{"Reserve the first order range", "Persist the first declaration", "Verify the first range"},
			[]string{
				"Reserve the first three task positions for the concurrent declaration.",
				"Persist the first declaration batch with its meaningful task descriptions.",
				"Verify that the first declaration received a contiguous order range.",
			},
		)
		errs <- err
	}()
	go func() {
		<-start
		_, err := s.DeclareTasks(
			context.Background(),
			goalID,
			"agent-two",
			"concurrent-batch-two",
			[]string{"Reserve the second order range", "Verify the second range"},
			[]string{
				"Reserve the next two task positions after the other declaration commits.",
				"Verify that concurrent declarations do not reuse an existing position.",
			},
		)
		errs <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent DeclareTasks %d: %v", i, err)
		}
	}

	tasks, err := s.ListTasks(context.Background(), goalID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("task count = %d, want 5", len(tasks))
	}
	for i, task := range tasks {
		if task.Order != i {
			t.Fatalf("task %d order = %d, want %d", i, task.Order, i)
		}
	}
}

func insertSortOrderConstraintTask(s *Store, goalID int64, id string, sortOrder int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB().ExecContext(context.Background(), `
	INSERT INTO tasks (
	  goal_id, title, description, status, agent, files, sort_order, declare_key,
	  created_at, updated_at
	)
	VALUES (?, ?, ?, 'todo', 'constraint-test', '[]', ?, ?, ?, ?)`,
		goalID,
		"Persist a task for the sort-order constraint",
		"Confirm the unique sort-order constraint for this fixture task.",
		sortOrder,
		id,
		now,
		now,
	)
	return err
}
