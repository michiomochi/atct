package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	ErrGoalNotFound                = errors.New("goal not found")
	ErrGoalNotProposed             = errors.New("goal is not proposed")
	ErrGoalNotActive               = errors.New("goal is not active")
	ErrGoalReviewOpen              = errors.New("goal review is already open")
	ErrGoalReviewNotFound          = errors.New("goal review not found")
	ErrGoalReviewNotApproved       = errors.New("goal review is not approved")
	ErrGoalReviewHandoffIncomplete = errors.New("goal review requires a completed delegated goal handoff")
	ErrGoalAlreadyClaimed          = errors.New("goal already claimed")
	ErrGoalSelfReference           = errors.New("goal cannot be derived from itself")
	ErrGoalDerivationCycle         = errors.New("goal derivation would create a cycle")
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin goal creation tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
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
	event := DecisionEvent{Name: "goal.created", Data: g, OccurredAt: g.CreatedAt}
	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit goal creation: %w", err)
	}
	s.publishWorkflowEvents([]DecisionEvent{event})
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

// HasGoalReview reports whether a goal has entered the human goal-review
// lifecycle. It is used by the daemon to distinguish the legacy completion
// request from the explicit review-then-complete flow.
func (s *Store) HasGoalReview(ctx context.Context, goalID int64) (bool, error) {
	exists, err := sqlcgen.New(s.db).HasGoalReview(ctx, goalID)
	if err != nil {
		return false, fmt.Errorf("check goal review: %w", err)
	}
	return exists, nil
}

func (s *Store) latestGoalReview(ctx context.Context, goalID int64) (domain.Decision, bool, error) {
	decisionID, err := sqlcgen.New(s.db).GetLatestGoalReviewID(ctx, goalID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Decision{}, false, nil
	}
	if err != nil {
		return domain.Decision{}, false, fmt.Errorf("find latest goal review: %w", err)
	}
	decision, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Decision{}, false, err
	}
	return decision, true, nil
}

func latestDelegatedGoalHandoff(handoffs []GoalHandoff) *GoalHandoff {
	var latest *GoalHandoff
	for i := range handoffs {
		candidate := &handoffs[i]
		if !handoffIsDelegation(candidate.RequestedBy, candidate.ReceivedBy) {
			continue
		}
		if latest == nil || (candidate.RequestedAt != nil && (latest.RequestedAt == nil || candidate.RequestedAt.After(*latest.RequestedAt))) {
			latest = candidate
		}
	}
	return latest
}

func goalHandoffHasCommanderReviewCompletion(handoff *GoalHandoff) bool {
	return handoff.RequestedAt != nil &&
		handoff.ReceivedAt != nil &&
		handoff.ReviewRequestedAt != nil &&
		handoff.ReviewReceivedAt != nil &&
		handoff.ReviewRequestedBy != 0 &&
		handoff.ReviewRequestedBy == handoff.ReceivedBy &&
		handoff.ReviewReceivedBy != 0 &&
		handoff.ReviewReceivedBy == handoff.RequestedBy &&
		handoff.CompletedReportAt != nil &&
		handoff.CompleteReport != goalHandoffReclaimedReport &&
		handoff.CompleteReport != goalHandoffReleasedReport
}

func (s *Store) requireLatestGoalHandoffForReview(ctx context.Context, goalID int64, previous domain.Decision, hasPrevious bool) error {
	handoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return fmt.Errorf("find goal handoff for review: %w", err)
	}
	latest := latestDelegatedGoalHandoff(handoffs)
	if latest == nil {
		return fmt.Errorf("%w: goal %d has no delegated goal handoff", ErrGoalReviewHandoffIncomplete, goalID)
	}
	if !goalHandoffHasCommanderReviewCompletion(latest) {
		return fmt.Errorf("%w: %s", ErrGoalReviewHandoffIncomplete, latest.ID)
	}
	if hasPrevious && previous.Status == domain.DecisionAnswered && previous.AnswerLabel == "reject" && (previous.AnsweredAt == nil || latest.RequestedAt == nil || !latest.RequestedAt.After(*previous.AnsweredAt)) {
		return fmt.Errorf("%w: goal review %d was rejected after handoff %s completed; request a new handoff", ErrGoalReviewHandoffIncomplete, previous.ID, latest.ID)
	}
	return nil
}

// RequestGoalReview creates the taskless decision that asks the human to
// review a goal after its goal handoff has been accepted. The completion report
// is persisted in the same transaction as the goal-review decision and its
// creation event.
func (s *Store) RequestGoalReview(ctx context.Context, goalID, agentSessionID int64, reports ...domain.CompletionReport) (domain.Decision, error) {
	if len(reports) == 0 {
		return domain.Decision{}, errors.New("goal review requires a completion report")
	}
	if len(reports) > 1 {
		return domain.Decision{}, errors.New("goal review accepts exactly one completion report")
	}
	report := reports[0]
	if err := validateCompletionReport(report); err != nil {
		return domain.Decision{}, err
	}

	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("get goal for review: %w", err)
	}
	if goal.Status != domain.GoalActive {
		return domain.Decision{}, goalNotActiveForCompletionError(goalID, goal.Status)
	}
	previous, ok, err := s.latestGoalReview(ctx, goalID)
	if err != nil {
		return domain.Decision{}, err
	}
	if ok && previous.Status == domain.DecisionOpen {
		return domain.Decision{}, fmt.Errorf("%w: %d", ErrGoalReviewOpen, goalID)
	}
	if err := s.requireLatestGoalHandoffForReview(ctx, goalID, previous, ok); err != nil {
		return domain.Decision{}, err
	}
	options := []domain.Option{
		{Label: "approve", Description: "Approve the reviewed goal", Consequence: "The commander may merge and complete the goal"},
		{Label: "reject", Description: "Reject the reviewed goal", Consequence: "The goal remains active and the commander must reissue the handoff"},
	}
	rawOptions, err := json.Marshal(options)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("marshal goal review options: %w", err)
	}

	createdAt := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("begin goal review creation tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	open, err := q.CountOpenDecisionsForGoal(ctx, goalID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("count open decisions for goal review: %w", err)
	}
	if open > 0 {
		return domain.Decision{}, fmt.Errorf("%w: %d", ErrGoalHasOpenDecision, goalID)
	}
	updated, err := q.UpdateGoalCompletionReport(ctx, sqlcgen.UpdateGoalCompletionReportParams{
		ResultSummary: report.WorkDone,
		WorkDone:      report.WorkDone,
		NowPossible:   report.NowPossible,
		HowToVerify:   report.HowToVerify,
		Surprises:     report.Surprises,
		NeedsReview:   report.NeedsReview,
		NextSteps:     report.NextSteps,
		UpdatedAt:     createdAt.Format(time.RFC3339),
		ID:            goalID,
	})
	if err != nil {
		return domain.Decision{}, fmt.Errorf("set goal review report: %w", err)
	}
	if affected, err := updated.RowsAffected(); err != nil {
		return domain.Decision{}, fmt.Errorf("set goal review report rows affected: %w", err)
	} else if affected != 1 {
		return domain.Decision{}, goalNotActiveForCompletionError(goalID, goal.Status)
	}

	decisionID, err := q.CreateDecision(ctx, sqlcgen.CreateDecisionParams{
		GoalID:         goalID,
		TaskID:         sql.NullInt64{},
		Kind:           string(domain.KindGoalReview),
		Question:       "Approve this goal review?",
		Options:        string(rawOptions),
		Status:         string(domain.DecisionOpen),
		DefaultOption:  "",
		DefaultAfterMs: sql.NullInt64{},
		AgentSessionID: agentSessionID,
		CreatedAt:      createdAt.Format(time.RFC3339),
	})
	if err != nil {
		return domain.Decision{}, fmt.Errorf("insert goal review decision: %w", err)
	}

	row, err := q.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("get created goal review decision: %w", err)
	}
	decision, err := decisionFromRow(decisionRowFromSQLC(row))
	if err != nil {
		return domain.Decision{}, err
	}
	event := DecisionEvent{Name: "decision.created", Data: decision, OccurredAt: decision.CreatedAt}
	if err := tx.Commit(); err != nil {
		return domain.Decision{}, fmt.Errorf("commit goal review creation: %w", err)
	}
	s.notify.publish(decision.ID)
	s.notify.publishAll()
	s.publishWorkflowEvents([]DecisionEvent{event})
	return decision, nil
}

// ApproveGoalReview applies the human approval while leaving the goal active.
// The commander must perform the later finalization separately.
func (s *Store) ApproveGoalReview(ctx context.Context, decisionID int64) (domain.Goal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin goal review approval tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)

	goalID, err := q.GetOpenGoalReviewGoalID(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
	} else if err != nil {
		return domain.Goal{}, fmt.Errorf("lookup goal review: %w", err)
	}

	status, err := q.GetGoalStatus(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("lookup goal for review approval: %w", err)
	}
	if domain.GoalStatus(status) != domain.GoalActive {
		return domain.Goal{}, goalNotActiveForCompletionError(goalID, domain.GoalStatus(status))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := q.ApproveGoalReviewDecision(ctx, sqlcgen.ApproveGoalReviewDecisionParams{
		AnsweredAt: sql.NullString{String: now, Valid: true}, AppliedAt: sql.NullString{String: now, Valid: true}, ID: decisionID,
	})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("approve goal review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return domain.Goal{}, fmt.Errorf("approve goal review rows affected: %w", err)
	} else if affected != 1 {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
	}
	row, err := q.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("get approved goal review decision: %w", err)
	}
	event, err := workflowDecisionEvent("decision.approved", row)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("build approved goal review event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit goal review approval: %w", err)
	}

	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.publishWorkflowEvents([]DecisionEvent{event})
	return s.GetGoal(ctx, goalID)
}

// RejectGoalReview records the human rejection and leaves both the goal and
// the completed handoff unchanged. A commander explicitly requests a new
// handoff when the work should resume.
func (s *Store) RejectGoalReview(ctx context.Context, decisionID int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin goal review rejection tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := q.RejectGoalReviewDecision(ctx, sqlcgen.RejectGoalReviewDecisionParams{
		AnswerText: reason, AnsweredAt: sql.NullString{String: now, Valid: true}, ID: decisionID,
	})
	if err != nil {
		return fmt.Errorf("reject goal review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("reject goal review rows affected: %w", err)
	} else if affected != 1 {
		return fmt.Errorf("%w: %d", ErrDecisionNotOpen, decisionID)
	}
	row, err := q.GetDecision(ctx, decisionID)
	if err != nil {
		return fmt.Errorf("get rejected goal review decision: %w", err)
	}
	event, err := workflowDecisionEvent("decision.rejected", row)
	if err != nil {
		return fmt.Errorf("build rejected goal review event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit goal review rejection: %w", err)
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.publishWorkflowEvents([]DecisionEvent{event})
	return nil
}

func completionReportFromGoal(goal domain.Goal) domain.CompletionReport {
	return domain.CompletionReport{
		WorkDone:    goal.WorkDone,
		NowPossible: goal.NowPossible,
		HowToVerify: goal.HowToVerify,
		Surprises:   goal.Surprises,
		NeedsReview: goal.NeedsReview,
		NextSteps:   goal.NextSteps,
	}
}

// FinalizeGoalReview is the commander-only final step after a human goal
// review has been approved. It closes the active goal using the report already
// stored when the review was requested and never accepts a replacement report.
func (s *Store) FinalizeGoalReview(ctx context.Context, goalID, _ int64) (domain.Goal, error) {
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("get goal for review finalization: %w", err)
	}
	if goal.Status != domain.GoalActive {
		return domain.Goal{}, goalNotActiveForCompletionError(goalID, goal.Status)
	}
	if err := validateCompletionReport(completionReportFromGoal(goal)); err != nil {
		return domain.Goal{}, fmt.Errorf("validate stored goal review report: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin goal review finalization tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)

	open, err := q.CountOpenDecisionsForGoal(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("count open decisions for review finalization: %w", err)
	}
	if open > 0 {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrGoalHasOpenDecision, goalID)
	}
	approved, err := q.CountApprovedGoalReviewsForGoal(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("check approved goal review: %w", err)
	}
	if approved == 0 {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrGoalReviewNotApproved, goalID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := q.FinalizeGoalReview(ctx, sqlcgen.FinalizeGoalReviewParams{UpdatedAt: now, ID: goalID})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("finalize goal review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return domain.Goal{}, fmt.Errorf("finalize goal review rows affected: %w", err)
	} else if affected != 1 {
		return domain.Goal{}, goalNotActiveForCompletionError(goalID, goal.Status)
	}
	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit goal review finalization: %w", err)
	}
	return s.GetGoal(ctx, goalID)
}

// FinalizeGoalWithReport is retained for source compatibility with callers
// that have not adopted the no-input goal-review finalizer. This remains the
// legacy report-bearing completion path; named no-input finalization uses
// FinalizeGoalReview.
func (s *Store) FinalizeGoalWithReport(ctx context.Context, goalID int64, report domain.CompletionReport, _ int64) (domain.Goal, error) {
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("get goal for finalization: %w", err)
	}
	if goal.Status != domain.GoalActive {
		return domain.Goal{}, goalNotActiveForCompletionError(goalID, goal.Status)
	}
	if err := validateCompletionReport(report); err != nil {
		return domain.Goal{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("begin goal finalization tx: %w", err)
	}
	defer tx.Rollback()

	open, err := sqlcgen.New(tx).CountOpenDecisionsForGoal(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("count open decisions for finalization: %w", err)
	}
	if open > 0 {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrGoalHasOpenDecision, goalID)
	}
	approved, err := sqlcgen.New(tx).CountApprovedGoalReviewsForGoal(ctx, goalID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("check approved goal review: %w", err)
	}
	if approved == 0 {
		return domain.Goal{}, fmt.Errorf("%w: %d", ErrGoalReviewNotApproved, goalID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := sqlcgen.New(tx).FinalizeGoal(ctx, sqlcgen.FinalizeGoalParams{
		ResultSummary: report.WorkDone, WorkDone: report.WorkDone, NowPossible: report.NowPossible,
		HowToVerify: report.HowToVerify, Surprises: report.Surprises, NeedsReview: report.NeedsReview,
		NextSteps: report.NextSteps, UpdatedAt: now, ID: goalID,
	})
	if err != nil {
		return domain.Goal{}, fmt.Errorf("finalize goal: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return domain.Goal{}, fmt.Errorf("finalize goal rows affected: %w", err)
	} else if affected != 1 {
		return domain.Goal{}, goalNotActiveForCompletionError(goalID, goal.Status)
	}
	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit goal finalization: %w", err)
	}
	return s.GetGoal(ctx, goalID)
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
	row, err := q.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("get approved completion decision: %w", err)
	}
	event, err := workflowDecisionEvent("decision.approved", row)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("build approved completion event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit: %w", err)
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.publishWorkflowEvents([]DecisionEvent{event})
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
	decisionEvent, err := workflowDecisionEvent("decision.rejected", d)
	if err != nil {
		return fmt.Errorf("build completion rejection event: %w", err)
	}
	events := []DecisionEvent{decisionEvent}
	if d.Kind == "completion" && d.AgentSessionID != 0 {
		projectID, err := q.GetGoalProjectID(ctx, d.GoalID)
		if err != nil {
			return fmt.Errorf("find project for completion rejection: %w", err)
		}
		project, err := q.GetProject(ctx, projectID)
		if err != nil {
			return fmt.Errorf("find project claim for completion rejection: %w", err)
		}
		commanderSubmitted := project.ClaimedBy != 0 && project.ClaimedBy == d.AgentSessionID
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
			if handoff.ReceivedAt == nil || handoff.ReceivedBy == 0 {
				continue
			}
			// A commander submits the completion decision, but a delegated
			// subcommander owns the handoff that must resume after rejection.
			// A non-commander reporter cannot reopen another session's work.
			if handoff.ReceivedBy != d.AgentSessionID && !commanderSubmitted {
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
			projectID, err := q.GetGoalProjectID(ctx, selected.GoalID)
			if err != nil {
				return fmt.Errorf("find project for reopened goal handoff: %w", err)
			}
			requestEvent := DecisionEvent{
				Name: EventGoalHandoffRequest,
				Data: HandoffEvent{ProjectID: projectID, GoalID: selected.GoalID, HandoffID: reopenID, RequestedBy: selected.RequestedBy, RequestReport: requestReport},
			}
			events = append(events, requestEvent)
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
			receiveEvent := DecisionEvent{
				Name: EventGoalHandoffReceive,
				Data: HandoffEvent{ProjectID: projectID, GoalID: selected.GoalID, HandoffID: reopenID, ReceivedBy: selected.ReceivedBy},
			}
			events = append(events, receiveEvent)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit completion rejection: %w", err)
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.publishWorkflowEvents(events)
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
	row, err := q.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("get approved goal decision: %w", err)
	}
	event, err := workflowDecisionEvent("decision.approved", row)
	if err != nil {
		return domain.Goal{}, fmt.Errorf("build approved goal event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.Goal{}, fmt.Errorf("commit goal approval: %w", err)
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.publishWorkflowEvents([]DecisionEvent{event})
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
	row, err := q.GetDecision(ctx, decisionID)
	if err != nil {
		return fmt.Errorf("get rejected goal decision: %w", err)
	}
	event, err := workflowDecisionEvent("decision.rejected", row)
	if err != nil {
		return fmt.Errorf("build rejected goal event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit goal rejection: %w", err)
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.publishWorkflowEvents([]DecisionEvent{event})
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
	projectID, err := q.GetGoalProjectID(ctx, goalID)
	if err != nil {
		return fmt.Errorf("lookup project for goal withdrawal: %w", err)
	}
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
	withdrawnEvents := make([]DecisionEvent, 0, len(openDecisions)+1)
	goalEvent := DecisionEvent{
		Name: EventGoalWithdrawn,
		Data: GoalWithdrawnEvent{
			GoalID: goalID, ProjectID: projectID, Reason: reason,
			DroppedTaskIDs: droppedTaskIDs, ClosedTaskHandoffIDs: closedHandoffIDs,
			WithdrawnDecisionIDs: withdrawnDecisionIDs,
		},
	}
	withdrawnEvents = append(withdrawnEvents, goalEvent)
	for _, decision := range openDecisions {
		row, err := q.GetDecision(ctx, decision.ID)
		if err != nil {
			return fmt.Errorf("get withdrawn decision %d: %w", decision.ID, err)
		}
		event, err := workflowDecisionEvent("decision.withdrawn", row)
		if err != nil {
			return fmt.Errorf("build withdrawn decision %d event: %w", decision.ID, err)
		}
		withdrawnEvents = append(withdrawnEvents, event)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit goal withdrawal: %w", err)
	}

	for _, decision := range openDecisions {
		s.notify.publish(decision.ID)
	}
	s.publishWorkflowEvents(withdrawnEvents)
	s.notify.publishAll()
	return nil
}
