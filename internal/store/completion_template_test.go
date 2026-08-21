package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func completionTemplateStore(t *testing.T) (*Store, context.Context, domain.Goal) {
	t.Helper()
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "atct-completion-template-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	project, err := s.CreateProject(ctx, "completion-template", dir)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "completion template", "human")
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return s, ctx, goal
}

func completionTemplateReport() domain.CompletionReport {
	return domain.CompletionReport{
		WorkDone:    "work done",
		NowPossible: "now possible",
		HowToVerify: "how to verify",
		Surprises:   "なし",
		NeedsReview: "needs review",
		NextSteps:   "next steps",
	}
}

func completionTemplateReportValues(report domain.CompletionReport) []any {
	return []any{
		report.WorkDone,
		report.NowPossible,
		report.HowToVerify,
		report.Surprises,
		report.NeedsReview,
		report.NextSteps,
	}
}

type completionTemplateLegacyGoal struct {
	status        string
	resultSummary string
}

type completionTemplateMigratedGoal struct {
	status        string
	resultSummary string
	workDone      string
	nowPossible   string
	howToVerify   string
	surprises     string
	needsReview   string
	nextSteps     string
}

func completionTemplateMeasureRealCopy(t *testing.T, dbPath string) {
	t.Helper()
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open VACUUM INTO copy: %v", err)
	}
	rows, err := legacy.Query(`SELECT id, status, result_summary FROM goals ORDER BY id`)
	if err != nil {
		legacy.Close()
		t.Fatalf("read copied v5 reports: %v", err)
	}
	want := make(map[string]completionTemplateLegacyGoal)
	reported := 0
	doneReported := 0
	for rows.Next() {
		var id, status, summary string
		if err := rows.Scan(&id, &status, &summary); err != nil {
			rows.Close()
			legacy.Close()
			t.Fatalf("scan copied v5 report: %v", err)
		}
		want[id] = completionTemplateLegacyGoal{status: status, resultSummary: summary}
		if strings.TrimSpace(summary) != "" {
			reported++
			if status == "done" {
				doneReported++
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		legacy.Close()
		t.Fatalf("read copied v5 reports: %v", err)
	}
	if err := rows.Close(); err != nil {
		legacy.Close()
		t.Fatalf("close copied v5 reports: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close copied v5 database: %v", err)
	}
	if reported != 8 {
		t.Fatalf("VACUUM INTO copy has %d reported goals, want 8", reported)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("migrate VACUUM INTO copy: %v", err)
	}
	defer s.Close()
	rows, err = s.DB().Query(`
SELECT id, status, result_summary, work_done, now_possible, how_to_verify,
       surprises, needs_review, next_steps
FROM goals ORDER BY id`)
	if err != nil {
		t.Fatalf("read migrated copied reports: %v", err)
	}
	got := make(map[string]completionTemplateMigratedGoal)
	for rows.Next() {
		var id string
		var goal completionTemplateMigratedGoal
		if err := rows.Scan(&id, &goal.status, &goal.resultSummary, &goal.workDone,
			&goal.nowPossible, &goal.howToVerify, &goal.surprises, &goal.needsReview, &goal.nextSteps); err != nil {
			rows.Close()
			t.Fatalf("scan migrated copied report: %v", err)
		}
		got[id] = goal
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read migrated copied reports: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close migrated copied reports: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("migrated goal count = %d, want %d", len(got), len(want))
	}
	for id, wantGoal := range want {
		gotGoal, ok := got[id]
		if !ok {
			t.Fatalf("migrated goal %s is missing", id)
		}
		if gotGoal.resultSummary != wantGoal.resultSummary {
			t.Fatalf("goal %s result_summary = %q, want %q", id, gotGoal.resultSummary, wantGoal.resultSummary)
		}
		switch wantGoal.status {
		case "active":
			if gotGoal.workDone != "" || gotGoal.nowPossible != "" || gotGoal.howToVerify != "" ||
				gotGoal.surprises != "" || gotGoal.needsReview != "" || gotGoal.nextSteps != "" {
				t.Fatalf("active goal %s has a migrated completion report: %+v", id, gotGoal)
			}
		case "done":
			wantWorkDone := "なし"
			if strings.TrimSpace(wantGoal.resultSummary) != "" {
				wantWorkDone = wantGoal.resultSummary
			}
			if gotGoal.workDone != wantWorkDone {
				t.Fatalf("done goal %s work_done = %q, want %q", id, gotGoal.workDone, wantWorkDone)
			}
			if gotGoal.nowPossible != "なし" || gotGoal.howToVerify != "なし" || gotGoal.surprises != "なし" ||
				gotGoal.needsReview != "なし" || gotGoal.nextSteps != "なし" {
				t.Fatalf("done goal %s placeholders = %+v", id, gotGoal)
			}
		default:
			t.Fatalf("goal %s has unexpected status %q", id, wantGoal.status)
		}
	}
	t.Logf("VACUUM INTO copy preserved %d existing reports (%d done in work_done, active reports retained in result_summary)", reported, doneReported)
}

func TestCompletionTemplateAllFieldsCanBecomeDone(t *testing.T) {
	s, ctx, goal := completionTemplateStore(t)
	report := completionTemplateReport()

	decision, err := s.CompleteGoalWithReport(ctx, goal.ID, report, "completion-template-run")
	if err != nil {
		t.Fatalf("complete goal: %v", err)
	}
	done, err := s.ApproveCompletion(ctx, decision.ID)
	if err != nil {
		t.Fatalf("approve completion: %v", err)
	}
	if done.Status != domain.GoalDone {
		t.Fatalf("goal status = %q, want %q", done.Status, domain.GoalDone)
	}
	if done.WorkDone != report.WorkDone || done.NowPossible != report.NowPossible ||
		done.HowToVerify != report.HowToVerify || done.Surprises != report.Surprises ||
		done.NeedsReview != report.NeedsReview || done.NextSteps != report.NextSteps {
		t.Fatalf("completion report = %+v, want %+v", done, report)
	}
}

func TestCompletionTemplateRejectsEmptyFields(t *testing.T) {
	fields := []struct {
		name  string
		index int
	}{
		{name: "work_done", index: 0},
		{name: "now_possible", index: 1},
		{name: "how_to_verify", index: 2},
		{name: "surprises", index: 3},
		{name: "needs_review", index: 4},
		{name: "next_steps", index: 5},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			s, _, goal := completionTemplateStore(t)
			values := completionTemplateReportValues(completionTemplateReport())
			values[field.index] = ""
			_, err := s.DB().Exec(`
UPDATE goals SET status = 'done',
  work_done = ?, now_possible = ?, how_to_verify = ?,
  surprises = ?, needs_review = ?, next_steps = ?
WHERE id = ?`, append(values, goal.ID)...)
			if err == nil {
				t.Fatalf("updating done goal with empty %s succeeded", field.name)
			}
		})
	}
}

func TestCompletionTemplateAllowsEmptyActiveGoal(t *testing.T) {
	s, _, goal := completionTemplateStore(t)
	if goal.Status != domain.GoalActive {
		t.Fatalf("goal status = %q, want %q", goal.Status, domain.GoalActive)
	}
	if goal.WorkDone != "" || goal.NowPossible != "" || goal.HowToVerify != "" ||
		goal.Surprises != "" || goal.NeedsReview != "" || goal.NextSteps != "" {
		t.Fatalf("new active goal has a non-empty completion report: %+v", goal)
	}
	var count int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM goals WHERE id = ? AND status = 'active'`, goal.ID).Scan(&count); err != nil {
		t.Fatalf("query active goal: %v", err)
	}
	if count != 1 {
		t.Fatalf("active goal count = %d, want 1", count)
	}
}

func TestCompletionTemplateMigratesV5ResultSummary(t *testing.T) {
	dir, err := os.MkdirTemp("", "atct-completion-template-v5-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dbPath := filepath.Join(dir, "atct.db")

	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open v5 database: %v", err)
	}
	_, err = legacy.Exec(`
CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  root_path TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
CREATE TABLE goals (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  result_summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO projects (id, name, root_path, created_at)
VALUES ('project-v5', 'v5 project', '/tmp/completion-template-v5', '2026-08-18T00:00:00Z');
INSERT INTO goals (id, project_id, title, description, status, result_summary, created_at, updated_at)
VALUES
  ('goal-v5-done', 'project-v5', 'old done goal', '', 'done', 'legacy completion report', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z'),
  ('goal-v5-active', 'project-v5', 'old active goal', '', 'active', 'legacy active note', '2026-08-18T00:00:00Z', '2026-08-18T00:00:00Z');
PRAGMA user_version = 5`)
	if err != nil {
		legacy.Close()
		t.Fatalf("create v5 database: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close v5 database: %v", err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("migrate v5 database: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	var version int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 6 {
		t.Fatalf("schema version = %d, want 6", version)
	}
	goal, err := s.GetGoal(context.Background(), "goal-v5-done")
	if err != nil {
		t.Fatalf("read migrated goal: %v", err)
	}
	if goal.WorkDone != "legacy completion report" {
		t.Fatalf("work_done = %q, want legacy report", goal.WorkDone)
	}
	if goal.NowPossible != "なし" || goal.HowToVerify != "なし" || goal.Surprises != "なし" ||
		goal.NeedsReview != "なし" || goal.NextSteps != "なし" {
		t.Fatalf("migrated placeholders = %+v", goal)
	}
	if goal.ResultSummary != "legacy completion report" {
		t.Fatalf("migrated result_summary = %q, want legacy report", goal.ResultSummary)
	}
	active, err := s.GetGoal(context.Background(), "goal-v5-active")
	if err != nil {
		t.Fatalf("read migrated active goal: %v", err)
	}
	if active.WorkDone != "" || active.NowPossible != "" || active.HowToVerify != "" ||
		active.Surprises != "" || active.NeedsReview != "" || active.NextSteps != "" {
		t.Fatalf("migrated active goal has a completion report: %+v", active)
	}
	if active.ResultSummary != "legacy active note" {
		t.Fatalf("migrated active result_summary = %q, want legacy active note", active.ResultSummary)
	}
	if realCopy := os.Getenv("ATCT_COMPLETION_REAL_DB_COPY"); realCopy != "" {
		completionTemplateMeasureRealCopy(t, realCopy)
	}
}
