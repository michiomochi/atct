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

var (
	ErrGoalNotFound    = errors.New("goal not found")
	ErrGoalNotProposed = errors.New("goal is not proposed")
	ErrGoalNotActive   = errors.New("goal is not active")
)

func (s *Store) CreateGoal(ctx context.Context, projectID, content, creator string, derivedFromGoalID ...string) (domain.Goal, error) {
	if strings.TrimSpace(content) == "" {
		return domain.Goal{}, errors.New("goal content must not be blank")
	}
	if len(derivedFromGoalID) > 1 {
		return domain.Goal{}, errors.New("goal can have at most one derived-from goal")
	}
	parentID := ""
	if len(derivedFromGoalID) == 1 {
		parentID = strings.TrimSpace(derivedFromGoalID[0])
	}

	now := time.Now().UTC()
	creator = normalizeGoalCreator([]string{creator})
	status := domain.GoalActive
	if creator == "agent" {
		status = domain.GoalProposed
	}
	g := domain.Goal{
		ID:                uuid.NewString(),
		ProjectID:         projectID,
		DerivedFromGoalID: parentID,
		Content:           content,
		Status:            status,
		Creator:           creator,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	q := sqlcgen.New(s.db)
	err := q.CreateGoal(ctx, sqlcgen.CreateGoalParams{
		ID:                g.ID,
		ProjectID:         g.ProjectID,
		DerivedFromGoalID: nullableGoalID(parentID),
		Content:           g.Content,
		Status:            string(g.Status),
		Creator:           g.Creator,
		CreatedAt:         now.Format(time.RFC3339),
		UpdatedAt:         now.Format(time.RFC3339),
	})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("insert goal: %w", err)
	}
	s.notify.publishEvent(Event{Name: "goal.created", Data: g})
	if creator == "agent" {
		if _, err := s.AskDecision(ctx, AskInput{
			GoalID:   g.ID,
			Kind:     domain.KindGoalApproval,
			Question: "Approve this goal?",
			Options: []domain.Option{
				{Label: "approve", Description: "Approve this goal", Consequence: "The goal becomes active"},
				{Label: "reject", Description: "Reject this goal", Consequence: "The goal is dropped"},
			},
		}); err != nil {
			return domain.Goal{}, fmt.Errorf("ask goal approval: %w", err)
		}
	}
	return g, nil
}

func normalizeGoalCreator(input []string) string {
	if len(input) == 0 {
		return "human"
	}
	if strings.TrimSpace(input[0]) == "human" {
		return "human"
	}
	return "agent"
}

func goalFromRow(row sqlcgen.Goal) (domain.Goal, error) {
	g := domain.Goal{
		ID:                row.ID,
		ProjectID:         row.ProjectID,
		DerivedFromGoalID: row.DerivedFromGoalID.String,
		Content:           row.Content,
		Status:            domain.GoalStatus(row.Status),
		Creator:           row.Creator,
		ResultSummary:     row.ResultSummary,
		WorkDone:          row.WorkDone,
		NowPossible:       row.NowPossible,
		HowToVerify:       row.HowToVerify,
		Surprises:         row.Surprises,
		NeedsReview:       row.NeedsReview,
		NextSteps:         row.NextSteps,
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

func nullableGoalID(id string) sql.NullString {
	return sql.NullString{String: id, Valid: id != ""}
}

func (s *Store) SetGoalDerivedFrom(ctx context.Context, goalID, derivedFromGoalID string) error {
	goalID = strings.TrimSpace(goalID)
	parentID := strings.TrimSpace(derivedFromGoalID)
	if goalID == "" {
		return fmt.Errorf("%w: empty id", ErrGoalNotFound)
	}
	if parentID != "" && goalID == parentID {
		return errors.New("goal cannot be derived from itself")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := sqlcgen.New(s.db).SetGoalDerivedFrom(ctx, sqlcgen.SetGoalDerivedFromParams{
		DerivedFromGoalID: nullableGoalID(parentID),
		UpdatedAt:         now,
		ID:                goalID,
	})
	if err != nil {
		return fmt.Errorf("set goal derived-from goal: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated goal: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrGoalNotFound, goalID)
	}
	return nil
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
	s.notify.publishEvent(Event{Name: "decision.approved", Data: d})
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

// ApproveGoal activates a proposed Goal and applies its approval decision atomically.
func (s *Store) ApproveGoal(ctx context.Context, decisionID string) (domain.Goal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin goal approval tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	goalID, err := q.GetGoalApprovalDecisionGoalID(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}
	if err != nil {
		return domain.Goal{}, fmt.Errorf("lookup goal approval decision: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := q.ApplyGoalApprovalDecision(ctx, sqlcgen.ApplyGoalApprovalDecisionParams{
		AnsweredAt: sql.NullString{String: now, Valid: true},
		AppliedAt:  sql.NullString{String: now, Valid: true},
		ID:         decisionID,
	})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("apply goal approval decision: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return domain.Goal{}, fmt.Errorf("goal approval decision rows affected: %w", err)
	} else if rows != 1 {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}

	result, err = q.MarkGoalActive(ctx, sqlcgen.MarkGoalActiveParams{UpdatedAt: now, ID: goalID})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("activate goal: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return domain.Goal{}, fmt.Errorf("activate goal rows affected: %w", err)
	} else if rows != 1 {
		return domain.Goal{}, fmt.Errorf("%w: %s", ErrGoalNotProposed, goalID)
	}

	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit goal approval: %w", err)
	}
	d, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Goal{}, err
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.notify.publishEvent(Event{Name: "decision.approved", Data: d})
	return s.GetGoal(ctx, goalID)
}

// RejectGoal drops a proposed Goal and records the human's reason atomically.
func (s *Store) RejectGoal(ctx context.Context, decisionID, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin goal rejection tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	goalID, err := q.GetGoalApprovalDecisionGoalID(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}
	if err != nil {
		return fmt.Errorf("lookup goal approval decision: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := q.RejectGoalApprovalDecision(ctx, sqlcgen.RejectGoalApprovalDecisionParams{
		AnswerText: reason,
		AnsweredAt: sql.NullString{String: now, Valid: true},
		ID:         decisionID,
	})
	if err != nil {
		return fmt.Errorf("reject goal approval decision: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("goal rejection decision rows affected: %w", err)
	} else if rows != 1 {
		return fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}

	result, err = q.MarkGoalDropped(ctx, sqlcgen.MarkGoalDroppedParams{UpdatedAt: now, ID: goalID})
	if err != nil {
		return fmt.Errorf("drop goal: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("drop goal rows affected: %w", err)
	} else if rows != 1 {
		return fmt.Errorf("%w: %s", ErrGoalNotProposed, goalID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit goal rejection: %w", err)
	}
	d, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return err
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.notify.publishEvent(Event{Name: "decision.rejected", Data: d})
	return nil
}

// WithdrawActiveGoal drops an active Goal and atomically closes its open work.
func (s *Store) WithdrawActiveGoal(ctx context.Context, goalID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("withdrawal reason is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin goal withdrawal tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := q.WithdrawActiveGoal(ctx, sqlcgen.WithdrawActiveGoalParams{
		ResultSummary: reason,
		UpdatedAt:     now,
		ID:            goalID,
	})
	if err != nil {
		return fmt.Errorf("withdraw goal: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("withdraw goal rows affected: %w", err)
	} else if rows != 1 {
		return fmt.Errorf("%w: %s", ErrGoalNotActive, goalID)
	}

	openDecisions, err := q.ListOpenDecisions(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list open decisions for withdrawn goal: %w", err)
	}
	for _, decision := range openDecisions {
		if err := withdrawDecisionWith(ctx, q, decision.ID, reason); err != nil {
			return fmt.Errorf("withdraw decision %s: %w", decision.ID, err)
		}
	}

	result, err = q.DropOpenTasksForGoal(ctx, sqlcgen.DropOpenTasksForGoalParams{
		UpdatedAt: now,
		GoalID:    goalID,
	})
	if err != nil {
		return fmt.Errorf("drop open tasks for withdrawn goal: %w", err)
	}
	if _, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("drop open tasks rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit goal withdrawal: %w", err)
	}

	for _, decision := range openDecisions {
		d, err := s.GetDecision(ctx, decision.ID)
		if err != nil {
			return err
		}
		s.notify.publish(decision.ID)
		s.notify.publishEvent(Event{Name: "decision.withdrawn", Data: d})
	}
	if len(openDecisions) > 0 {
		s.notify.publishAll()
	}
	return nil
}
