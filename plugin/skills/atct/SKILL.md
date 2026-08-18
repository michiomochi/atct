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

## Close a task the moment it is finished

Call `atct_task_update` with `done` as soon as the work lands, before you claim
anything else. Claiming is only half of the pair; a task nobody closed still
reads as unstarted.

This matters most when the run that did the work is not the run that holds the
claim — an orchestrator delegating to another agent, for example. The delegate
finishes, the orchestrator moves on, and nothing writes the result back. **Then
the dashboard says the work has not begun, and the human plans around that.**
If you delegated, close the task when the delegate reports, not later.

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

## Ask here, not in conversation

`atct_decision_ask` is the only place a question belongs. Saying "let me know how
you want to proceed" in the conversation and then waiting is not asking — it is
stopping. That sentence never reaches the dashboard, leaves no record, carries no
default, and holds every other task hostage until someone happens to reply.

**Bringing the options is part of the question.** "This needs a decision about X"
is not a decision to make; it is work you have not finished. Find out what the
real alternatives are, what each one costs, and put them in the call. If you
cannot yet name two concrete options, you are not ready to ask — go find out.

## Say what happens if nobody answers

`default_option` names the option to take when the deadline in `default_after_ms`
passes with no reply. The human's answer always wins if it arrives first; the
default only fires on silence.

**Set one on every question you can.** A question without a default stops the
work until someone replies, which is the stall this tool exists to remove. The
exception is narrow and comes below: a choice the human could not undo.

**A default belongs on the option that keeps going**, not on the cautious one.
Reading silence as "stop" gives you back the stall you were trying to avoid.

**Only put a default on an option that can be undone.** Nothing checks this —
ATCT cannot tell "proceed with A" from "drop the production database" — so it
rests on you applying the same test as above: can the human get the previous
state back?

If the answer is no, **send the question with no default at all** and wait for
their approval, however long that takes. A deadline on an irreversible choice is
not a safeguard with a timer on it; it is the thing happening anyway, with a
delay. Waiting is the point.

**Waiting is not idling.** Park the question with `wait_ms=0` and go do the
tasks that do not depend on it. The human comes back to a decision that is still
theirs to make and to work that moved forward in the meantime. If every
remaining task depends on this one answer, say so and stop — that is a real
block, and it is worth their knowing about.

Set the deadline to how long this particular question can reasonably wait.
**Do not put thirty minutes on everything.** A deadline shorter than the human's
actual response time takes the decision away from them while appearing to ask.

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
