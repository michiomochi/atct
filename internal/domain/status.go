package domain

import "fmt"

type GoalStatus string

const (
	GoalProposed GoalStatus = "proposed"
	GoalActive   GoalStatus = "active"
	GoalDone     GoalStatus = "done"
	GoalDropped  GoalStatus = "dropped"
)

type TaskStatus string

const (
	TaskTodo    TaskStatus = "todo"
	TaskDoing   TaskStatus = "doing"
	TaskDone    TaskStatus = "done"
	TaskDropped TaskStatus = "dropped"
)

type DecisionStatus string

const (
	DecisionOpen      DecisionStatus = "open"
	DecisionAnswered  DecisionStatus = "answered"
	DecisionApplied   DecisionStatus = "applied"
	DecisionWithdrawn DecisionStatus = "withdrawn"
)

type DecisionKind string

const (
	KindDecision     DecisionKind = "decision"
	KindCompletion   DecisionKind = "completion"
	KindGoalApproval DecisionKind = "goal_approval"
)

func ParseTaskStatus(s string) (TaskStatus, error) {
	switch TaskStatus(s) {
	case TaskTodo, TaskDoing, TaskDone, TaskDropped:
		return TaskStatus(s), nil
	}
	return "", fmt.Errorf("unknown task status: %q", s)
}
