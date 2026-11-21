package main

import (
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func TestProjectScopeAgainstRealDatabaseCopy(t *testing.T) {
	path := os.Getenv("ATCT_REAL_DB")
	if path == "" {
		t.Skip("set ATCT_REAL_DB to a copy of a real database")
	}
	date := os.Getenv("ATCT_REAL_DATE")
	if date == "" {
		t.Skip("set ATCT_REAL_DATE to the date to inspect")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	taskRows, err := db.Query(`
		select task_id, id from task_handoffs
		 where date(completed_report_at) = ?
		   and (requested_by is null or received_by is null or requested_by <> received_by)
	`, date)
	if err != nil {
		t.Fatalf("query task handoffs: %v", err)
	}
	defer taskRows.Close()

	taskDecisions := make([]watchDecision, 0)
	for taskRows.Next() {
		var taskID, handoffID string
		if err := taskRows.Scan(&taskID, &handoffID); err != nil {
			t.Fatalf("scan task handoff: %v", err)
		}
		taskDecisions = append(taskDecisions, watchDecision{
			GoalID:    "any",
			TaskID:    taskID,
			HandoffID: handoffID,
		})
	}
	if err := taskRows.Err(); err != nil {
		t.Fatalf("iterate task handoffs: %v", err)
	}

	goalRows, err := db.Query(`
		select goal_id, id from goal_handoffs
		 where date(completed_report_at) = ?
		   and (requested_by is null or received_by is null or requested_by <> received_by)
	`, date)
	if err != nil {
		t.Fatalf("query goal handoffs: %v", err)
	}
	defer goalRows.Close()

	goalDecisions := make([]watchDecision, 0)
	for goalRows.Next() {
		var goalID, handoffID string
		if err := goalRows.Scan(&goalID, &handoffID); err != nil {
			t.Fatalf("scan goal handoff: %v", err)
		}
		goalDecisions = append(goalDecisions, watchDecision{
			GoalID:    goalID,
			HandoffID: handoffID,
		})
	}
	if err := goalRows.Err(); err != nil {
		t.Fatalf("iterate goal handoffs: %v", err)
	}

	projectFilter := newWatchScopeFilter("")
	taskDelivered := 0
	goalDelivered := 0
	var taskLeaks []string
	for _, decision := range taskDecisions {
		if projectFilter.delivers("handoff_reported", decision) {
			taskDelivered++
			if len(taskLeaks) < 5 {
				taskLeaks = append(taskLeaks, decision.HandoffID)
			}
		}
	}
	for _, decision := range goalDecisions {
		if projectFilter.delivers("handoff_reported", decision) {
			goalDelivered++
		}
	}

	t.Logf("task rows=%d delivered=%d / goal rows=%d delivered=%d", len(taskDecisions), taskDelivered, len(goalDecisions), goalDelivered)
	if len(taskDecisions) == 0 {
		t.Fatalf("no task-derived handoff rows for %q", date)
	}
	if len(goalDecisions) == 0 {
		t.Fatalf("no goal-derived handoff rows for %q", date)
	}
	if taskDelivered != 0 {
		t.Errorf("project scope delivered task-derived handoff IDs: %v", taskLeaks)
	}
	if goalDelivered != len(goalDecisions) {
		t.Errorf("project scope delivered %d of %d goal-derived handoffs", goalDelivered, len(goalDecisions))
	}

	goalFilter := newWatchScopeFilter("any")
	taskGoalScopeDelivered := 0
	for _, decision := range taskDecisions {
		if goalFilter.delivers("handoff_reported", decision) {
			taskGoalScopeDelivered++
		}
	}
	if taskGoalScopeDelivered != len(taskDecisions) {
		t.Errorf("goal scope delivered %d of %d task-derived handoffs", taskGoalScopeDelivered, len(taskDecisions))
	}
}
