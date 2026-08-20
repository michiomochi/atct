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
	"github.com/michiomochi/atct/internal/store/sqlcgen"
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
	q := sqlcgen.New(s.db)
	err := q.CreateGoal(ctx, sqlcgen.CreateGoalParams{
		ID:          g.ID,
		ProjectID:   g.ProjectID,
		Title:       g.Title,
		Description: g.Description,
		Status:      string(g.Status),
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
	})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("insert goal: %w", err)
	}
	return g, nil
}

func goalFromRow(row sqlcgen.Goal) (domain.Goal, error) {
	g := domain.Goal{
		ID:            row.ID,
		ProjectID:     row.ProjectID,
		Title:         row.Title,
		Description:   row.Description,
		Status:        domain.GoalStatus(row.Status),
		ResultSummary: row.ResultSummary,
		WorkDone:      row.WorkDone,
		NowPossible:   row.NowPossible,
		HowToVerify:   row.HowToVerify,
		Surprises:     row.Surprises,
		NeedsReview:   row.NeedsReview,
		NextSteps:     row.NextSteps,
	}
	var err error
	if g.CreatedAt, err = time.Parse(time.RFC3339, row.CreatedAt); err != nil {
		return domain.Goal{}, fmt.Errorf("parse created_at: %w", err)
	}
	if g.UpdatedAt, err = time.Parse(time.RFC3339, row.UpdatedAt); err != nil {
		return domain.Goal{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return g, nil
}

func (s *Store) GetGoal(ctx context.Context, id string) (domain.Goal, error) {
	row, err := sqlcgen.New(s.db).GetGoal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrGoalNotFound, id)
	}
	if err != nil {
		return domain.Goal{}, err
	}
	return goalFromRow(row)
}

func (s *Store) ListGoals(ctx context.Context, projectID string) ([]domain.Goal, error) {
	rows, err := sqlcgen.New(s.db).ListGoals(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("query goals: %w", err)
	}

	var out []domain.Goal
	for _, row := range rows {
		g, err := goalFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *Store) ListAllGoals(ctx context.Context) ([]domain.Goal, error) {
	rows, err := sqlcgen.New(s.db).ListAllGoals(ctx)
	if err != nil {
		return nil, fmt.Errorf("query all goals: %w", err)
	}

	var out []domain.Goal
	for _, row := range rows {
		g, err := goalFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
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
func (s *Store) CompleteGoal(ctx context.Context, goalID, resultSummary, agentSessionID string) (domain.Decision, error) {
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
	}, agentSessionID)
}

// CompleteGoalWithReport creates a kind=completion Decision.
// A Goal cannot close while a child Decision is open (invariant 4).
func (s *Store) CompleteGoalWithReport(ctx context.Context, goalID string, report domain.CompletionReport, agentSessionID string) (domain.Decision, error) {
	if err := validateCompletionReport(report); err != nil {
		return domain.Decision{}, err
	}

	q := sqlcgen.New(s.db)
	open, err := q.CountOpenDecisionsForGoal(ctx, goalID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("count open decisions: %w", err)
	}
	if open > 0 {
		return domain.Decision{}, fmt.Errorf("%w: %s", ErrGoalHasOpenDecision, goalID)
	}

	if _, err := q.UpdateGoalCompletionReport(ctx, sqlcgen.UpdateGoalCompletionReportParams{
		ResultSummary: report.WorkDone,
		WorkDone:      report.WorkDone,
		NowPossible:   report.NowPossible,
		HowToVerify:   report.HowToVerify,
		Surprises:     report.Surprises,
		NeedsReview:   report.NeedsReview,
		NextSteps:     report.NextSteps,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		ID:            goalID,
	}); err != nil {
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
		AgentSessionID: agentSessionID,
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

	q := sqlcgen.New(tx)
	goalID, err := q.GetCompletionDecisionGoalID(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}
	if err != nil {
		return domain.Goal{}, fmt.Errorf("lookup completion decision: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := q.ApplyCompletionDecision(ctx, sqlcgen.ApplyCompletionDecisionParams{
		AnsweredAt: sql.NullString{String: now, Valid: true},
		AppliedAt:  sql.NullString{String: now, Valid: true},
		ID:         decisionID,
	}); err != nil {
		return domain.Goal{}, fmt.Errorf("apply completion decision: %w", err)
	}
	if _, err := q.MarkGoalDone(ctx, sqlcgen.MarkGoalDoneParams{UpdatedAt: now, ID: goalID}); err != nil {
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
