package domain

// Reasons a decision sits where it does in the inbox. The web maps each code
// to a label (decision.priority.<code>).
const (
	PriorityGoalReview     = "goal_review"
	PriorityTaskInProgress = "task_in_progress"
	PriorityQueued         = "queued"
	PriorityAutoSettles    = "auto_settles"
)

// DecisionPriority ranks an open decision (1 = most urgent); the first
// matching row wins. taskStatus is only read when hasTask is true.
func DecisionPriority(kind DecisionKind, taskStatus TaskStatus, hasTask bool, autoSettles bool) (rank int, reason string) {
	switch {
	case kind == KindGoalReview:
		return 1, PriorityGoalReview
	case hasTask && (taskStatus == TaskDoing || taskStatus == TaskReview):
		return 2, PriorityTaskInProgress
	case !autoSettles:
		return 3, PriorityQueued
	default:
		return 4, PriorityAutoSettles
	}
}
