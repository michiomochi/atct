package daemon

// nextStepOption is one operation the flow can continue with. When a
// transition has more than one, each carries the condition that selects it, so
// the caller never has to look the branch up.
type nextStepOption struct {
	When string `json:"when,omitempty"`
	Call string `json:"call"`
	Note string `json:"note,omitempty"`
}

// nextStepAfter lists what follows a completed transition, so an agent does not
// re-read doc/execution-flow.md to find its next call. The chart in that
// document is a function of the transition alone.
var nextStepAfter = map[string][]nextStepOption{
	"goal.handoff.request": {{
		Call: "atct_goal_handoff_receive",
		Note: "wake the executor; it receives with this handoff_id and its SessionStart session_key.",
	}},
	"goal.handoff.receive": {{
		Call: "atct_plan_handoff_review_request",
		Note: "design the goal first and save spec and plan with atct_goal_update_request_report.",
	}},

	"plan.handoff.review.request": {{
		Call: "atct_plan_handoff_review_receive",
		Note: "the commander receives the review. Nothing else is left for you: stop your Monitor with TaskStop and wait on Bash with run_in_background running `atct watch --monitor --token <monitor_token> --once`; a Monitor would only expire every 30 minutes and wake you for nothing.",
	}},
	"plan.handoff.review.receive": {
		{When: "the plan is acceptable", Call: "atct_plan_handoff_complete"},
		{When: "the plan needs changes", Call: "atct_plan_handoff_review_reject"},
	},
	"plan.handoff.review.reject": {{
		Call: "atct_plan_handoff_review_reject_receive",
		Note: "the executor receives the rejection.",
	}},
	"plan.handoff.review.reject.receive": {{
		Call: "atct_plan_handoff_review_request",
		Note: "fix the plan, then request review on this same handoff_id. Do not create a new one.",
	}},
	"plan.handoff.complete": {{
		Call: "atct_goal_handoff_review_request",
		Note: "implement and verify, then request the goal review. Tasks are optional and you need not receive the task-create handoff the daemon created.",
	}},

	"task.create_handoff.receive": {{
		Call: "atct_task_create",
		Note: "pass this handoff_id and the tasks you intend to do.",
	}},
	"task.create": {{
		Call: "atct_task_handoff_request",
		Note: "one per task; wake one task worker per task after each request succeeds.",
	}},
	"task.handoff.request": {{
		Call: "atct_task_handoff_receive",
		Note: "wake the task worker; it receives with this handoff_id and task_id. If no other task is left to delegate, do the same: stop your Monitor with TaskStop and wait on Bash with run_in_background running `atct watch --monitor --token <monitor_token> --once`.",
	}},
	"task.handoff.receive": {{
		Call: "atct_task_handoff_review_request",
		Note: "implement and run the verification named in the handoff first.",
	}},

	"task.handoff.review.request": {{
		Call: "atct_task_handoff_review_receive",
		Note: "the executor receives the review.",
	}},
	"task.handoff.review.receive": {
		{When: "the work is acceptable", Call: "atct_task_handoff_complete"},
		{When: "the work needs changes", Call: "atct_task_handoff_review_reject"},
	},
	"task.handoff.review.reject": {{
		Call: "atct_task_handoff_review_reject_receive",
		Note: "the task worker receives the rejection.",
	}},
	"task.handoff.review.reject.receive": {{
		Call: "atct_task_handoff_review_request",
		Note: "fix the work, then request review on this same handoff_id. Do not create a new one.",
	}},
	"task.handoff.complete": {
		{When: "a task of this goal is still undelegated", Call: "atct_task_handoff_request"},
		{When: "every task is done", Call: "atct_goal_handoff_review_request", Note: "close the task workers and commit first."},
	},

	"goal.handoff.review.request": {{
		Call: "atct_goal_handoff_review_receive",
		Note: "the commander receives the review. Nothing else is left for you: stop your Monitor with TaskStop and wait on Bash with run_in_background running `atct watch --monitor --token <monitor_token> --once`; a Monitor would only expire every 30 minutes and wake you for nothing.",
	}},
	"goal.handoff.review.receive": {
		{When: "the goal is acceptable", Call: "atct_goal_review_request", Note: "leave this handoff open; the human reviews next."},
		{When: "the goal needs changes", Call: "atct_goal_handoff_review_reject"},
	},
	"goal.handoff.review.reject": {{
		Call: "atct_goal_handoff_review_reject_receive",
		Note: "the executor receives the rejection, fixes the work, then requests review again on this same handoff.",
	}},
	"goal.handoff.review.reject.receive": {{
		Call: "atct_goal_handoff_review_request",
		Note: "fix the work, then request review on this same handoff_id. Do not create a new one.",
	}},
	"goal.review.request": {
		{When: "the human approves", Call: "atct_goal_review_complete", Note: "merge to main first."},
		{When: "the human rejects", Call: "atct_goal_handoff_review_reject", Note: "pass the reason down on the existing handoff; do not create a new one."},
	},
	"goal.review.complete": {{
		Call: "atct_goal_handoff_request",
		Note: "the goal handoff and the goal closed together. Clean up the worktree and the executor, then take the next goal.",
	}},
}
