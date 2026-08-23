package mcpshim

const Instructions = "This repository is registered with ATCT.\n" +
	"An active goal is permission to work. Declare tasks with `atct_task_declare`, claim one with `atct_task_claim`, and carry it through to a commit without waiting for approval to begin.\n" +
	"Finishing a task is not a checkpoint: claim the next one and keep going, moving to another active goal when this one has no unclaimed tasks left.\n" +
	"Stop only before what cannot be undone: rewriting history, discarding uncommitted work, deleting files, or publishing off this machine. Ask about those with `atct_decision_ask`.\n" +
	"For a reversible choice, execute the recommendation first, then record it with `atct_decision_ask` using `wait_ms=0` and `default_after_ms=0`; do not wait for an answer, and make any later disagreement a new decision.\n" +
	"For an irreversible choice, omit `default_option` and `default_after_ms`, park it with `wait_ms=0`, and work on unrelated tasks.\n" +
	"Never ask in conversation. \"Tell me how you want to proceed\" reaches no dashboard, carries no default, and stops everything until someone replies.\n" +
	"Open a question with the choice, not the history, and say which option you would take. The same goes for `result_summary`: lead with what the human can now do, not with what you did.\n" +
	"Answers from an earlier session arrive as `orphaned_decisions`; pass each `decision_id` to `atct_decision_poll`.\n" +
	"When the goal is met, call `atct_goal_complete` to request approval.\n" +
	"See the `atct` skill for details."
