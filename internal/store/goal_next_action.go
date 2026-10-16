package store

import (
	"context"
	"fmt"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// NextAction is the step a goal waits on, shown read-only next to the goal.
// An empty Code means no step applies right now.
type NextAction struct {
	Code       string `json:"code"`
	Actor      string `json:"actor"`
	Call       string `json:"call,omitempty"`
	DecisionID int64  `json:"decision_id,omitempty"`
	TaskCount  int    `json:"task_count,omitempty"`
}

// NextActionSpec is the actor and the ATCT tool call that answer a NextAction code.
type NextActionSpec struct {
	Actor string
	Call  string
}

// NextActionSpecs maps every NextAction code to its actor and call. Codes whose
// actor is human have no call: the human answers a decision in the dashboard.
var NextActionSpecs = map[string]NextActionSpec{
	"done":                                {},
	"withdrawn":                           {},
	"human_review":                        {Actor: "human"},
	"human_decision":                      {Actor: "human"},
	"commander_complete":                  {Actor: "commander", Call: "atct_goal_review_complete"},
	"commander_reject_handoff":            {Actor: "commander", Call: "atct_goal_handoff_review_reject"},
	"commander_request_handoff":           {Actor: "commander", Call: "atct_goal_handoff_request"},
	"subcommander_receive_handoff":        {Actor: "subcommander", Call: "atct_goal_handoff_receive"},
	"subcommander_receive_rejection":      {Actor: "subcommander", Call: "atct_goal_handoff_review_reject_receive"},
	"commander_receive_goal_review":       {Actor: "commander", Call: "atct_goal_handoff_review_receive"},
	"commander_review_goal":               {Actor: "commander", Call: "atct_goal_review_request"},
	"subcommander_plan":                   {Actor: "subcommander", Call: "atct_plan_handoff_review_request"},
	"subcommander_receive_plan_rejection": {Actor: "subcommander", Call: "atct_plan_handoff_review_reject_receive"},
	"commander_receive_plan_review":       {Actor: "commander", Call: "atct_plan_handoff_review_receive"},
	"commander_review_plan":               {Actor: "commander", Call: "atct_plan_handoff_complete"},
	"subcommander_receive_task_create":    {Actor: "subcommander", Call: "atct_task_create_handoff_receive"},
	"subcommander_create_tasks":           {Actor: "subcommander", Call: "atct_task_create"},
	"subcommander_review_task":            {Actor: "subcommander", Call: "atct_task_handoff_review_receive"},
	"subcommander_delegate_task":          {Actor: "subcommander", Call: "atct_task_handoff_request"},
	"executor_receive_task":               {Actor: "executor", Call: "atct_task_handoff_receive"},
	"executor_implement":                  {Actor: "executor", Call: "atct_task_handoff_review_request"},
	"subcommander_request_goal_review":    {Actor: "subcommander", Call: "atct_goal_handoff_review_request"},
}

// nextActionInput is everything deriveNextAction reads. Handoffs are the latest
// open one of their kind (nil when there is none); tasks are in ListTasks order.
type nextActionInput struct {
	Status       domain.GoalStatus
	OpenDecision *domain.Decision
	PendingState string
	GoalHandoff  *GoalHandoff
	PlanHandoff  *PlanHandoff
	TaskCreate   *TaskCreateHandoff
	Tasks        []domain.Task
	TaskHandoffs map[int64]*TaskHandoff
}

// goalRows is everything nextActionFor reads for one goal, already loaded.
type goalRows struct {
	openDecisions []domain.Decision // open decisions, in created_at order
	decisions     []domain.Decision // the goal's goal_review decisions, in id order
	goalHandoffs  []GoalHandoff
	planHandoffs  []PlanHandoff
	taskCreates   []TaskCreateHandoff
	tasks         []domain.Task
	taskHandoffs  map[int64]*TaskHandoff
}

// nextActionFor derives the next action of goal from its rows. GoalNextAction and
// GoalNextActions both derive through it, so they cannot disagree.
func nextActionFor(goal domain.Goal, rows goalRows) (NextAction, error) {
	if goal.Status == domain.GoalDone || goal.Status == domain.GoalDropped {
		return deriveNextAction(nextActionInput{Status: goal.Status}), nil
	}
	in := nextActionInput{Status: goal.Status}
	if len(rows.openDecisions) > 0 {
		in.OpenDecision = &rows.openDecisions[0]
	}
	pending, err := goalPendingState(rows.decisions, func() ([]GoalHandoff, error) { return rows.goalHandoffs, nil })
	if err != nil {
		return NextAction{}, err
	}
	in.PendingState = pending
	for i := range rows.goalHandoffs {
		if h := rows.goalHandoffs[i]; h.CompletedReportAt == nil && h.RecoveredAt == nil {
			in.GoalHandoff = &rows.goalHandoffs[i]
		}
	}
	if len(rows.planHandoffs) > 0 {
		in.PlanHandoff = &rows.planHandoffs[len(rows.planHandoffs)-1]
	}
	for i := range rows.taskCreates {
		if h := rows.taskCreates[i]; h.CompletedAt == nil && h.RecoveredAt == nil {
			in.TaskCreate = &rows.taskCreates[i]
		}
	}
	in.Tasks = rows.tasks
	in.TaskHandoffs = rows.taskHandoffs
	return deriveNextAction(in), nil
}

// GoalNextAction derives the next step of goal from the store. It only reads.
func (s *Store) GoalNextAction(ctx context.Context, goal domain.Goal) (NextAction, error) {
	if goal.Status == domain.GoalDone || goal.Status == domain.GoalDropped {
		return nextActionFor(goal, goalRows{})
	}
	var rows goalRows
	var err error
	if rows.openDecisions, err = s.ListOpenDecisions(ctx, goal.ID); err != nil {
		return NextAction{}, fmt.Errorf("list open decisions for goal %d: %w", goal.ID, err)
	}
	if rows.decisions, err = s.ListDecisionsForGoal(ctx, goal.ID); err != nil {
		return NextAction{}, fmt.Errorf("list decisions for goal %d: %w", goal.ID, err)
	}
	if rows.goalHandoffs, err = s.ListGoalHandoffs(ctx, goal.ID); err != nil {
		return NextAction{}, fmt.Errorf("list goal handoffs for goal %d: %w", goal.ID, err)
	}
	if rows.planHandoffs, err = s.ListPlanHandoffs(ctx, goal.ID); err != nil {
		return NextAction{}, fmt.Errorf("list plan handoffs for goal %d: %w", goal.ID, err)
	}
	if rows.taskCreates, err = s.ListTaskCreateHandoffs(ctx, goal.ID); err != nil {
		return NextAction{}, fmt.Errorf("list task create handoffs for goal %d: %w", goal.ID, err)
	}
	if rows.tasks, err = s.ListTasks(ctx, goal.ID); err != nil {
		return NextAction{}, fmt.Errorf("list tasks for goal %d: %w", goal.ID, err)
	}
	if rows.taskHandoffs, err = s.ListOpenTaskHandoffsForGoal(ctx, goal.ID); err != nil {
		return NextAction{}, err
	}
	return nextActionFor(goal, rows)
}

// allGoalRows is goalRows for every active goal, each table read once.
type allGoalRows struct {
	openDecisions map[int64][]domain.Decision
	decisions     map[int64][]domain.Decision
	goalHandoffs  map[int64][]GoalHandoff
	planHandoffs  map[int64][]PlanHandoff
	taskCreates   map[int64][]TaskCreateHandoff
	tasks         map[int64][]domain.Task
	taskHandoffs  map[int64]*TaskHandoff
}

func (s *Store) loadAllGoalRows(ctx context.Context) (allGoalRows, error) {
	var all allGoalRows
	open, err := s.ListAllOpenDecisions(ctx)
	if err != nil {
		return allGoalRows{}, fmt.Errorf("list open decisions: %w", err)
	}
	all.openDecisions = groupByGoal(open, func(d domain.Decision) int64 { return d.GoalID })

	decisionRows, err := sqlcgen.New(s.db).ListGoalReviewDecisionsForActiveGoals(ctx)
	if err != nil {
		return allGoalRows{}, fmt.Errorf("list goal review decisions: %w", err)
	}
	decisions, err := convertDecisionRows(decisionRows, decisionRowFromSQLC)
	if err != nil {
		return allGoalRows{}, err
	}
	all.decisions = groupByGoal(decisions, func(d domain.Decision) int64 { return d.GoalID })

	goalHandoffRows, err := sqlcgen.New(s.db).ListGoalHandoffsForActiveGoals(ctx)
	if err != nil {
		return allGoalRows{}, fmt.Errorf("list goal handoffs for active goals: %w", err)
	}
	goalHandoffs := make([]GoalHandoff, 0, len(goalHandoffRows))
	for _, row := range goalHandoffRows {
		handoff, err := goalHandoffFromRow(row)
		if err != nil {
			return allGoalRows{}, fmt.Errorf("parse goal handoff: %w", err)
		}
		goalHandoffs = append(goalHandoffs, handoff)
	}
	all.goalHandoffs = groupByGoal(goalHandoffs, func(h GoalHandoff) int64 { return h.GoalID })

	planHandoffRows, err := sqlcgen.New(s.db).ListPlanHandoffsForActiveGoals(ctx)
	if err != nil {
		return allGoalRows{}, fmt.Errorf("list plan handoffs for active goals: %w", err)
	}
	planHandoffs := make([]PlanHandoff, 0, len(planHandoffRows))
	for _, row := range planHandoffRows {
		handoff, err := planHandoffFromRow(row)
		if err != nil {
			return allGoalRows{}, fmt.Errorf("parse plan handoff: %w", err)
		}
		planHandoffs = append(planHandoffs, handoff)
	}
	all.planHandoffs = groupByGoal(planHandoffs, func(h PlanHandoff) int64 { return h.GoalID })

	taskCreateRows, err := sqlcgen.New(s.db).ListTaskCreateHandoffsForActiveGoals(ctx)
	if err != nil {
		return allGoalRows{}, fmt.Errorf("list task create handoffs for active goals: %w", err)
	}
	taskCreates := make([]TaskCreateHandoff, 0, len(taskCreateRows))
	for _, row := range taskCreateRows {
		handoff, err := taskCreateHandoffFromRow(row)
		if err != nil {
			return allGoalRows{}, err
		}
		taskCreates = append(taskCreates, handoff)
	}
	all.taskCreates = groupByGoal(taskCreates, func(h TaskCreateHandoff) int64 { return h.GoalID })

	if all.tasks, err = s.ListTasksForAllGoals(ctx); err != nil {
		return allGoalRows{}, fmt.Errorf("list tasks for all goals: %w", err)
	}
	if all.taskHandoffs, err = s.ListOpenTaskHandoffsForAllGoals(ctx); err != nil {
		return allGoalRows{}, err
	}
	return all, nil
}

// groupByGoal splits rows by goal, keeping each goal's rows in their input order.
func groupByGoal[T any](rows []T, goalID func(T) int64) map[int64][]T {
	out := make(map[int64][]T)
	for _, row := range rows {
		id := goalID(row)
		out[id] = append(out[id], row)
	}
	return out
}

// GoalNextActions returns GoalNextAction for each goal in goals, which must be
// active goals, with one read per table for all of them instead of one per goal.
func (s *Store) GoalNextActions(ctx context.Context, goals []domain.Goal) (map[int64]NextAction, error) {
	all, err := s.loadAllGoalRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]NextAction, len(goals))
	for _, goal := range goals {
		rows := goalRows{
			openDecisions: all.openDecisions[goal.ID],
			decisions:     all.decisions[goal.ID],
			goalHandoffs:  all.goalHandoffs[goal.ID],
			planHandoffs:  all.planHandoffs[goal.ID],
			taskCreates:   all.taskCreates[goal.ID],
			tasks:         all.tasks[goal.ID],
			taskHandoffs:  all.taskHandoffs,
		}
		next, err := nextActionFor(goal, rows)
		if err != nil {
			return nil, fmt.Errorf("next action for goal %d: %w", goal.ID, err)
		}
		out[goal.ID] = next
	}
	return out, nil
}

// GoalsAwaitingHandoffReview returns the active goals that have a goal or plan
// handoff whose review was requested and is neither received, rejected, nor completed.
func (s *Store) GoalsAwaitingHandoffReview(ctx context.Context) (map[int64]bool, error) {
	goalHandoffRows, err := sqlcgen.New(s.db).ListGoalHandoffsForActiveGoals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list goal handoffs for active goals: %w", err)
	}
	planHandoffRows, err := sqlcgen.New(s.db).ListPlanHandoffsForActiveGoals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plan handoffs for active goals: %w", err)
	}
	out := make(map[int64]bool)
	for _, row := range goalHandoffRows {
		h, err := goalHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse goal handoff: %w", err)
		}
		if h.ReviewRequestedAt != nil && h.ReviewReceivedAt == nil &&
			h.ReviewRejectedAt == nil && h.CompletedReportAt == nil && h.RecoveredAt == nil {
			out[h.GoalID] = true
		}
	}
	for _, row := range planHandoffRows {
		h, err := planHandoffFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("parse plan handoff: %w", err)
		}
		if h.ReviewRequestedAt != nil && h.ReviewReceivedAt == nil &&
			h.ReviewRejectedAt == nil && h.CompletedReportAt == nil {
			out[h.GoalID] = true
		}
	}
	return out, nil
}

// deriveNextAction applies the goal's derivation order: the goal's end state,
// then an open decision, then the pending state, then the goal handoff, then
// the plan and task-create steps while no task is left, then the task step
// chosen by status priority.
func deriveNextAction(in nextActionInput) NextAction {
	switch in.Status {
	case domain.GoalDone:
		return newNextAction("done")
	case domain.GoalDropped:
		return newNextAction("withdrawn")
	}

	if d := in.OpenDecision; d != nil {
		code := "human_decision"
		if d.Kind == domain.KindGoalReview {
			code = "human_review"
		}
		na := newNextAction(code)
		na.DecisionID = d.ID
		return na
	}

	switch in.PendingState {
	case GoalPendingApprovedComplete:
		return newNextAction("commander_complete")
	case GoalPendingRejectedHandoffReject:
		return newNextAction("commander_reject_handoff")
	}

	if h := in.GoalHandoff; h == nil {
		return newNextAction("commander_request_handoff")
	} else if h.ReceivedAt == nil {
		return newNextAction("subcommander_receive_handoff")
	} else if h.ReviewRejectedAt != nil && h.ReviewRejectionReceivedAt == nil {
		return newNextAction("subcommander_receive_rejection")
	} else if h.ReviewRejectionReceivedAt == nil {
		if h.ReviewRequestedAt != nil && h.ReviewReceivedAt == nil {
			return newNextAction("commander_receive_goal_review")
		}
		if h.ReviewReceivedAt != nil {
			return newNextAction("commander_review_goal")
		}
	}
	// A received handoff with no review requested, or with its rejection
	// received, is the subcommander's own work: fall through to the tasks.

	active := make([]domain.Task, 0, len(in.Tasks))
	for _, task := range in.Tasks {
		if task.Status != domain.TaskDropped {
			active = append(active, task)
		}
	}
	if len(active) == 0 {
		return derivePlanNextAction(in)
	}
	return deriveTaskNextAction(active, in.TaskHandoffs)
}

func derivePlanNextAction(in nextActionInput) NextAction {
	if p := in.PlanHandoff; p == nil || p.CompletedReportAt == nil {
		if p == nil || p.ReviewRequestedAt == nil || p.ReviewRejectionReceivedAt != nil {
			return newNextAction("subcommander_plan")
		}
		if p.ReviewRejectedAt != nil {
			return newNextAction("subcommander_receive_plan_rejection")
		}
		if p.ReviewReceivedAt == nil {
			return newNextAction("commander_receive_plan_review")
		}
		return newNextAction("commander_review_plan")
	}
	if t := in.TaskCreate; t != nil && t.ReceivedAt == nil {
		return newNextAction("subcommander_receive_task_create")
	}
	return newNextAction("subcommander_create_tasks")
}

// deriveTaskNextAction picks the step by task status priority, not by task order:
// a task in review, then a todo task with no handoff, then a todo task whose
// handoff is not yet received, then a doing task. TaskCount is the number of
// tasks in the chosen status bucket. When every active task is done, it requests
// the goal review and TaskCount is the number of done tasks. A todo task whose
// handoff is already received needs no step here.
func deriveTaskNextAction(active []domain.Task, handoffs map[int64]*TaskHandoff) NextAction {
	var review, delegate, receive, doing, done int
	for _, task := range active {
		switch task.Status {
		case domain.TaskReview:
			review++
		case domain.TaskTodo:
			h, open := handoffs[task.ID]
			switch {
			case !open:
				delegate++
			case h.ReceivedAt == nil:
				receive++
			}
		case domain.TaskDoing:
			doing++
		case domain.TaskDone:
			done++
		}
	}

	steps := []struct {
		code  string
		count int
	}{
		{"subcommander_review_task", review},
		{"subcommander_delegate_task", delegate},
		{"executor_receive_task", receive},
		{"executor_implement", doing},
	}
	for _, step := range steps {
		if step.count > 0 {
			na := newNextAction(step.code)
			na.TaskCount = step.count
			return na
		}
	}
	if done == len(active) {
		na := newNextAction("subcommander_request_goal_review")
		na.TaskCount = done
		return na
	}
	return NextAction{}
}

func newNextAction(code string) NextAction {
	spec := NextActionSpecs[code]
	return NextAction{Code: code, Actor: spec.Actor, Call: spec.Call}
}
