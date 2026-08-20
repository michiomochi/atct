package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var (
	ErrDecisionNotFound       = errors.New("decision not found")
	ErrDecisionNotOpen        = errors.New("decision is not open")
	ErrInvalidDecisionDefault = errors.New("invalid decision default")
	ErrTaskHasOpenDecision    = errors.New("task has an open decision")
	ErrGoalHasOpenDecision    = errors.New("goal has an open decision")
)

type AskInput struct {
	GoalID         string
	TaskID         string
	Kind           domain.DecisionKind
	Question       string
	Options        []domain.Option
	DefaultOption  string
	DefaultAfterMs *int64
	AgentSessionID string
}

func (s *Store) AskDecision(ctx context.Context, in AskInput) (domain.Decision, error) {
	if err := validateDecisionDefault(in.Options, in.DefaultOption, in.DefaultAfterMs); err != nil {
		return domain.Decision{}, err
	}
	if in.Options == nil {
		in.Options = []domain.Option{}
	}
	raw, err := json.Marshal(in.Options)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("marshal options: %w", err)
	}

	d := domain.Decision{
		ID:             uuid.NewString(),
		GoalID:         in.GoalID,
		TaskID:         in.TaskID,
		Kind:           in.Kind,
		Question:       in.Question,
		Options:        in.Options,
		DefaultOption:  in.DefaultOption,
		Status:         domain.DecisionOpen,
		AgentSessionID: in.AgentSessionID,
		CreatedAt:      time.Now().UTC(),
	}
	if in.DefaultAfterMs != nil {
		after := *in.DefaultAfterMs
		d.DefaultAfterMs = &after
	}

	q := decisionQueries(s)
	params := sqlcgen.CreateDecisionParams{
		ID:             d.ID,
		GoalID:         d.GoalID,
		TaskID:         sql.NullString{String: in.TaskID, Valid: in.TaskID != ""},
		Kind:           string(d.Kind),
		Question:       d.Question,
		Options:        string(raw),
		Status:         string(d.Status),
		DefaultOption:  d.DefaultOption,
		AgentSessionID: d.AgentSessionID,
		CreatedAt:      d.CreatedAt.Format(time.RFC3339),
	}
	if in.DefaultAfterMs != nil {
		params.DefaultAfterMs = sql.NullInt64{Int64: *in.DefaultAfterMs, Valid: true}
	}
	if err = q.CreateDecision(ctx, params); err != nil {
		return domain.Decision{}, fmt.Errorf("insert decision: %w", err)
	}
	s.notify.publish(d.ID)
	s.notify.publishAll()
	s.notify.publishEvent(Event{Name: "decision.created", Data: d})
	return d, nil
}

func validateDecisionDefault(options []domain.Option, defaultOption string, defaultAfterMs *int64) error {
	if defaultOption == "" && defaultAfterMs == nil {
		return nil
	}
	if defaultOption == "" {
		return fmt.Errorf("%w: default_option is required", ErrInvalidDecisionDefault)
	}
	if defaultAfterMs != nil && *defaultAfterMs < 0 {
		return fmt.Errorf("%w: default_after_ms must not be negative", ErrInvalidDecisionDefault)
	}
	for _, option := range options {
		if option.Label == defaultOption {
			return nil
		}
	}
	return fmt.Errorf("%w: default_option %q is not one of the option labels", ErrInvalidDecisionDefault, defaultOption)
}

type decisionRow struct {
	ID               string
	GoalID           string
	TaskID           string
	Kind             string
	Question         string
	Options          string
	Status           string
	DefaultOption    string
	DefaultAfterMs   sql.NullInt64
	DefaultAppliedAt sql.NullString
	AnswerLabel      string
	AnswerText       string
	AnsweredAt       sql.NullString
	AppliedAt        sql.NullString
	AgentSessionID   string
	CreatedAt        string
}

func decisionFromRow(row decisionRow) (domain.Decision, error) {
	d := domain.Decision{
		ID:             row.ID,
		GoalID:         row.GoalID,
		TaskID:         row.TaskID,
		Kind:           domain.DecisionKind(row.Kind),
		Question:       row.Question,
		Status:         domain.DecisionStatus(row.Status),
		DefaultOption:  row.DefaultOption,
		AnswerLabel:    row.AnswerLabel,
		AnswerText:     row.AnswerText,
		AgentSessionID: row.AgentSessionID,
		CreatedAt:      time.Time{},
	}
	if row.DefaultAfterMs.Valid {
		after := row.DefaultAfterMs.Int64
		d.DefaultAfterMs = &after
	}
	if err := json.Unmarshal([]byte(row.Options), &d.Options); err != nil {
		return domain.Decision{}, fmt.Errorf("unmarshal options: %w", err)
	}
	var err error
	if d.CreatedAt, err = time.Parse(time.RFC3339, row.CreatedAt); err != nil {
		return domain.Decision{}, fmt.Errorf("parse created_at: %w", err)
	}
	if row.AnsweredAt.Valid {
		t, err := time.Parse(time.RFC3339, row.AnsweredAt.String)
		if err != nil {
			return domain.Decision{}, fmt.Errorf("parse answered_at: %w", err)
		}
		d.AnsweredAt = &t
	}
	if row.AppliedAt.Valid {
		t, err := time.Parse(time.RFC3339, row.AppliedAt.String)
		if err != nil {
			return domain.Decision{}, fmt.Errorf("parse applied_at: %w", err)
		}
		d.AppliedAt = &t
	}
	if row.DefaultAppliedAt.Valid {
		t, err := time.Parse(time.RFC3339, row.DefaultAppliedAt.String)
		if err != nil {
			return domain.Decision{}, fmt.Errorf("parse default_applied_at: %w", err)
		}
		d.DefaultAppliedAt = &t
	}
	return d, nil
}

func convertDecisionRows[T any](rows []T, convert func(T) decisionRow) ([]domain.Decision, error) {
	var out []domain.Decision
	for _, row := range rows {
		d, err := decisionFromRow(convert(row))
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func decisionQueries(s *Store) *sqlcgen.Queries {
	return sqlcgen.New(s.db)
}

func (s *Store) GetDecision(ctx context.Context, id string) (domain.Decision, error) {
	row, err := decisionQueries(s).GetDecision(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Decision{}, fmt.Errorf("%w: %s", ErrDecisionNotFound, id)
	}
	if err != nil {
		return domain.Decision{}, err
	}
	return decisionFromRow(decisionRow{
		ID:               row.ID,
		GoalID:           row.GoalID,
		TaskID:           row.TaskID,
		Kind:             row.Kind,
		Question:         row.Question,
		Options:          row.Options,
		Status:           row.Status,
		DefaultOption:    row.DefaultOption,
		DefaultAfterMs:   row.DefaultAfterMs,
		DefaultAppliedAt: row.DefaultAppliedAt,
		AnswerLabel:      row.AnswerLabel,
		AnswerText:       row.AnswerText,
		AnsweredAt:       row.AnsweredAt,
		AppliedAt:        row.AppliedAt,
		AgentSessionID:   row.AgentSessionID,
		CreatedAt:        row.CreatedAt,
	})
}

func (s *Store) ListOpenDecisions(ctx context.Context, goalID string) ([]domain.Decision, error) {
	rows, err := decisionQueries(s).ListOpenDecisions(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("query open decisions: %w", err)
	}
	return convertDecisionRows(rows, func(row sqlcgen.ListOpenDecisionsRow) decisionRow {
		return decisionRow{
			ID:               row.ID,
			GoalID:           row.GoalID,
			TaskID:           row.TaskID,
			Kind:             row.Kind,
			Question:         row.Question,
			Options:          row.Options,
			Status:           row.Status,
			DefaultOption:    row.DefaultOption,
			DefaultAfterMs:   row.DefaultAfterMs,
			DefaultAppliedAt: row.DefaultAppliedAt,
			AnswerLabel:      row.AnswerLabel,
			AnswerText:       row.AnswerText,
			AnsweredAt:       row.AnsweredAt,
			AppliedAt:        row.AppliedAt,
			AgentSessionID:   row.AgentSessionID,
			CreatedAt:        row.CreatedAt,
		}
	})
}

func (s *Store) ListAllOpenDecisions(ctx context.Context) ([]domain.Decision, error) {
	rows, err := decisionQueries(s).ListAllOpenDecisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("query all open decisions: %w", err)
	}
	return convertDecisionRows(rows, func(row sqlcgen.ListAllOpenDecisionsRow) decisionRow {
		return decisionRow{
			ID:               row.ID,
			GoalID:           row.GoalID,
			TaskID:           row.TaskID,
			Kind:             row.Kind,
			Question:         row.Question,
			Options:          row.Options,
			Status:           row.Status,
			DefaultOption:    row.DefaultOption,
			DefaultAfterMs:   row.DefaultAfterMs,
			DefaultAppliedAt: row.DefaultAppliedAt,
			AnswerLabel:      row.AnswerLabel,
			AnswerText:       row.AnswerText,
			AnsweredAt:       row.AnsweredAt,
			AppliedAt:        row.AppliedAt,
			AgentSessionID:   row.AgentSessionID,
			CreatedAt:        row.CreatedAt,
		}
	})
}

type AnswerInput struct {
	DecisionID  string
	AnswerLabel string
	AnswerText  string
}

// AnswerDecision performs a conditional transition from open.
// WHERE status = 'open' ensures only one concurrent answer succeeds.
// The application must enforce this at the database boundary because humans can answer twice in separate tabs.
func (s *Store) AnswerDecision(ctx context.Context, in AnswerInput) (domain.Decision, error) {
	return s.answerDecision(ctx, in, "decision.answered")
}

func (s *Store) answerDecision(ctx context.Context, in AnswerInput, eventName string) (domain.Decision, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := decisionQueries(s).AnswerDecision(ctx, sqlcgen.AnswerDecisionParams{
		AnswerLabel: in.AnswerLabel,
		AnswerText:  in.AnswerText,
		AnsweredAt:  sql.NullString{String: now, Valid: true},
		ID:          in.DecisionID,
	})
	if err != nil {
		return domain.Decision{}, fmt.Errorf("update decision: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Decision{}, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return domain.Decision{}, fmt.Errorf("%w: %s", ErrDecisionNotOpen, in.DecisionID)
	}
	d, err := s.GetDecision(ctx, in.DecisionID)
	if err != nil {
		return domain.Decision{}, err
	}
	s.notify.publish(in.DecisionID)
	s.notify.publishAll()
	s.notify.publishEvent(Event{Name: eventName, Data: d})
	return d, nil
}

// ApplyExpiredDefaults settles open decisions whose default timeout has elapsed.
// The selection and conditional update happen in one transaction so a human answer
// that wins the open-to-answered transition cannot be overwritten by a default.
func (s *Store) ApplyExpiredDefaults(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := decisionQueries(s).WithTx(tx)
	rows, err := q.ListExpiredDecisions(ctx)
	if err != nil {
		return 0, fmt.Errorf("query expired decisions: %w", err)
	}

	allCandidates, err := convertDecisionRows(rows, func(row sqlcgen.ListExpiredDecisionsRow) decisionRow {
		return decisionRow{
			ID:               row.ID,
			GoalID:           row.GoalID,
			TaskID:           row.TaskID,
			Kind:             row.Kind,
			Question:         row.Question,
			Options:          row.Options,
			Status:           row.Status,
			DefaultOption:    row.DefaultOption,
			DefaultAfterMs:   row.DefaultAfterMs,
			DefaultAppliedAt: row.DefaultAppliedAt,
			AnswerLabel:      row.AnswerLabel,
			AnswerText:       row.AnswerText,
			AnsweredAt:       row.AnsweredAt,
			AppliedAt:        row.AppliedAt,
			AgentSessionID:   row.AgentSessionID,
			CreatedAt:        row.CreatedAt,
		}
	})
	if err != nil {
		return 0, err
	}

	var candidates []domain.Decision
	for _, d := range allCandidates {
		if d.DefaultAfterMs != nil && !now.Before(d.CreatedAt.Add(time.Duration(*d.DefaultAfterMs)*time.Millisecond)) {
			candidates = append(candidates, d)
		}
	}

	settledAt := now.UTC()
	settledAtText := settledAt.Format(time.RFC3339)
	var settledDecisions []domain.Decision
	for i := range candidates {
		result, err := q.ApplyDecisionDefault(ctx, sqlcgen.ApplyDecisionDefaultParams{
			AnswerLabel:      candidates[i].DefaultOption,
			AnsweredAt:       sql.NullString{String: settledAtText, Valid: true},
			DefaultAppliedAt: sql.NullString{String: settledAtText, Valid: true},
			ID:               candidates[i].ID,
		})
		if err != nil {
			return 0, fmt.Errorf("apply default: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("default rows affected: %w", err)
		}
		if updated == 0 {
			continue
		}
		candidates[i].Status = domain.DecisionAnswered
		candidates[i].AnswerLabel = candidates[i].DefaultOption
		answeredAt := settledAt
		candidates[i].AnsweredAt = &answeredAt
		defaultAppliedAt := settledAt
		candidates[i].DefaultAppliedAt = &defaultAppliedAt
		settledDecisions = append(settledDecisions, candidates[i])
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	for _, d := range settledDecisions {
		s.notify.publish(d.ID)
		s.notify.publishEvent(Event{Name: "decision.answered", Data: d})
	}
	if len(settledDecisions) > 0 {
		s.notify.publishAll()
	}
	return len(settledDecisions), nil
}

func (s *Store) WithdrawDecision(ctx context.Context, decisionID, reason string) error {
	res, err := decisionQueries(s).WithdrawDecision(ctx, sqlcgen.WithdrawDecisionParams{
		AnswerText: reason,
		ID:         decisionID,
	})
	if err != nil {
		return fmt.Errorf("withdraw decision: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrDecisionNotOpen, decisionID)
	}
	d, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return err
	}
	s.notify.publish(decisionID)
	s.notify.publishAll()
	s.notify.publishEvent(Event{Name: "decision.withdrawn", Data: d})
	return nil
}

// PollDecisions returns answered Decisions and transitions them to applied in one transaction.
// "Human answered" and "agent received" are distinct facts.
// A decision becomes applied when returned; if the process dies before return, it remains answered and the next poll can recover it.
func (s *Store) PollDecisions(ctx context.Context, agentSessionID string, decisionID string) ([]domain.Decision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := decisionQueries(s).WithTx(tx)
	var out []domain.Decision
	var conversionErr error
	if decisionID == "" {
		rows, queryErr := q.ListAnsweredDecisionsForAgentSession(ctx, agentSessionID)
		if queryErr != nil {
			return nil, fmt.Errorf("query answered decisions: %w", queryErr)
		}
		out, conversionErr = convertDecisionRows(rows, func(row sqlcgen.ListAnsweredDecisionsForAgentSessionRow) decisionRow {
			return decisionRow{
				ID:               row.ID,
				GoalID:           row.GoalID,
				TaskID:           row.TaskID,
				Kind:             row.Kind,
				Question:         row.Question,
				Options:          row.Options,
				Status:           row.Status,
				DefaultOption:    row.DefaultOption,
				DefaultAfterMs:   row.DefaultAfterMs,
				DefaultAppliedAt: row.DefaultAppliedAt,
				AnswerLabel:      row.AnswerLabel,
				AnswerText:       row.AnswerText,
				AnsweredAt:       row.AnsweredAt,
				AppliedAt:        row.AppliedAt,
				AgentSessionID:   row.AgentSessionID,
				CreatedAt:        row.CreatedAt,
			}
		})
	} else {
		rows, queryErr := q.ListAnsweredDecisionForID(ctx, decisionID)
		if queryErr != nil {
			return nil, fmt.Errorf("query answered decisions: %w", queryErr)
		}
		out, conversionErr = convertDecisionRows(rows, func(row sqlcgen.ListAnsweredDecisionForIDRow) decisionRow {
			return decisionRow{
				ID:               row.ID,
				GoalID:           row.GoalID,
				TaskID:           row.TaskID,
				Kind:             row.Kind,
				Question:         row.Question,
				Options:          row.Options,
				Status:           row.Status,
				DefaultOption:    row.DefaultOption,
				DefaultAfterMs:   row.DefaultAfterMs,
				DefaultAppliedAt: row.DefaultAppliedAt,
				AnswerLabel:      row.AnswerLabel,
				AnswerText:       row.AnswerText,
				AnsweredAt:       row.AnsweredAt,
				AppliedAt:        row.AppliedAt,
				AgentSessionID:   row.AgentSessionID,
				CreatedAt:        row.CreatedAt,
			}
		})
	}
	if conversionErr != nil {
		return nil, conversionErr
	}

	now := time.Now().UTC()
	for i := range out {
		if err := q.MarkDecisionApplied(ctx, sqlcgen.MarkDecisionAppliedParams{
			AppliedAt: sql.NullString{String: now.Format(time.RFC3339), Valid: true},
			ID:        out[i].ID,
		}); err != nil {
			return nil, fmt.Errorf("mark applied: %w", err)
		}
		out[i].Status = domain.DecisionApplied
		applied := now
		out[i].AppliedAt = &applied
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	for _, d := range out {
		s.notify.publish(d.ID)
		s.notify.publishEvent(Event{Name: "decision.applied", Data: d})
	}
	if len(out) > 0 {
		s.notify.publishAll()
	}
	return out, nil
}

// ListUnappliedDecisions returns answered Decisions not yet received by an agent.
// This is the only place to detect an answer stranded after a dead session.
func (s *Store) ListUnappliedDecisions(ctx context.Context) ([]domain.Decision, error) {
	rows, err := decisionQueries(s).ListUnappliedDecisions(ctx)
	if err != nil {
		return nil, fmt.Errorf("query unapplied decisions: %w", err)
	}
	return convertDecisionRows(rows, func(row sqlcgen.ListUnappliedDecisionsRow) decisionRow {
		return decisionRow{
			ID:               row.ID,
			GoalID:           row.GoalID,
			TaskID:           row.TaskID,
			Kind:             row.Kind,
			Question:         row.Question,
			Options:          row.Options,
			Status:           row.Status,
			DefaultOption:    row.DefaultOption,
			DefaultAfterMs:   row.DefaultAfterMs,
			DefaultAppliedAt: row.DefaultAppliedAt,
			AnswerLabel:      row.AnswerLabel,
			AnswerText:       row.AnswerText,
			AnsweredAt:       row.AnsweredAt,
			AppliedAt:        row.AppliedAt,
			AgentSessionID:   row.AgentSessionID,
			CreatedAt:        row.CreatedAt,
		}
	})
}

// ListUnappliedDecisionsForProject returns answered Decisions not yet received by an agent
// for Goals belonging to the given project.
func (s *Store) ListUnappliedDecisionsForProject(ctx context.Context, projectID string) ([]domain.Decision, error) {
	rows, err := decisionQueries(s).ListUnappliedDecisionsForProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("query project unapplied decisions: %w", err)
	}
	return convertDecisionRows(rows, func(row sqlcgen.ListUnappliedDecisionsForProjectRow) decisionRow {
		return decisionRow{
			ID:               row.ID,
			GoalID:           row.GoalID,
			TaskID:           row.TaskID,
			Kind:             row.Kind,
			Question:         row.Question,
			Options:          row.Options,
			Status:           row.Status,
			DefaultOption:    row.DefaultOption,
			DefaultAfterMs:   row.DefaultAfterMs,
			DefaultAppliedAt: row.DefaultAppliedAt,
			AnswerLabel:      row.AnswerLabel,
			AnswerText:       row.AnswerText,
			AnsweredAt:       row.AnsweredAt,
			AppliedAt:        row.AppliedAt,
			AgentSessionID:   row.AgentSessionID,
			CreatedAt:        row.CreatedAt,
		}
	})
}

// ListAppliedDecisions returns the most recent applied Decisions for a goal
// and the exact number omitted by the history limit.
func (s *Store) ListAppliedDecisions(ctx context.Context, goalID string) ([]domain.Decision, int, error) {
	totalCount, err := decisionQueries(s).CountAppliedDecisions(ctx, goalID)
	if err != nil {
		return nil, 0, fmt.Errorf("count applied decisions: %w", err)
	}
	total := int(totalCount)

	rows, err := decisionQueries(s).ListAppliedDecisions(ctx, goalID)
	if err != nil {
		return nil, 0, fmt.Errorf("query applied decisions: %w", err)
	}

	limit := total
	if limit > 20 {
		limit = 20
	}
	out, err := convertDecisionRows(rows, func(row sqlcgen.ListAppliedDecisionsRow) decisionRow {
		return decisionRow{
			ID:               row.ID,
			GoalID:           row.GoalID,
			TaskID:           row.TaskID,
			Kind:             row.Kind,
			Question:         row.Question,
			Options:          row.Options,
			Status:           row.Status,
			DefaultOption:    row.DefaultOption,
			DefaultAfterMs:   row.DefaultAfterMs,
			DefaultAppliedAt: row.DefaultAppliedAt,
			AnswerLabel:      row.AnswerLabel,
			AnswerText:       row.AnswerText,
			AnsweredAt:       row.AnsweredAt,
			AppliedAt:        row.AppliedAt,
			AgentSessionID:   row.AgentSessionID,
			CreatedAt:        row.CreatedAt,
		}
	})
	if err != nil {
		return nil, 0, err
	}
	if out == nil {
		out = make([]domain.Decision, 0, limit)
	}

	omitted := total - len(out)
	if omitted < 0 {
		omitted = 0
	}
	return out, omitted, nil
}

// ListAppliedDecisionsForTask returns the most recent applied Decisions for a task
// and the exact number omitted by the history limit.
func (s *Store) ListAppliedDecisionsForTask(ctx context.Context, goalID, taskID string) ([]domain.Decision, int, error) {
	totalCount, err := decisionQueries(s).CountAppliedDecisionsForTask(ctx, sqlcgen.CountAppliedDecisionsForTaskParams{
		GoalID: goalID,
		TaskID: sql.NullString{String: taskID, Valid: true},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count applied decisions for task: %w", err)
	}
	total := int(totalCount)

	rows, err := decisionQueries(s).ListAppliedDecisionsForTask(ctx, sqlcgen.ListAppliedDecisionsForTaskParams{
		GoalID: goalID,
		TaskID: sql.NullString{String: taskID, Valid: true},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("query applied decisions for task: %w", err)
	}

	limit := total
	if limit > 20 {
		limit = 20
	}
	out, err := convertDecisionRows(rows, func(row sqlcgen.ListAppliedDecisionsForTaskRow) decisionRow {
		return decisionRow{
			ID:               row.ID,
			GoalID:           row.GoalID,
			TaskID:           row.TaskID,
			Kind:             row.Kind,
			Question:         row.Question,
			Options:          row.Options,
			Status:           row.Status,
			DefaultOption:    row.DefaultOption,
			DefaultAfterMs:   row.DefaultAfterMs,
			DefaultAppliedAt: row.DefaultAppliedAt,
			AnswerLabel:      row.AnswerLabel,
			AnswerText:       row.AnswerText,
			AnsweredAt:       row.AnsweredAt,
			AppliedAt:        row.AppliedAt,
			AgentSessionID:   row.AgentSessionID,
			CreatedAt:        row.CreatedAt,
		}
	})
	if err != nil {
		return nil, 0, err
	}
	if out == nil {
		out = make([]domain.Decision, 0, limit)
	}

	omitted := total - len(out)
	if omitted < 0 {
		omitted = 0
	}
	return out, omitted, nil
}
