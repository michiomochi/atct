package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

var ErrGoalNotFound = errors.New("goal not found")

func (s *Store) CreateGoal(ctx context.Context, projectID, title, description string) (domain.Goal, error) {
	now := time.Now().UTC()
	g := domain.Goal{
		ID:          uuid.NewString(),
		ProjectID:   projectID,
		Title:       title,
		Description: description,
		Status:      domain.GoalActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO goals (
			id, project_id, title, description, status,
			work_done, now_possible, how_to_verify, surprises, needs_review, next_steps,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, '', '', '', '', '', '', ?, ?)`,
		g.ID, g.ProjectID, g.Title, g.Description, string(g.Status),
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return domain.Goal{}, fmt.Errorf("insert goal: %w", err)
	}
	return g, nil
}

func scanGoal(sc interface{ Scan(...any) error }) (domain.Goal, error) {
	var g domain.Goal
	var status, createdAt, updatedAt string
	if err := sc.Scan(&g.ID, &g.ProjectID, &g.Title, &g.Description,
		&status, &g.WorkDone, &g.NowPossible, &g.HowToVerify, &g.Surprises,
		&g.NeedsReview, &g.NextSteps, &createdAt, &updatedAt); err != nil {
		return domain.Goal{}, err
	}
	g.Status = domain.GoalStatus(status)
	g.ResultSummary = g.WorkDone
	var err error
	if g.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return domain.Goal{}, fmt.Errorf("parse created_at: %w", err)
	}
	if g.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return domain.Goal{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return g, nil
}

const goalColumns = `id, project_id, title, description, status, work_done, now_possible, how_to_verify, surprises, needs_review, next_steps, created_at, updated_at`

func (s *Store) GetGoal(ctx context.Context, id string) (domain.Goal, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+goalColumns+` FROM goals WHERE id = ?`, id)
	g, err := scanGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrGoalNotFound, id)
	}
	return g, err
}

func (s *Store) ListGoals(ctx context.Context, projectID string) ([]domain.Goal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+goalColumns+` FROM goals WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query goals: %w", err)
	}
	defer rows.Close()

	var out []domain.Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) ListAllGoals(ctx context.Context) ([]domain.Goal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+goalColumns+` FROM goals ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("query all goals: %w", err)
	}
	defer rows.Close()

	var out []domain.Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

type completionReportField struct {
	name  string
	value string
}

func completionReportFields(report domain.CompletionReport) []completionReportField {
	return []completionReportField{
		{name: "work_done", value: report.WorkDone},
		{name: "now_possible", value: report.NowPossible},
		{name: "how_to_verify", value: report.HowToVerify},
		{name: "surprises", value: report.Surprises},
		{name: "needs_review", value: report.NeedsReview},
		{name: "next_steps", value: report.NextSteps},
	}
}

func validateCompletionReport(report domain.CompletionReport) error {
	fields := completionReportFields(report)
	var empty []string
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			empty = append(empty, field.name)
		}
	}
	if len(empty) > 0 {
		return fmt.Errorf("completion report fields are empty: %s", strings.Join(empty, ", "))
	}
	for _, field := range fields {
		if utf8.RuneCountInString(field.value) > completionReportMaxLength {
			return fmt.Errorf("completion report field %s exceeds %d characters", field.name, completionReportMaxLength)
		}
	}
	return nil
}

// CompleteGoal keeps the pre-v6 Go call shape source-compatible for packages
// that have not adopted the structured report yet. The MCP API uses
// CompleteGoalWithReport and does not expose this compatibility path.
func (s *Store) CompleteGoal(ctx context.Context, goalID, resultSummary, runID string) (domain.Decision, error) {
	if strings.TrimSpace(resultSummary) == "" {
		resultSummary = "なし"
	}
	return s.CompleteGoalWithReport(ctx, goalID, domain.CompletionReport{
		WorkDone:    resultSummary,
		NowPossible: "なし",
		HowToVerify: "なし",
		Surprises:   "なし",
		NeedsReview: "なし",
		NextSteps:   "なし",
	}, runID)
}

// CompleteGoalWithReport creates a kind=completion Decision.
// A Goal cannot close while a child Decision is open (invariant 4).
func (s *Store) CompleteGoalWithReport(ctx context.Context, goalID string, report domain.CompletionReport, runID string) (domain.Decision, error) {
	if err := validateCompletionReport(report); err != nil {
		return domain.Decision{}, err
	}

	var open int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decisions WHERE goal_id = ? AND status = 'open'`, goalID).Scan(&open)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("count open decisions: %w", err)
	}
	if open > 0 {
		return domain.Decision{}, fmt.Errorf("%w: %s", ErrGoalHasOpenDecision, goalID)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE goals SET
			work_done = ?, now_possible = ?, how_to_verify = ?,
			surprises = ?, needs_review = ?, next_steps = ?, updated_at = ?
		WHERE id = ?`,
		report.WorkDone, report.NowPossible, report.HowToVerify,
		report.Surprises, report.NeedsReview, report.NextSteps,
		time.Now().UTC().Format(time.RFC3339), goalID); err != nil {
		return domain.Decision{}, fmt.Errorf("set completion report: %w", err)
	}

	return s.AskDecision(ctx, AskInput{
		GoalID:   goalID,
		Kind:     domain.KindCompletion,
		Question: "Approve this goal as complete?",
		Options: []domain.Option{
			{Label: "approve", Description: "Approve as complete", Consequence: "The goal becomes done"},
			{Label: "reject", Description: "Send back", Consequence: "The goal remains active and the agent continues"},
		},
		RunID: runID,
	})
}

// ApproveCompletion marks the Goal done and the Decision applied atomically.
// No agent follow-up needs to be applied, so there is no reason to wait for receipt (invariant 3).
func (s *Store) ApproveCompletion(ctx context.Context, decisionID string) (domain.Goal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var goalID string
	err = tx.QueryRowContext(ctx,
		`SELECT goal_id FROM decisions WHERE id = ? AND kind = 'completion' AND status = 'open'`,
		decisionID).Scan(&goalID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}
	if err != nil {
		return domain.Goal{}, fmt.Errorf("lookup completion decision: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE decisions SET status = 'applied', answer_label = 'approve',
				 answered_at = ?, applied_at = ? WHERE id = ?`,
		now, now, decisionID); err != nil {
		return domain.Goal{}, fmt.Errorf("apply completion decision: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE goals SET status = 'done', updated_at = ? WHERE id = ?`, now, goalID); err != nil {
		return domain.Goal{}, fmt.Errorf("close goal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit: %w", err)
	}
	d, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Goal{}, err
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.notify.publishEvent(DecisionEvent{Name: "decision.approved", Decision: d})
	return s.GetGoal(ctx, goalID)
}

// RejectCompletion leaves the Goal active and the Decision answered.
// It becomes applied when the agent receives the rejection reason.
func (s *Store) RejectCompletion(ctx context.Context, decisionID, reason string) error {
	_, err := s.answerDecision(ctx, AnswerInput{
		DecisionID:  decisionID,
		AnswerLabel: "reject",
		AnswerText:  reason,
	}, "decision.rejected")
	return err
}
