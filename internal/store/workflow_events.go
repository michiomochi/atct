package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	workflowEventDefaultLimit = 1000
	workflowEventMaxLimit     = 10000
)

// WorkflowEvent is the durable representation of a store event. Data is the
// original event payload, kept as JSON so callers can decode it according to
// Name without losing fields during reconciliation.
type WorkflowEvent struct {
	ID         string          `json:"id"`
	ProjectID  int64           `json:"project_id"`
	Sequence   int64           `json:"sequence"`
	Name       string          `json:"event_name"`
	GoalID     int64           `json:"goal_id,omitempty"`
	TaskID     int64           `json:"task_id,omitempty"`
	DecisionID int64           `json:"decision_id,omitempty"`
	HandoffID  string          `json:"handoff_id,omitempty"`
	Data       json.RawMessage `json:"data"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// WorkflowEventQuery scopes an outbox read to one project and optionally one
// goal or task. BeforeSequence is a high-watermark used to merge a backfill
// with the live subscription without allowing newly committed events into the
// backfill batch.
type WorkflowEventQuery struct {
	ProjectID      int64
	GoalID         int64
	TaskID         int64
	AfterSequence  int64
	BeforeSequence int64
	Limit          int
}

type WorkflowEventPage struct {
	Events          []WorkflowEvent `json:"events"`
	OldestSequence  int64           `json:"oldest_sequence"`
	CurrentSequence int64           `json:"current_sequence"`
	HighWatermark   int64           `json:"high_watermark"`
}

// WatchDeliveryCursor is the durable position of one watcher scope. GoalID
// is zero for a project-wide watcher.
type WatchDeliveryCursor struct {
	WatcherKey string    `json:"watcher_key"`
	ProjectID  int64     `json:"project_id"`
	GoalID     int64     `json:"goal_id,omitempty"`
	Sequence   int64     `json:"sequence"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// WorkflowReconciliation is a point-in-time canonical snapshot. It contains
// the scoped state needed after a watcher restarts or a live signal is missed.
type WorkflowReconciliation struct {
	Goals        []domain.Goal     `json:"goals"`
	Tasks        []domain.Task     `json:"tasks"`
	Decisions    []domain.Decision `json:"decisions"`
	GoalHandoffs []GoalHandoff     `json:"goal_handoffs"`
	PlanHandoffs []PlanHandoff     `json:"plan_handoffs"`
	TaskHandoffs []TaskHandoff     `json:"task_handoffs"`
}

func workflowEventShouldPersist(name string) bool {
	return strings.HasPrefix(name, "decision.") ||
		name == "goal.created" || name == EventGoalWithdrawn ||
		name == EventHandoffReported ||
		strings.HasPrefix(name, "task.handoff.") ||
		strings.HasPrefix(name, "goal.handoff.") ||
		strings.HasPrefix(name, "plan.handoff.")
}

type workflowEventMetadata struct {
	ProjectID  int64
	GoalID     int64
	TaskID     int64
	DecisionID int64
	HandoffID  string
}

func workflowEventMetadataFromData(data any) workflowEventMetadata {
	var metadata workflowEventMetadata
	switch value := data.(type) {
	case domain.Decision:
		metadata.GoalID = value.GoalID
		metadata.TaskID = value.TaskID
		metadata.DecisionID = value.ID
	case *domain.Decision:
		if value != nil {
			metadata.GoalID = value.GoalID
			metadata.TaskID = value.TaskID
			metadata.DecisionID = value.ID
		}
	case domain.Goal:
		metadata.ProjectID = value.ProjectID
		metadata.GoalID = value.ID
	case *domain.Goal:
		if value != nil {
			metadata.ProjectID = value.ProjectID
			metadata.GoalID = value.ID
		}
	case DetectionEvent:
		metadata.ProjectID = value.ProjectID
		metadata.GoalID = value.GoalID
		metadata.TaskID = value.TaskID
		metadata.DecisionID = value.DecisionID
		metadata.HandoffID = value.HandoffID
	case *DetectionEvent:
		if value != nil {
			metadata.ProjectID = value.ProjectID
			metadata.GoalID = value.GoalID
			metadata.TaskID = value.TaskID
			metadata.DecisionID = value.DecisionID
			metadata.HandoffID = value.HandoffID
		}
	case GoalWithdrawnEvent:
		metadata.ProjectID = value.ProjectID
		metadata.GoalID = value.GoalID
	case *GoalWithdrawnEvent:
		if value != nil {
			metadata.ProjectID = value.ProjectID
			metadata.GoalID = value.GoalID
		}
	case HandoffReviewEvent:
		metadata.ProjectID = value.ProjectID
		metadata.GoalID = value.GoalID
		metadata.TaskID = value.TaskID
		metadata.HandoffID = value.HandoffID
	case *HandoffReviewEvent:
		if value != nil {
			metadata.ProjectID = value.ProjectID
			metadata.GoalID = value.GoalID
			metadata.TaskID = value.TaskID
			metadata.HandoffID = value.HandoffID
		}
	case HandoffEvent:
		metadata.ProjectID = value.ProjectID
		metadata.GoalID = value.GoalID
		metadata.TaskID = value.TaskID
		metadata.HandoffID = value.HandoffID
	case *HandoffEvent:
		if value != nil {
			metadata.ProjectID = value.ProjectID
			metadata.GoalID = value.GoalID
			metadata.TaskID = value.TaskID
			metadata.HandoffID = value.HandoffID
		}
	}
	return metadata
}

func (s *Store) persistWorkflowEvent(ctx context.Context, tx *sql.Tx, event DecisionEvent, metadata workflowEventMetadata) (DecisionEvent, error) {
	if !workflowEventShouldPersist(event.Name) {
		return event, nil
	}
	dataMetadata := workflowEventMetadataFromData(event.Data)
	if metadata.ProjectID == 0 {
		metadata.ProjectID = dataMetadata.ProjectID
	}
	if metadata.GoalID == 0 {
		metadata.GoalID = dataMetadata.GoalID
	}
	if metadata.TaskID == 0 {
		metadata.TaskID = dataMetadata.TaskID
	}
	if metadata.DecisionID == 0 {
		metadata.DecisionID = dataMetadata.DecisionID
	}
	if metadata.HandoffID == "" {
		metadata.HandoffID = dataMetadata.HandoffID
	}
	if metadata.ProjectID == 0 && metadata.GoalID != 0 {
		projectID, err := sqlcgen.New(tx).GetGoalProjectID(ctx, metadata.GoalID)
		if err != nil {
			return event, fmt.Errorf("find project for workflow event: %w", err)
		}
		metadata.ProjectID = projectID
	}
	if metadata.TaskID != 0 {
		taskGoalID, err := sqlcgen.New(tx).GetTaskGoalID(ctx, metadata.TaskID)
		if err != nil {
			return event, fmt.Errorf("find task scope for workflow event: %w", err)
		}
		taskProjectID, err := sqlcgen.New(tx).GetGoalProjectID(ctx, taskGoalID)
		if err != nil {
			return event, fmt.Errorf("find task project scope for workflow event: %w", err)
		}
		if metadata.GoalID == 0 {
			metadata.GoalID = taskGoalID
		}
		if metadata.ProjectID == 0 {
			metadata.ProjectID = taskProjectID
		}
	}
	if metadata.ProjectID == 0 {
		return event, fmt.Errorf("workflow event %q has no project scope", event.Name)
	}
	switch value := event.Data.(type) {
	case HandoffEvent:
		value.ProjectID = metadata.ProjectID
		value.GoalID = metadata.GoalID
		event.Data = value
	case *HandoffEvent:
		if value != nil {
			value.ProjectID = metadata.ProjectID
			value.GoalID = metadata.GoalID
		}
	case HandoffReviewEvent:
		value.ProjectID = metadata.ProjectID
		value.GoalID = metadata.GoalID
		event.Data = value
	case *HandoffReviewEvent:
		if value != nil {
			value.ProjectID = metadata.ProjectID
			value.GoalID = metadata.GoalID
		}
	case DetectionEvent:
		value.ProjectID = metadata.ProjectID
		value.GoalID = metadata.GoalID
		event.Data = value
	case *DetectionEvent:
		if value != nil {
			value.ProjectID = metadata.ProjectID
			value.GoalID = metadata.GoalID
		}
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return event, fmt.Errorf("marshal workflow event %q: %w", event.Name, err)
	}
	sequence, err := sqlcgen.New(tx).AllocateProjectEventSequence(ctx, metadata.ProjectID)
	if err != nil {
		return event, fmt.Errorf("allocate project event sequence: %w", err)
	}
	eventID := fmt.Sprintf("%d:%d", metadata.ProjectID, sequence)
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	if err := sqlcgen.New(tx).InsertWorkflowEventOutbox(ctx, sqlcgen.InsertWorkflowEventOutboxParams{
		ProjectID:  metadata.ProjectID,
		Sequence:   sequence,
		EventID:    eventID,
		EventName:  event.Name,
		GoalID:     sql.NullInt64{Int64: metadata.GoalID, Valid: metadata.GoalID != 0},
		TaskID:     sql.NullInt64{Int64: metadata.TaskID, Valid: metadata.TaskID != 0},
		DecisionID: sql.NullInt64{Int64: metadata.DecisionID, Valid: metadata.DecisionID != 0},
		HandoffID:  sql.NullString{String: metadata.HandoffID, Valid: metadata.HandoffID != ""},
		Payload:    string(payload),
		OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return event, fmt.Errorf("insert workflow event outbox: %w", err)
	}
	if err := sqlcgen.New(tx).DeleteRetainedWorkflowEvents(ctx, sqlcgen.DeleteRetainedWorkflowEventsParams{
		ProjectID:  metadata.ProjectID,
		Column2:    sequence,
		OccurredAt: occurredAt.UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano),
	}); err != nil {
		return event, fmt.Errorf("prune workflow event outbox: %w", err)
	}
	event.EventID = eventID
	event.ID = eventID
	event.ProjectID = metadata.ProjectID
	event.Sequence = sequence
	event.OccurredAt = occurredAt
	return event, nil
}

func (s *Store) persistDecisionEvent(ctx context.Context, tx *sql.Tx, name string, row sqlcgen.Decision) (DecisionEvent, error) {
	decision, err := decisionFromRow(decisionRowFromSQLC(row))
	if err != nil {
		return DecisionEvent{}, err
	}
	return s.persistWorkflowEvent(ctx, tx, DecisionEvent{Name: name, Data: decision}, workflowEventMetadata{
		GoalID: decision.GoalID, TaskID: decision.TaskID, DecisionID: decision.ID,
	})
}

func (s *Store) publishWorkflowEvents(events []DecisionEvent) {
	for _, event := range events {
		if event.EventID != "" || event.ID != "" {
			s.notify.publishEvent(event)
		}
	}
}

func workflowEventFromRow(row sqlcgen.WorkflowEventOutbox) (WorkflowEvent, error) {
	occurredAt, err := time.Parse(time.RFC3339Nano, row.OccurredAt)
	if err != nil {
		return WorkflowEvent{}, fmt.Errorf("parse workflow event occurred_at: %w", err)
	}
	return WorkflowEvent{
		ID:         row.EventID,
		ProjectID:  row.ProjectID,
		Sequence:   row.Sequence,
		Name:       row.EventName,
		GoalID:     row.GoalID.Int64,
		TaskID:     row.TaskID.Int64,
		DecisionID: row.DecisionID.Int64,
		HandoffID:  row.HandoffID.String,
		Data:       json.RawMessage(row.Payload),
		OccurredAt: occurredAt,
	}, nil
}

func workflowInt64(value any) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	case []byte:
		return strconv.ParseInt(string(value), 10, 64)
	case string:
		return strconv.ParseInt(value, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected sqlite integer type %T", value)
	}
}

func (s *Store) workflowEventBounds(ctx context.Context, projectID int64) (int64, int64, error) {
	row, err := sqlcgen.New(s.db).GetWorkflowEventBounds(ctx, projectID)
	if err != nil {
		return 0, 0, fmt.Errorf("get workflow event bounds: %w", err)
	}
	oldest, err := workflowInt64(row.OldestSequence)
	if err != nil {
		return 0, 0, err
	}
	current, err := workflowInt64(row.CurrentSequence)
	if err != nil {
		return 0, 0, err
	}
	sequence, err := sqlcgen.New(s.db).GetProjectEventSequence(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return oldest, current, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("get current project event sequence: %w", err)
	}
	if sequence > current {
		current = sequence
	}
	return oldest, current, nil
}

func (s *Store) ListWorkflowEvents(ctx context.Context, query WorkflowEventQuery) (WorkflowEventPage, error) {
	if query.ProjectID <= 0 {
		return WorkflowEventPage{}, errors.New("workflow event project_id is required")
	}
	beforeSequence := query.BeforeSequence
	if beforeSequence == 0 {
		var err error
		beforeSequence, err = s.CurrentProjectEventSequence(ctx, query.ProjectID)
		if err != nil {
			return WorkflowEventPage{}, err
		}
	}
	limit := query.Limit
	if limit <= 0 {
		limit = workflowEventDefaultLimit
	}
	if limit > workflowEventMaxLimit {
		limit = workflowEventMaxLimit
	}
	rows, err := sqlcgen.New(s.db).ListWorkflowEvents(ctx, sqlcgen.ListWorkflowEventsParams{
		ProjectID:      query.ProjectID,
		AfterSequence:  query.AfterSequence,
		BeforeSequence: beforeSequence,
		GoalID:         query.GoalID,
		TaskID:         query.TaskID,
		EventLimit:     int64(limit),
	})
	if err != nil {
		return WorkflowEventPage{}, fmt.Errorf("list workflow events: %w", err)
	}
	events := make([]WorkflowEvent, 0, len(rows))
	for _, row := range rows {
		event, err := workflowEventFromRow(row)
		if err != nil {
			return WorkflowEventPage{}, err
		}
		events = append(events, event)
	}
	oldest, current, err := s.workflowEventBounds(ctx, query.ProjectID)
	if err != nil {
		return WorkflowEventPage{}, err
	}
	highWatermark := beforeSequence
	if highWatermark > current {
		highWatermark = current
	}
	return WorkflowEventPage{Events: events, OldestSequence: oldest, CurrentSequence: current, HighWatermark: highWatermark}, nil
}

// ListWorkflowEventOutbox is a descriptive alias for callers that want to
// emphasize that this is a durable read rather than the live event channel.
func (s *Store) ListWorkflowEventOutbox(ctx context.Context, query WorkflowEventQuery) (WorkflowEventPage, error) {
	return s.ListWorkflowEvents(ctx, query)
}

func (s *Store) CurrentProjectEventSequence(ctx context.Context, projectID int64) (int64, error) {
	sequence, err := sqlcgen.New(s.db).GetProjectEventSequence(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get current project event sequence: %w", err)
	}
	return sequence, nil
}

func (s *Store) GetWatchDeliveryCursor(ctx context.Context, watcherKey string, projectID, goalID int64) (WatchDeliveryCursor, error) {
	if strings.TrimSpace(watcherKey) == "" {
		return WatchDeliveryCursor{}, errors.New("watcher_key is required")
	}
	row, err := sqlcgen.New(s.db).GetWatchDeliveryCursor(ctx, sqlcgen.GetWatchDeliveryCursorParams{
		WatcherKey: watcherKey,
		ProjectID:  projectID,
		GoalID:     goalID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return WatchDeliveryCursor{WatcherKey: watcherKey, ProjectID: projectID, GoalID: goalID}, nil
	}
	if err != nil {
		return WatchDeliveryCursor{}, fmt.Errorf("get watch delivery cursor: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	if err != nil {
		return WatchDeliveryCursor{}, fmt.Errorf("parse watch delivery cursor updated_at: %w", err)
	}
	return WatchDeliveryCursor{WatcherKey: row.WatcherKey, ProjectID: row.ProjectID, GoalID: row.GoalID, Sequence: row.Sequence, UpdatedAt: updatedAt}, nil
}

func (s *Store) GetWatchCursor(ctx context.Context, watcherKey string, projectID, goalID int64) (WatchDeliveryCursor, error) {
	return s.GetWatchDeliveryCursor(ctx, watcherKey, projectID, goalID)
}

func (s *Store) AdvanceWatchDeliveryCursor(ctx context.Context, watcherKey string, projectID, goalID, sequence int64) error {
	if strings.TrimSpace(watcherKey) == "" {
		return errors.New("watcher_key is required")
	}
	if projectID <= 0 || goalID < 0 || sequence < 0 {
		return errors.New("invalid watch delivery cursor")
	}
	if sequence > 0 {
		current, err := s.CurrentProjectEventSequence(ctx, projectID)
		if err != nil {
			return err
		}
		if sequence > current {
			return fmt.Errorf("watch delivery cursor sequence %d is ahead of project sequence %d", sequence, current)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := sqlcgen.New(s.db).UpsertWatchDeliveryCursor(ctx, sqlcgen.UpsertWatchDeliveryCursorParams{
		WatcherKey: watcherKey,
		ProjectID:  projectID,
		GoalID:     goalID,
		Sequence:   sequence,
		UpdatedAt:  now,
	}); err != nil {
		return fmt.Errorf("advance watch delivery cursor: %w", err)
	}
	return nil
}

func (s *Store) AdvanceWatchCursor(ctx context.Context, watcherKey string, projectID, goalID, sequence int64) error {
	return s.AdvanceWatchDeliveryCursor(ctx, watcherKey, projectID, goalID, sequence)
}

func (s *Store) ReconcileWorkflow(ctx context.Context, query WorkflowEventQuery) (WorkflowReconciliation, error) {
	projectID := query.ProjectID
	if query.GoalID != 0 {
		goal, err := s.GetGoal(ctx, query.GoalID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		if projectID == 0 {
			projectID = goal.ProjectID
		} else if goal.ProjectID != projectID {
			return WorkflowReconciliation{}, fmt.Errorf("goal %d does not belong to project %d", query.GoalID, projectID)
		}
	}
	if projectID <= 0 {
		return WorkflowReconciliation{}, errors.New("workflow reconciliation project_id is required")
	}
	if query.TaskID != 0 {
		taskGoalID, err := sqlcgen.New(s.db).GetTaskGoalID(ctx, query.TaskID)
		if err != nil {
			return WorkflowReconciliation{}, fmt.Errorf("find task for workflow reconciliation: %w", err)
		}
		if query.GoalID == 0 {
			query.GoalID = taskGoalID
		} else if query.GoalID != taskGoalID {
			return WorkflowReconciliation{}, fmt.Errorf("task %d does not belong to goal %d", query.TaskID, query.GoalID)
		}
	}
	goals, err := s.ListGoals(ctx, projectID)
	if err != nil {
		return WorkflowReconciliation{}, err
	}
	if query.GoalID != 0 {
		filtered := goals[:0]
		for _, goal := range goals {
			if goal.ID == query.GoalID {
				filtered = append(filtered, goal)
			}
		}
		goals = filtered
	}
	if query.TaskID != 0 && len(goals) == 0 {
		return WorkflowReconciliation{}, fmt.Errorf("task %d is outside project %d", query.TaskID, projectID)
	}
	reconciliation := WorkflowReconciliation{
		Goals:        append([]domain.Goal(nil), goals...),
		Decisions:    make([]domain.Decision, 0),
		GoalHandoffs: make([]GoalHandoff, 0),
		PlanHandoffs: make([]PlanHandoff, 0),
		TaskHandoffs: make([]TaskHandoff, 0),
		Tasks:        make([]domain.Task, 0),
	}
	for _, goal := range goals {
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		for _, task := range tasks {
			if query.TaskID == 0 || task.ID == query.TaskID {
				reconciliation.Tasks = append(reconciliation.Tasks, task)
				handoffs, err := s.ListTaskHandoffs(ctx, task.ID)
				if err != nil {
					return WorkflowReconciliation{}, err
				}
				for _, handoff := range handoffs {
					reconciliation.TaskHandoffs = append(reconciliation.TaskHandoffs, handoff)
				}
			}
		}
		goalHandoffs, err := s.ListGoalHandoffs(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		reconciliation.GoalHandoffs = append(reconciliation.GoalHandoffs, goalHandoffs...)
		planHandoffs, err := s.ListPlanHandoffs(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		reconciliation.PlanHandoffs = append(reconciliation.PlanHandoffs, planHandoffs...)
		decisions, err := s.ListDecisionsForGoal(ctx, goal.ID)
		if err != nil {
			return WorkflowReconciliation{}, err
		}
		for _, decision := range decisions {
			if query.TaskID == 0 || decision.TaskID == query.TaskID {
				reconciliation.Decisions = append(reconciliation.Decisions, decision)
			}
		}
	}
	return reconciliation, nil
}

func (query WorkflowEventQuery) ProjectIDOr(fallback int64) int64 {
	if query.ProjectID != 0 {
		return query.ProjectID
	}
	return fallback
}

func (s *Store) ReconcileWorkflowEvents(ctx context.Context, query WorkflowEventQuery) (WorkflowReconciliation, error) {
	return s.ReconcileWorkflow(ctx, query)
}
