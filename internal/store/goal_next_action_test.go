package store

import (
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func naPtr() *time.Time {
	t := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	return &t
}

func TestDeriveNextActionFollowsBranchOrder(t *testing.T) {
	openGoalReview := &domain.Decision{ID: 7, Kind: domain.KindGoalReview, Status: domain.DecisionOpen}
	openDecision := &domain.Decision{ID: 8, Kind: domain.KindDecision, Status: domain.DecisionOpen}
	received := func() *GoalHandoff { return &GoalHandoff{ReceivedAt: naPtr()} }
	activeTask := func(id int64, status domain.TaskStatus) domain.Task {
		return domain.Task{ID: id, Status: status}
	}
	openHandoff := func(isReceived bool) *TaskHandoff {
		h := &TaskHandoff{}
		if isReceived {
			h.ReceivedAt = naPtr()
		}
		return h
	}
	completedPlan := func() *PlanHandoff {
		return &PlanHandoff{ReviewRequestedAt: naPtr(), ReviewReceivedAt: naPtr(), CompletedReportAt: naPtr()}
	}

	cases := []struct {
		name       string
		in         nextActionInput
		code       string
		decisionID int64
		taskCount  int
	}{
		// Goal end state comes first.
		{name: "done goal", in: nextActionInput{Status: domain.GoalDone}, code: "done"},
		{name: "dropped goal", in: nextActionInput{Status: domain.GoalDropped}, code: "withdrawn"},
		{name: "done goal ignores open decision", in: nextActionInput{Status: domain.GoalDone, OpenDecision: openGoalReview}, code: "done"},
		{name: "dropped goal ignores pending approval", in: nextActionInput{Status: domain.GoalDropped, PendingState: GoalPendingApprovedComplete}, code: "withdrawn"},

		// An open decision comes before the pending state and the handoff.
		{name: "open goal review", in: nextActionInput{Status: domain.GoalActive, OpenDecision: openGoalReview}, code: "human_review", decisionID: 7},
		{name: "open decision", in: nextActionInput{Status: domain.GoalActive, OpenDecision: openDecision}, code: "human_decision", decisionID: 8},
		{name: "open decision beats approved pending state", in: nextActionInput{Status: domain.GoalActive, OpenDecision: openDecision, PendingState: GoalPendingApprovedComplete}, code: "human_decision", decisionID: 8},
		{name: "open decision beats rejected pending state", in: nextActionInput{Status: domain.GoalActive, OpenDecision: openGoalReview, PendingState: GoalPendingRejectedHandoffReject}, code: "human_review", decisionID: 7},
		{name: "open decision beats task work", in: nextActionInput{Status: domain.GoalActive, OpenDecision: openDecision, Tasks: []domain.Task{activeTask(1, domain.TaskDoing)}}, code: "human_decision", decisionID: 8},

		// Pending state comes before the goal handoff.
		{name: "approved pending state", in: nextActionInput{Status: domain.GoalActive, PendingState: GoalPendingApprovedComplete}, code: "commander_complete"},
		{name: "approved pending state beats missing handoff", in: nextActionInput{Status: domain.GoalActive, PendingState: GoalPendingApprovedComplete, GoalHandoff: nil}, code: "commander_complete"},
		{name: "rejected pending state", in: nextActionInput{Status: domain.GoalActive, PendingState: GoalPendingRejectedHandoffReject}, code: "commander_reject_handoff"},
		{name: "rejected pending state beats handoff received", in: nextActionInput{Status: domain.GoalActive, PendingState: GoalPendingRejectedHandoffReject, GoalHandoff: received()}, code: "commander_reject_handoff"},

		// Goal handoff stages.
		{name: "no goal handoff", in: nextActionInput{Status: domain.GoalActive}, code: "commander_request_handoff"},
		{name: "goal handoff not received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: &GoalHandoff{}}, code: "subcommander_receive_handoff"},
		{name: "goal handoff rejection not received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: &GoalHandoff{ReceivedAt: naPtr(), ReviewRequestedAt: naPtr(), ReviewReceivedAt: naPtr(), ReviewRejectedAt: naPtr()}}, code: "subcommander_receive_rejection"},
		{name: "goal review requested, not yet received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: &GoalHandoff{ReceivedAt: naPtr(), ReviewRequestedAt: naPtr()}}, code: "commander_receive_goal_review"},
		{name: "goal review received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: &GoalHandoff{ReceivedAt: naPtr(), ReviewRequestedAt: naPtr(), ReviewReceivedAt: naPtr()}}, code: "commander_review_goal"},
		{name: "goal handoff rejection received falls through to planning", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: &GoalHandoff{ReceivedAt: naPtr(), ReviewRequestedAt: naPtr(), ReviewReceivedAt: naPtr(), ReviewRejectedAt: naPtr(), ReviewRejectionReceivedAt: naPtr()}}, code: "subcommander_plan"},
		{name: "received goal handoff with no review falls through to planning", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received()}, code: "subcommander_plan"},

		// Plan stages, reached while no task is left.
		{name: "no plan handoff", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received()}, code: "subcommander_plan"},
		{name: "plan not yet review-requested", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: &PlanHandoff{}}, code: "subcommander_plan"},
		{name: "plan rejection received, plan resubmitted", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: &PlanHandoff{ReviewRequestedAt: naPtr(), ReviewRejectedAt: naPtr(), ReviewRejectionReceivedAt: naPtr()}}, code: "subcommander_plan"},
		{name: "plan rejection not received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: &PlanHandoff{ReviewRequestedAt: naPtr(), ReviewRejectedAt: naPtr()}}, code: "subcommander_receive_plan_rejection"},
		{name: "plan review not yet received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: &PlanHandoff{ReviewRequestedAt: naPtr()}}, code: "commander_receive_plan_review"},
		{name: "plan review received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: &PlanHandoff{ReviewRequestedAt: naPtr(), ReviewReceivedAt: naPtr()}}, code: "commander_review_plan"},

		// Task-create stages, after the plan is completed.
		{name: "completed plan, task create not received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: completedPlan(), TaskCreate: &TaskCreateHandoff{}}, code: "subcommander_receive_task_create"},
		{name: "completed plan, no task create handoff", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: completedPlan()}, code: "subcommander_create_tasks"},
		{name: "completed plan, task create received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), PlanHandoff: completedPlan(), TaskCreate: &TaskCreateHandoff{ReceivedAt: naPtr()}}, code: "subcommander_create_tasks"},

		// Task stages, one per status.
		{name: "task in review", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskReview)}}, code: "subcommander_review_task", taskCount: 1},
		{name: "todo task with no handoff", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskTodo)}}, code: "subcommander_delegate_task", taskCount: 1},
		{name: "todo task with handoff not received", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskTodo)}, TaskHandoffs: map[int64]*TaskHandoff{1: openHandoff(false)}}, code: "executor_receive_task", taskCount: 1},
		{name: "todo task with handoff received needs no step", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskTodo)}, TaskHandoffs: map[int64]*TaskHandoff{1: openHandoff(true)}}, code: ""},
		{name: "doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDoing)}}, code: "executor_implement", taskCount: 1},

		// Task priority: the status bucket decides the step, not the task order.
		// TaskCount is the size of that bucket.
		{name: "review wins over earlier doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDoing), activeTask(2, domain.TaskReview)}}, code: "subcommander_review_task", taskCount: 1},
		{name: "review wins over later doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskReview), activeTask(2, domain.TaskDoing)}}, code: "subcommander_review_task", taskCount: 1},
		{name: "review bucket counts every review task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDoing), activeTask(2, domain.TaskReview), activeTask(3, domain.TaskReview)}}, code: "subcommander_review_task", taskCount: 2},
		{name: "delegate wins over earlier doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDoing), activeTask(2, domain.TaskTodo)}}, code: "subcommander_delegate_task", taskCount: 1},
		{name: "delegate bucket counts every undelegated todo task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskTodo), activeTask(2, domain.TaskTodo), activeTask(3, domain.TaskDoing)}}, code: "subcommander_delegate_task", taskCount: 2},
		{name: "delegate wins over later receive", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskTodo), activeTask(2, domain.TaskTodo)}, TaskHandoffs: map[int64]*TaskHandoff{1: openHandoff(false)}}, code: "subcommander_delegate_task", taskCount: 1},
		{name: "receive wins over earlier doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDoing), activeTask(2, domain.TaskTodo), activeTask(3, domain.TaskTodo)}, TaskHandoffs: map[int64]*TaskHandoff{2: openHandoff(false), 3: openHandoff(false)}}, code: "executor_receive_task", taskCount: 2},
		{name: "skipped todo task then doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskTodo), activeTask(2, domain.TaskDoing)}, TaskHandoffs: map[int64]*TaskHandoff{1: openHandoff(true)}}, code: "executor_implement", taskCount: 1},
		{name: "done task before doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDone), activeTask(2, domain.TaskDoing)}}, code: "executor_implement", taskCount: 1},
		{name: "doing bucket counts every doing task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDone), activeTask(2, domain.TaskDoing), activeTask(3, domain.TaskDoing)}}, code: "executor_implement", taskCount: 2},
		{name: "dropped task is not active", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDropped), activeTask(2, domain.TaskDoing)}}, code: "executor_implement", taskCount: 1},

		// All active tasks done: request goal review. Dropped tasks do not count.
		{name: "all tasks done", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDone), activeTask(2, domain.TaskDone)}}, code: "subcommander_request_goal_review", taskCount: 2},
		{name: "done tasks with dropped task", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDone), activeTask(2, domain.TaskDropped)}}, code: "subcommander_request_goal_review", taskCount: 1},
		{name: "all tasks done but one skipped todo", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDone), activeTask(2, domain.TaskTodo)}, TaskHandoffs: map[int64]*TaskHandoff{2: openHandoff(true)}}, code: ""},

		// Only dropped tasks: no active task, so the plan branch applies.
		{name: "only dropped tasks falls through to planning", in: nextActionInput{Status: domain.GoalActive, GoalHandoff: received(), Tasks: []domain.Task{activeTask(1, domain.TaskDropped)}}, code: "subcommander_plan"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := deriveNextAction(c.in)
			if got.Code != c.code {
				t.Fatalf("code = %q, want %q", got.Code, c.code)
			}
			if got.DecisionID != c.decisionID {
				t.Fatalf("decision id = %d, want %d", got.DecisionID, c.decisionID)
			}
			if got.TaskCount != c.taskCount {
				t.Fatalf("task count = %d, want %d", got.TaskCount, c.taskCount)
			}
			if spec := NextActionSpecs[c.code]; got.Actor != spec.Actor || got.Call != spec.Call {
				t.Fatalf("actor/call = %q/%q, want %q/%q", got.Actor, got.Call, spec.Actor, spec.Call)
			}
		})
	}
}

func TestNextActionSpecsCoverEveryCode(t *testing.T) {
	for code, spec := range NextActionSpecs {
		switch spec.Actor {
		case "":
			if code != "done" && code != "withdrawn" {
				t.Fatalf("code %q has no actor", code)
			}
			if spec.Call != "" {
				t.Fatalf("code %q has call %q without an actor", code, spec.Call)
			}
		case "human":
			if spec.Call != "" {
				t.Fatalf("human code %q must not have a call, got %q", code, spec.Call)
			}
		case "commander", "subcommander", "executor":
			if len(spec.Call) < len("atct_") || spec.Call[:len("atct_")] != "atct_" {
				t.Fatalf("code %q call = %q, want an atct_ tool name", code, spec.Call)
			}
		default:
			t.Fatalf("code %q has unknown actor %q", code, spec.Actor)
		}
	}
}
