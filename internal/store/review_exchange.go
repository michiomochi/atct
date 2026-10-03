package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	ReviewScopeGoal = "goal"
	ReviewScopePlan = "plan"
	ReviewScopeTask = "task"

	ReviewRejectionSourceHandoff   = "handoff"
	ReviewRejectionSourceHuman     = "human"
	ReviewRejectionSourceWithdrawn = "withdrawn"
)

type ReviewExchangeHistory struct {
	Exchanges []ReviewExchange    `json:"exchanges"`
	Gaps      []ReviewExchangeGap `json:"gaps"`
}

type ReviewExchange struct {
	Scope     string          `json:"scope"` // "goal" | "plan" | "task"
	TaskID    int64           `json:"task_id,omitempty"`
	HandoffID string          `json:"handoff_id,omitempty"` // empty for a human rejection
	Rejection ReviewRejection `json:"rejection"`
	Response  *ReviewResponse `json:"response"`
}

type ReviewRejection struct {
	Source         string    `json:"source"` // "handoff" | "human" | "withdrawn"
	At             time.Time `json:"at"`
	ActorSessionID int64     `json:"actor_session_id,omitempty"`
	DecisionID     int64     `json:"decision_id,omitempty"`
	Reason         string    `json:"reason"`
}

type ReviewResponse struct {
	At              time.Time `json:"at"`
	AuthorSessionID int64     `json:"author_session_id"`
	HandoffID       string    `json:"handoff_id"`
	Report          string    `json:"report"`
}

type ReviewExchangeGap struct {
	Scope     string    `json:"scope"`
	HandoffID string    `json:"handoff_id"`
	TaskID    int64     `json:"task_id,omitempty"`
	BeforeAt  time.Time `json:"before_at"`
}

// reviewEvent is one rejection or submission on a single stream.
type reviewEvent struct {
	at         time.Time
	rejection  *ReviewExchange // set for a rejection
	submission *ReviewResponse // set for a submission
}

// ListReviewExchanges pairs every review rejection with the next submission on
// the same stream. taskID 0 covers the whole goal (goal, plan, human decisions
// and every task); taskID > 0 covers that task only.
//
// ponytail: no paging; a body is at most 16 KiB and a goal carries tens of
// exchanges. Cut by an after_id cursor when that stops holding.
func (s *Store) ListReviewExchanges(ctx context.Context, goalID, taskID int64) (ReviewExchangeHistory, error) {
	history := ReviewExchangeHistory{Exchanges: []ReviewExchange{}, Gaps: []ReviewExchangeGap{}}
	if _, err := s.GetGoal(ctx, goalID); err != nil {
		return history, err
	}
	q := sqlcgen.New(s.db)
	if taskID > 0 {
		owner, err := q.GetTaskGoalID(ctx, taskID)
		if errors.Is(err, sql.ErrNoRows) {
			return history, fmt.Errorf("%w: %d", ErrTaskNotFound, taskID)
		}
		if err != nil {
			return history, fmt.Errorf("lookup goal_id: %w", err)
		}
		if owner != goalID {
			return history, fmt.Errorf("%w: task %d belongs to goal %d, not %d", ErrTaskHandoffTaskMismatch, taskID, owner, goalID)
		}
	}

	streams := map[string][]reviewEvent{} // keyed by scope, then task id
	var order []string
	add := func(key string, ev reviewEvent) {
		if _, ok := streams[key]; !ok {
			order = append(order, key)
		}
		streams[key] = append(streams[key], ev)
	}
	entryEvent := func(scope string, task int64, id int64, handoffID, kind, body string, author sql.NullInt64, createdAt string) (reviewEvent, error) {
		at, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return reviewEvent{}, fmt.Errorf("parse %s handoff entry %d created_at: %w", scope, id, err)
		}
		if kind == HandoffEntryKindReviewRequested {
			return reviewEvent{at: at, submission: &ReviewResponse{At: at, AuthorSessionID: author.Int64, HandoffID: handoffID, Report: body}}, nil
		}
		return reviewEvent{at: at, rejection: &ReviewExchange{
			Scope: scope, TaskID: task, HandoffID: handoffID,
			Rejection: ReviewRejection{Source: ReviewRejectionSourceHandoff, At: at, ActorSessionID: author.Int64, Reason: body},
		}}, nil
	}

	if taskID == 0 {
		goalRows, err := q.ListGoalReviewExchangeEntries(ctx, goalID)
		if err != nil {
			return history, fmt.Errorf("list goal review entries: %w", err)
		}
		for _, r := range goalRows {
			ev, err := entryEvent(ReviewScopeGoal, 0, r.ID, r.HandoffID, r.Kind, r.Body, r.AuthorSessionID, r.CreatedAt)
			if err != nil {
				return history, err
			}
			add(ReviewScopeGoal, ev)
		}
		decisions, err := q.ListGoalReviewDecisions(ctx, goalID)
		if err != nil {
			return history, fmt.Errorf("list goal review decisions: %w", err)
		}
		for _, d := range decisions {
			stamp := d.CreatedAt
			if d.AnsweredAt.Valid && d.AnsweredAt.String != "" {
				stamp = d.AnsweredAt.String
			}
			at, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil {
				return history, fmt.Errorf("parse decision %d timestamp: %w", d.ID, err)
			}
			source := ReviewRejectionSourceWithdrawn
			if d.AnswerLabel == "reject" {
				source = ReviewRejectionSourceHuman
			}
			add(ReviewScopeGoal, reviewEvent{at: at, rejection: &ReviewExchange{
				Scope:     ReviewScopeGoal,
				Rejection: ReviewRejection{Source: source, At: at, DecisionID: d.ID, Reason: d.AnswerText},
			}})
		}
		planRows, err := q.ListPlanReviewExchangeEntries(ctx, goalID)
		if err != nil {
			return history, fmt.Errorf("list plan review entries: %w", err)
		}
		for _, r := range planRows {
			ev, err := entryEvent(ReviewScopePlan, 0, r.ID, r.HandoffID, r.Kind, r.Body, r.AuthorSessionID, r.CreatedAt)
			if err != nil {
				return history, err
			}
			add(ReviewScopePlan, ev)
		}
	}
	taskRows, err := q.ListTaskReviewExchangeEntries(ctx, sqlcgen.ListTaskReviewExchangeEntriesParams{GoalID: goalID, TaskID: taskID})
	if err != nil {
		return history, fmt.Errorf("list task review entries: %w", err)
	}
	for _, r := range taskRows {
		ev, err := entryEvent(ReviewScopeTask, r.TaskID, r.ID, r.HandoffID, r.Kind, r.Body, r.AuthorSessionID, r.CreatedAt)
		if err != nil {
			return history, err
		}
		add(fmt.Sprintf("%s/%020d", ReviewScopeTask, r.TaskID), ev)
	}

	for _, key := range order {
		history.Exchanges = append(history.Exchanges, pairReviewStream(streams[key])...)
	}
	sort.SliceStable(history.Exchanges, func(i, j int) bool {
		a, b := history.Exchanges[i], history.Exchanges[j]
		if !a.Rejection.At.Equal(b.Rejection.At) {
			return a.Rejection.At.Before(b.Rejection.At)
		}
		return a.Scope < b.Scope
	})

	gaps, err := s.listReviewGaps(ctx, q, goalID, taskID)
	if err != nil {
		return history, err
	}
	history.Gaps = gaps
	return history, nil
}

// pairReviewStream gives each rejection the first submission at or after it.
// On equal times the rejection sorts first, so a same-instant submission is
// its response.
func pairReviewStream(events []reviewEvent) []ReviewExchange {
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].at.Equal(events[j].at) {
			return events[i].at.Before(events[j].at)
		}
		return events[i].rejection != nil && events[j].rejection == nil
	})
	var out []ReviewExchange
	for i, ev := range events {
		if ev.rejection == nil {
			continue
		}
		ex := *ev.rejection
		for _, next := range events[i+1:] {
			if next.submission != nil {
				resp := *next.submission
				ex.Response = &resp
				break
			}
		}
		out = append(out, ex)
	}
	return out
}

func (s *Store) listReviewGaps(ctx context.Context, q *sqlcgen.Queries, goalID, taskID int64) ([]ReviewExchangeGap, error) {
	gaps := []ReviewExchangeGap{}
	add := func(scope, handoffID string, task int64, before interface{}) error {
		text, ok := before.(string)
		if !ok || text == "" {
			return nil // no entry yet, so nothing is known to be missing
		}
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return fmt.Errorf("parse %s gap %s: %w", scope, handoffID, err)
		}
		gaps = append(gaps, ReviewExchangeGap{Scope: scope, HandoffID: handoffID, TaskID: task, BeforeAt: at})
		return nil
	}
	if taskID == 0 {
		goalGaps, err := q.ListGoalReviewGaps(ctx, goalID)
		if err != nil {
			return nil, fmt.Errorf("list goal review gaps: %w", err)
		}
		for _, g := range goalGaps {
			if err := add(ReviewScopeGoal, g.HandoffID, 0, g.BeforeAt); err != nil {
				return nil, err
			}
		}
		planGaps, err := q.ListPlanReviewGaps(ctx, goalID)
		if err != nil {
			return nil, fmt.Errorf("list plan review gaps: %w", err)
		}
		for _, g := range planGaps {
			if err := add(ReviewScopePlan, g.HandoffID, 0, g.BeforeAt); err != nil {
				return nil, err
			}
		}
	}
	taskGaps, err := q.ListTaskReviewGaps(ctx, sqlcgen.ListTaskReviewGapsParams{GoalID: goalID, TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("list task review gaps: %w", err)
	}
	for _, g := range taskGaps {
		if err := add(ReviewScopeTask, g.HandoffID, g.TaskID, g.BeforeAt); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(gaps, func(i, j int) bool { return gaps[i].BeforeAt.Before(gaps[j].BeforeAt) })
	return gaps, nil
}

// appendPlanHandoffEntryTx records a plan review submission or rejection in the
// same transaction as the plan handoff row it describes.
func appendPlanHandoffEntryTx(ctx context.Context, q *sqlcgen.Queries, handoffID, kind, body string, authorSessionID int64, createdAt string) error {
	body, err := normalizeHandoffEntryBody(body)
	if err != nil {
		return err
	}
	if _, err := q.CreatePlanHandoffEntry(ctx, sqlcgen.CreatePlanHandoffEntryParams{
		HandoffID: handoffID, Kind: kind, Body: body,
		AuthorSessionID: nullableEntryAuthor(authorSessionID), CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("insert plan handoff entry: %w", err)
	}
	return nil
}
