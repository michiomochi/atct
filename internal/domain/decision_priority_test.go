package domain

import "testing"

func TestDecisionPriority(t *testing.T) {
	cases := []struct {
		name       string
		kind       DecisionKind
		status     TaskStatus
		hasTask    bool
		auto       bool
		wantRank   int
		wantReason string
	}{
		{"goal_review", KindGoalReview, "", false, false, 1, PriorityGoalReview},
		{"goal_review with default stays 1", KindGoalReview, "", false, true, 1, PriorityGoalReview},
		{"doing task", KindDecision, TaskDoing, true, false, 2, PriorityTaskInProgress},
		{"review task with default stays 2", KindDecision, TaskReview, true, true, 2, PriorityTaskInProgress},
		{"goal-level decision", KindDecision, "", false, false, 3, PriorityQueued},
		{"todo task", KindDecision, TaskTodo, true, false, 3, PriorityQueued},
		{"done task", KindDecision, TaskDone, true, false, 3, PriorityQueued},
		{"dropped task", KindDecision, TaskDropped, true, false, 3, PriorityQueued},
		{"goal-level with default", KindDecision, "", false, true, 4, PriorityAutoSettles},
		{"done task with default", KindDecision, TaskDone, true, true, 4, PriorityAutoSettles},
	}
	for _, c := range cases {
		rank, reason := DecisionPriority(c.kind, c.status, c.hasTask, c.auto)
		if rank != c.wantRank || reason != c.wantReason {
			t.Errorf("%s: got (%d, %q), want (%d, %q)", c.name, rank, reason, c.wantRank, c.wantReason)
		}
	}
}
