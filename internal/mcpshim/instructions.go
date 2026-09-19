package mcpshim

const Instructions = "This repository is registered with ATCT.\n" +
	"An active goal is permission to coordinate work. Create the goal's tasks with `atct_task_create`, then delegate each implementation task with `atct_task_handoff_request`.\n" +
	"Finishing a task is not a checkpoint: review its handoff, then delegate the next task and keep going.\n" +
	"For the human-decision rule, see the `atct` skill.\n" +
	"Never ask in conversation. \"Tell me how you want to proceed\" reaches no dashboard, carries no default, and stops everything until someone replies.\n" +
	"Open a question with the choice, not the history, and say which option you would take. The same goes for `result_summary`: lead with what the human can now do, not with what you did.\n" +
	"Answers from an earlier session arrive as `orphaned_decisions`; pass each `decision_id` to `atct_decision_poll`.\n" +
	"When the goal is met, call `atct_goal_complete` to request approval.\n" +
	"See the `atct` skill for details."
