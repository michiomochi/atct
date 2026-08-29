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
	ErrGoalNotFound        = errors.New("goal not found")
	ErrGoalNotProposed     = errors.New("goal is not proposed")
	ErrGoalNotActive       = errors.New("goal is not active")
	ErrGoalAlreadyClaimed  = errors.New("goal already claimed")
	ErrGoalSelfReference   = errors.New("goal cannot be derived from itself")
	ErrGoalDerivationCycle = errors.New("goal derivation would create a cycle")
)

func (s *Store) CreateGoal(ctx context.Context, projectID int64, content, creator string, derivedFromGoalID ...int64) (domain.Goal, error) {
	if strings.TrimSpace(content) == "" {
		return domain.Goal{}, errors.New("goal content must not be blank")
	}
	if len(derivedFromGoalID) > 1 {
		return domain.Goal{}, errors.New("goal can have at most one derived-from goal")
	}
	var parentID int64
	if len(derivedFromGoalID) == 1 {
		parentID = derivedFromGoalID[0]
		if parentID != 0 {
			if _, err := s.GetGoal(ctx, parentID); err != nil {
				return domain.Goal{}, err
			}
		}
	}

	now := time.Now().UTC()
	creator = normalizeGoalCreator([]string{creator})
	status := domain.GoalActive
	if creator == "agent" {
		status = domain.GoalProposed
	}
	g := domain.Goal{
		ProjectID:         projectID,
		DerivedFromGoalID: parentID,
		Content:           content,
		Status:            status,
		Creator:           creator,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	q := sqlcgen.New(s.db)
	id, err := q.CreateGoal(ctx, sqlcgen.CreateGoalParams{
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
	g.ID = id
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

func (s *Store) UpdateGoalContent(ctx context.Context, goalID int64, content string) (domain.Goal, error) {
	if strings.TrimSpace(content) == "" {
		return domain.Goal{}, errors.New("goal content must not be blank")
	}
	if goalID == 0 {
		return domain.Goal{}, fmt.Errorf("%w: empty id", ErrGoalNotFound)
	}

	result, err := sqlcgen.New(s.db).UpdateGoalContent(ctx, sqlcgen.UpdateGoalContentParams{
		Content:   content,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		ID:        goalID,
	})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("update goal content: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Goal{}, fmt.Errorf("check updated goal content: %w", err)
	}
	if affected == 0 {
		if _, err := s.GetGoal(ctx, goalID); err != nil {
			return domain.Goal{}, err
		}
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrGoalNotProposed, goalID)
	}
	return s.GetGoal(ctx, goalID)
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
		DerivedFromGoalID: row.DerivedFromGoalID.Int64,
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

func goalFromFields(id, projectID int64, derivedFromGoalID sql.NullInt64, content, status, creator, resultSummary, workDone, nowPossible, howToVerify, surprises, needsReview, nextSteps, createdAt, updatedAt string) (domain.Goal, error) {
	return goalFromRow(sqlcgen.Goal{ID: id, ProjectID: projectID, DerivedFromGoalID: derivedFromGoalID, Content: content, Status: status, Creator: creator, ResultSummary: resultSummary, WorkDone: workDone, NowPossible: nowPossible, HowToVerify: howToVerify, Surprises: surprises, NeedsReview: needsReview, NextSteps: nextSteps, CreatedAt: createdAt, UpdatedAt: updatedAt})
}

// derivedFromGoalID narrows the value GetGoal selects. The query casts the
// column so a dangling reference SQLite could not coerce to INTEGER reads as
// NULL instead of failing the scan, and sqlc types a computed column as any.
func derivedFromGoalID(value any) sql.NullInt64 {
	id, ok := value.(int64)
	if !ok {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id, Valid: true}
}

func (s *Store) GetGoal(ctx context.Context, id int64) (domain.Goal, error) {
	row, err := sqlcgen.New(s.db).GetGoal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrGoalNotFound, id)
	}
	if err != nil {
		return domain.Goal{}, err
	}
	return goalFromFields(row.ID, row.ProjectID, derivedFromGoalID(row.DerivedFromGoalID), row.Content, row.Status, row.Creator, row.ResultSummary, row.WorkDone, row.NowPossible, row.HowToVerify, row.Surprises, row.NeedsReview, row.NextSteps, row.CreatedAt, row.UpdatedAt)
}

// ClaimGoal records the agent session that owns a goal. An empty session ID
// clears the claim; callers that only need to release a claim can use
// ReleaseGoal.
func (s *Store) ClaimGoal(ctx context.Context, goalID int64, agentSessionID int64) (domain.Goal, error) {
	if goalID == 0 {
		return domain.Goal{}, fmt.Errorf("%w: empty id", ErrGoalNotFound)
	}
	if agentSessionID == 0 {
		if err := s.ReleaseGoal(ctx, goalID); err != nil {
			return domain.Goal{}, err
		}
		return s.GetGoal(ctx, goalID)
	}
	if _, err := s.GetGoal(ctx, goalID); err != nil {
		return domain.Goal{}, err
	}
	open, err := s.openGoalHandoff(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("find open goal claim: %w", err)
	}
	if open != nil {
		owner := open.ReceivedBy
		if owner == 0 {
			owner = open.RequestedBy
		}
		if owner == agentSessionID {
			return s.GetGoal(ctx, goalID)
		}
	}

	handoffID := uuid.NewString()
	if err := s.reclaimOpenGoalHandoff(ctx, handoffID, goalID); err != nil {
		return domain.Goal{}, mapGoalClaimHandoffError(goalID, err)
	}
	if _, err := s.requestGoalHandoffForClaim(ctx, handoffID, goalID, agentSessionID); err != nil {
		return domain.Goal{}, mapGoalClaimHandoffError(goalID, err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoffID, goalID, agentSessionID); err != nil {
		return domain.Goal{}, fmt.Errorf("receive goal claim handoff: %w", err)
	}
	return s.GetGoal(ctx, goalID)
}

func mapGoalClaimHandoffError(goalID int64, err error) error {
	if errors.Is(err, ErrGoalHandoffAlreadyOpen) || strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		return fmt.Errorf("%w: %d", ErrGoalAlreadyClaimed, goalID)
	}
	return err
}

// ReleaseGoal clears a goal's claim.
func (s *Store) ReleaseGoal(ctx context.Context, goalID int64) error {
	if goalID == 0 {
		return fmt.Errorf("%w: empty id", ErrGoalNotFound)
	}
	if _, err := s.CompleteGoalHandoffForGoal(ctx, goalID, goalHandoffReleasedReport); err != nil {
		return fmt.Errorf("complete goal handoff after release: %w", err)
	}
	return nil
}

func nullableGoalID(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id != 0}
}

func (s *Store) SetGoalDerivedFrom(ctx context.Context, goalID, derivedFromGoalID int64) error {
	parentID := derivedFromGoalID
	if goalID == 0 {
		return fmt.Errorf("%w: empty id", ErrGoalNotFound)
	}
	if parentID != 0 && goalID == parentID {
		return ErrGoalSelfReference
	}
	if parentID != 0 {
		if _, err := s.GetGoal(ctx, parentID); err != nil {
			return err
		}
		seen := make(map[string]struct{})
		for currentID := parentID; currentID != 0; {
			if currentID == goalID {
				return ErrGoalDerivationCycle
			}
			if _, ok := seen[fmt.Sprint(currentID)]; ok {
				return ErrGoalDerivationCycle
			}
			seen[fmt.Sprint(currentID)] = struct{}{}

			parent, err := s.GetGoal(ctx, currentID)
			if err != nil {
				return err
			}
			currentID = parent.DerivedFromGoalID
		}
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
		return fmt.Errorf("%w: %d", ErrGoalNotFound, goalID)
	}
	return nil
}

func (s *Store) ListGoals(ctx context.Context, projectID int64) ([]domain.Goal, error) {
	rows, err := sqlcgen.New(s.db).ListGoals(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("query goals: %w", err)
	}

	var out []domain.Goal
	for _, row := range rows {
		g, err := goalFromFields(row.ID, row.ProjectID, row.DerivedFromGoalID, row.Content, row.Status, row.Creator, row.ResultSummary, row.WorkDone, row.NowPossible, row.HowToVerify, row.Surprises, row.NeedsReview, row.NextSteps, row.CreatedAt, row.UpdatedAt)
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
		g, err := goalFromFields(row.ID, row.ProjectID, row.DerivedFromGoalID, row.Content, row.Status, row.Creator, row.ResultSummary, row.WorkDone, row.NowPossible, row.HowToVerify, row.Surprises, row.NeedsReview, row.NextSteps, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *Store) ListDerivedGoals(ctx context.Context, derivedFromGoalID int64) ([]domain.Goal, error) {
	rows, err := sqlcgen.New(s.db).ListDerivedGoals(ctx, nullableGoalID(derivedFromGoalID))
	if err != nil {
		return nil, fmt.Errorf("query derived goals: %w", err)
	}

	out := make([]domain.Goal, 0, len(rows))
	for _, row := range rows {
		g, err := goalFromFields(row.ID, row.ProjectID, row.DerivedFromGoalID, row.Content, row.Status, row.Creator, row.ResultSummary, row.WorkDone, row.NowPossible, row.HowToVerify, row.Surprises, row.NeedsReview, row.NextSteps, row.CreatedAt, row.UpdatedAt)
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

func goalNotActiveForCompletionError(goalID int64, status domain.GoalStatus) error {
	var stateMessage string
	switch status {
	case domain.GoalProposed:
		stateMessage = fmt.Sprintf("goal %d is proposed, not active; approve it before reporting completion (承認前のゴールには完了報告を出せません)", goalID)
	case domain.GoalDone:
		stateMessage = fmt.Sprintf("goal %d is done, not active; the approved completion report was left unchanged (完了済みのゴールには完了報告を出せません。承認済みの文章はそのままです)", goalID)
	case domain.GoalDropped:
		stateMessage = fmt.Sprintf("goal %d is dropped, not active; the completion report was left unchanged (取り下げ済みのゴールには完了報告を出せません。完了報告はそのままです)", goalID)
	default:
		stateMessage = fmt.Sprintf("goal %d has status %q, not active; the completion report was left unchanged (ゴールの状態 %q はアクティブではないため、完了報告を出せません。完了報告はそのままです)", goalID, status, status)
	}
	return fmt.Errorf("%w: %s", ErrGoalNotActive, stateMessage)
}

// CompleteGoal keeps the pre-v6 Go call shape source-compatible for packages
// that have not adopted the structured report yet. The MCP API uses
// CompleteGoalWithReport and does not expose this compatibility path.
func (s *Store) CompleteGoal(ctx context.Context, goalID int64, resultSummary string, agentSessionID int64) (domain.Decision, error) {
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
func (s *Store) CompleteGoalWithReport(ctx context.Context, goalID int64, report domain.CompletionReport, agentSessionID int64) (domain.Decision, error) {
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("get goal for completion: %w", err)
	}
	if goal.Status != domain.GoalActive {
		return domain.Decision{}, goalNotActiveForCompletionError(goalID, goal.Status)
	}

	if err := validateCompletionReport(report); err != nil {
		return domain.Decision{}, err
	}

	q := sqlcgen.New(s.db)
	open, err := q.CountOpenDecisionsForGoal(ctx, goalID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("count open decisions: %w", err)
	}
	if open > 0 {
		return domain.Decision{}, fmt.Errorf("%w: %d", ErrGoalHasOpenDecision, goalID)
	}

	result, err := q.UpdateGoalCompletionReport(ctx, sqlcgen.UpdateGoalCompletionReportParams{
		ResultSummary: report.WorkDone,
		WorkDone:      report.WorkDone,
		NowPossible:   report.NowPossible,
		HowToVerify:   report.HowToVerify,
		Surprises:     report.Surprises,
		NeedsReview:   report.NeedsReview,
		NextSteps:     report.NextSteps,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		ID:            goalID,
	})
	if err != nil {
		return domain.Decision{}, fmt.Errorf("set completion report: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return domain.Decision{}, fmt.Errorf("set completion report rows affected: %w", err)
	} else if rows != 1 {
		goal, err := s.GetGoal(ctx, goalID)
		if err != nil {
			return domain.Decision{}, fmt.Errorf("get goal after completion report update: %w", err)
		}
		return domain.Decision{}, goalNotActiveForCompletionError(goalID, goal.Status)
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
func (s *Store) ApproveCompletion(ctx context.Context, decisionID int64) (domain.Goal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	goalID, err := q.GetCompletionDecisionGoalID(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
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
func (s *Store) RejectCompletion(ctx context.Context, decisionID int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin completion rejection tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := q.AnswerDecision(ctx, sqlcgen.AnswerDecisionParams{
		AnswerLabel: "reject",
		AnswerText:  reason,
		AnsweredAt:  sql.NullString{String: now, Valid: true},
		ID:          decisionID,
	})
	if err != nil {
		return fmt.Errorf("update decision: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("rows affected: %w", err)
	} else if rows == 0 {
		return fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
	}

	d, err := q.GetDecision(ctx, decisionID)
	if err != nil {
		return fmt.Errorf("get decision: %w", err)
	}
	if d.Kind == "completion" && d.AgentSessionID != 0 {
		handoffs, err := q.ListGoalHandoffs(ctx, d.GoalID)
		if err != nil {
			return fmt.Errorf("list goal handoffs for completion rejection: %w", err)
		}

		var selected *GoalHandoff
		for _, row := range handoffs {
			handoff, err := goalHandoffFromRow(row)
			if err != nil {
				return fmt.Errorf("parse goal handoff for completion rejection: %w", err)
			}
			if handoff.CompletedReportAt == nil {
				selected = nil
				break
			}
			if handoff.ReceivedAt == nil || handoff.ReceivedBy != d.AgentSessionID {
				continue
			}
			if selected == nil || handoff.CompletedReportAt.After(*selected.CompletedReportAt) {
				candidate := handoff
				selected = &candidate
			}
		}

		if selected != nil {
			reopenID := fmt.Sprintf("%s-reopen-%d", selected.ID, decisionID)
			requestReport := "完了報告が却下されたため handoff を再発行した"
			if reason != "" {
				requestReport += ": " + reason
			}
			handoffNow := time.Now().UTC().Format(time.RFC3339Nano)
			txq := q.WithTx(tx)
			if err := txq.RequestGoalHandoff(ctx, sqlcgen.RequestGoalHandoffParams{
				ID:            reopenID,
				GoalID:        selected.GoalID,
				RequestedBy:   sql.NullInt64{Int64: selected.RequestedBy, Valid: selected.RequestedBy != 0},
				RequestedAt:   sql.NullString{String: handoffNow, Valid: true},
				RequestReport: sql.NullString{String: requestReport, Valid: true},
			}); err != nil {
				return fmt.Errorf("request reopened goal handoff: %w", err)
			}
			result, err := txq.ReceiveGoalHandoff(ctx, sqlcgen.ReceiveGoalHandoffParams{
				ID:         reopenID,
				GoalID:     selected.GoalID,
				ReceivedBy: sql.NullInt64{Int64: selected.ReceivedBy, Valid: selected.ReceivedBy != 0},
				ReceivedAt: sql.NullString{String: handoffNow, Valid: true},
			})
			if err != nil {
				return fmt.Errorf("receive reopened goal handoff: %w", err)
			}
			if rows, err := result.RowsAffected(); err != nil {
				return fmt.Errorf("reopened goal handoff rows affected: %w", err)
			} else if rows != 1 {
				return fmt.Errorf("reopened goal handoff was not received: %q", reopenID)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit completion rejection: %w", err)
	}
	domainDecision, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return err
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.notify.publishEvent(Event{Name: "decision.rejected", Data: domainDecision})
	return nil
}

// ApproveGoal activates a proposed Goal and applies its approval decision atomically.
func (s *Store) ApproveGoal(ctx context.Context, decisionID int64) (domain.Goal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin goal approval tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	goalID, err := q.GetGoalApprovalDecisionGoalID(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
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
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
	}

	result, err = q.MarkGoalActive(ctx, sqlcgen.MarkGoalActiveParams{UpdatedAt: now, ID: goalID})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("activate goal: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return domain.Goal{}, fmt.Errorf("activate goal rows affected: %w", err)
	} else if rows != 1 {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrGoalNotProposed, goalID)
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
func (s *Store) RejectGoal(ctx context.Context, decisionID int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin goal rejection tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	goalID, err := q.GetGoalApprovalDecisionGoalID(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
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
		return fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
	}

	result, err = q.MarkGoalDropped(ctx, sqlcgen.MarkGoalDroppedParams{UpdatedAt: now, ID: goalID})
	if err != nil {
		return fmt.Errorf("drop goal: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("drop goal rows affected: %w", err)
	} else if rows != 1 {
		return fmt.Errorf("%w: %d", ErrGoalNotProposed, goalID)
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
func (s *Store) WithdrawActiveGoal(ctx context.Context, goalID int64, reason string) error {
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
		return fmt.Errorf("%w: %d", ErrGoalNotActive, goalID)
	}

	openDecisions, err := q.ListOpenDecisions(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list open decisions for withdrawn goal: %w", err)
	}
	withdrawnDecisionIDs := make([]int64, 0, len(openDecisions))
	for _, decision := range openDecisions {
		withdrawnDecisionIDs = append(withdrawnDecisionIDs, decision.ID)
		if err := withdrawDecisionWith(ctx, q, decision.ID, reason); err != nil {
			return fmt.Errorf("withdraw decision %d: %w", decision.ID, err)
		}
	}

	tasks, err := q.ListTasks(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list tasks for withdrawn goal: %w", err)
	}
	droppedTaskIDs := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == string(domain.TaskTodo) || task.Status == string(domain.TaskDoing) {
			droppedTaskIDs = append(droppedTaskIDs, task.ID)
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
	openTaskHandoffs, err := q.ListOpenTaskHandoffsForGoal(ctx, goalID)
	if err != nil {
		return fmt.Errorf("list task handoffs for withdrawn goal: %w", err)
	}
	closedHandoffIDs := make([]string, 0, len(openTaskHandoffs))
	for _, handoff := range openTaskHandoffs {
		if handoffIsDelegation(handoff.RequestedBy.Int64, handoff.ReceivedBy.Int64) {
			closedHandoffIDs = append(closedHandoffIDs, handoff.ID)
		}
		result, err := q.CompleteTaskHandoff(ctx, sqlcgen.CompleteTaskHandoffParams{
			ID:                handoff.ID,
			TaskID:            handoff.TaskID,
			CompletedReportAt: sql.NullString{String: now, Valid: true},
			CompleteReport:    sql.NullString{String: reason, Valid: reason != ""},
		})
		if err != nil {
			return fmt.Errorf("complete task handoff %s for withdrawn goal: %w", handoff.ID, err)
		}
		if _, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("complete task handoff %s rows affected: %w", handoff.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit goal withdrawal: %w", err)
	}

	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return err
	}
	s.notify.publishEvent(Event{
		Name: EventGoalWithdrawn,
		Data: GoalWithdrawnEvent{
			GoalID:               goalID,
			ProjectID:            goal.ProjectID,
			Reason:               reason,
			DroppedTaskIDs:       droppedTaskIDs,
			ClosedTaskHandoffIDs: closedHandoffIDs,
			WithdrawnDecisionIDs: withdrawnDecisionIDs,
		},
	})
	for _, decision := range openDecisions {
		d, err := s.GetDecision(ctx, decision.ID)
		if err != nil {
			return err
		}
		s.notify.publish(decision.ID)
		s.notify.publishEvent(Event{Name: "decision.withdrawn", Data: d})
	}
	s.notify.publishAll()
	return nil
}
