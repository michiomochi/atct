package store

import (
	"context"
	"fmt"

	"github.com/michiomochi/atct/internal/domain"
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

// GoalNextAction derives the next step of goal from the store. It only reads.
func (s *Store) GoalNextAction(ctx context.Context, goal domain.Goal) (NextAction, error) {
	if goal.Status == domain.GoalDone || goal.Status == domain.GoalDropped {
		return deriveNextAction(nextActionInput{Status: goal.Status}), nil
	}
	in := nextActionInput{Status: goal.Status}

	decisions, err := s.ListOpenDecisions(ctx, goal.ID)
	if err != nil {
		return NextAction{}, fmt.Errorf("list open decisions for goal %d: %w", goal.ID, err)
	}
	if len(decisions) > 0 {
		in.OpenDecision = &decisions[0]
	}

	if in.PendingState, err = s.GoalPendingState(ctx, goal.ID); err != nil {
		return NextAction{}, err
	}

	goalHandoffs, err := s.ListGoalHandoffs(ctx, goal.ID)
	if err != nil {
		return NextAction{}, fmt.Errorf("list goal handoffs for goal %d: %w", goal.ID, err)
	}
	for i := range goalHandoffs {
		if h := goalHandoffs[i]; h.CompletedReportAt == nil && h.RecoveredAt == nil {
			in.GoalHandoff = &goalHandoffs[i]
		}
	}

	planHandoffs, err := s.ListPlanHandoffs(ctx, goal.ID)
	if err != nil {
		return NextAction{}, fmt.Errorf("list plan handoffs for goal %d: %w", goal.ID, err)
	}
	if len(planHandoffs) > 0 {
		in.PlanHandoff = &planHandoffs[len(planHandoffs)-1]
	}

	taskCreates, err := s.ListTaskCreateHandoffs(ctx, goal.ID)
	if err != nil {
		return NextAction{}, fmt.Errorf("list task create handoffs for goal %d: %w", goal.ID, err)
	}
	for i := range taskCreates {
		if h := taskCreates[i]; h.CompletedAt == nil && h.RecoveredAt == nil {
			in.TaskCreate = &taskCreates[i]
		}
	}

	if in.Tasks, err = s.ListTasks(ctx, goal.ID); err != nil {
		return NextAction{}, fmt.Errorf("list tasks for goal %d: %w", goal.ID, err)
	}
	if in.TaskHandoffs, err = s.ListOpenTaskHandoffsForGoal(ctx, goal.ID); err != nil {
		return NextAction{}, err
	}
	return deriveNextAction(in), nil
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
