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
