---
name: atct
description: Use when working on a goal that a human is tracking - declaring the tasks you plan to do, claiming one before you start, and asking the human for a decision instead of guessing. Also use when you are about to finish and need approval.
---

# ATCT

ATCT records what you are working on and routes your questions to a human's
inbox. Registering tools is not enough; the value comes from calling them at
the right moments.

## Declare before you work

Call `atct_task_declare` with the tasks you intend to do, before doing them.
Pass a stable `idempotency_key` for the batch. Re-declaring the same batch does
not create duplicates, so it is safe after a retry or a context compaction.

## Claim before you start

Call `atct_task_claim` before working on a task. Exactly one run wins a claim.
If the claim fails the task is already owned, so pick another one rather than
working on it anyway.

Release a task by setting it back to `todo` with `atct_task_update`. There is
no separate release tool.

## Keep going

An active goal is permission to work, not a request for a plan. When
`atct_goal_list` or the session context shows one, declare the tasks and start.
Do not wait for the human to approve the plan first: they set the goal, and that
was the approval.

Carry each task through to a commit. The human's attention is for decisions and
for the final approval, not for granting permission at every step.

Finishing a task is not a checkpoint. Claim the next one and keep going. When a
goal has no unclaimed tasks left, move to another active goal instead of
reporting back. Announcing what you will do next and then stopping to be told to
do it wastes the turn that ends the sentence.

Stop before anything that cannot be undone:

- rewriting history: force push, `rebase`, `reset`, `amend`
- discarding uncommitted work: `restore`, `checkout`, `stash`
- deleting files or directories
- publishing off the machine: deploying, sending data to an external service

For those, use `atct_decision_ask` and park.

The test is whether the human can get the previous state back. A commit is
undoable. A force push over work that exists nowhere else is not.

## Ask instead of guessing

Call `atct_decision_ask` when a choice would change the shape of the work and
you cannot settle it from the code. Supply options with a label, a description,
and the consequence of choosing it. `wait_ms` blocks for an answer and parks
when none arrives, so asking does not force you to stall.

Do not ask about things you can determine yourself. An inbox full of trivia
stops being read.

## Apply what you were told

Answers reach you through `atct_decision_poll`. Polling marks the decision
applied, which is how the human can tell their answer landed rather than
hanging. Poll before continuing work that depended on the question.

If a question stopped being relevant, call `atct_decision_withdraw` rather than
leaving it open.

## Finishing

A task cannot become `done` while a decision on it is open. Answer it or
withdraw it first.

Call `atct_goal_complete` when the work is done. It creates a completion
decision for the human to approve or reject; approval closes the goal, and
rejection returns a reason for you to act on.
