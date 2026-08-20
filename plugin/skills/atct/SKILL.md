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

The title says what to do. The description says the conditions for judging it
done and the assumptions that must hold. Paraphrasing the title in the
description is equivalent to not writing a description.

On 2026-08-20, a task to verify whether a decision is rolled back to open on
approval failure was withdrawn because its assumption was wrong. If that
assumption had been written in the description, a human could have corrected
it when the task was declared.

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

## Write so the answer takes ten seconds

**Open with the choice, not the history.** The human is deciding, not reviewing
your investigation. Put what you need decided in the first sentence, then the
options, then — only if it changes the answer — how you got here.

A question that opens "I implemented X but it turns out Y because Z was created
by W and never reaches V…" makes the reader assemble the decision themselves.
The one that opens "Which of these three should I use for X?" does not.

**Say which one you would pick and why, in one line.** `default_option` already
carries that, but name it in the text too. "I would take the first: it keeps
the plugin-only install working."

**Options carry consequences, not descriptions.** "Use approach A" tells the
reader nothing. "Use approach A — users install nothing extra, but we keep
maintaining the apply path ourselves" lets them choose.

Skip the parts that do not change the decision: file paths, function names,
which commit introduced it. If the reader would decide the same way without a
sentence, cut it.

**This holds for anyone waiting on you, not just the dashboard.** When something
is blocked on your answer — a person, another agent — reply with the answer
first. Your account of how you got it wrong belongs after, or nowhere. Burying a
one-word decision inside a retrospective makes them ask again.

## Report completion in six parts

`atct_goal_complete` takes six fields, and the database rejects a completion
with any of them empty. **Where nothing applies, say so** — writing "none" is
the point, because it separates "there was nothing" from "I did not look."

| Field | What goes in it |
|---|---|
| `work_done` | What you changed |
| `now_possible` | What the human can do that they could not before |
| `how_to_verify` | What to look at to confirm it |
| `surprises` | What turned out differently than expected |
| `needs_review` | What you want them to look at closely |
| `next_steps` | What you left for later, and why |

The approver reads `how_to_verify` and `needs_review` first: these fields say
what to check and what still needs confirmation. Keep `work_done` concise so it
does not bury them.

**`work_done` is the only field about you.** The other five are about them —
what they gained, what to check, what to worry about, what is still open. A
report where all six read like a changelog has answered one question six times.

**`surprises` is where a report earns its keep.** It is the field a writer most
wants to skip and a reviewer most needs. If your change touched the human's data
in a way they did not ask for, that belongs here, not buried in `work_done`.

Each field has a length limit. **A report nobody finishes reading cannot be
approved**, and six short fields beat one long one.

## Name goals after the symptom, not the mechanism

"Attach unattached decisions to the goal detail response" describes the fix.
**"Decisions waiting on you do not show up on the goal page" describes what the
human saw.** They set the goal from the symptom; they will look for it by the
same words.

## Act on reversible choices, ask about irreversible ones

Classify the decision before asking:

- For a reversible choice, execute the recommendation first, then record it with
  `atct_decision_ask` using `wait_ms=0` and `default_after_ms=0`. Do not stop to
  wait for an answer. If the human chooses differently, create a new decision
  to correct it; never overturn a settled decision.
- For an irreversible choice, ask before acting: omit `default_option` and
  `default_after_ms`, and wait for the human's approval. Use `wait_ms=0` to park
  it while doing unrelated work.

The test is whether the human can get the previous state back. A commit is
undoable. A force push over work that exists nowhere else is not.

**A deadline on an irreversible choice is not a safeguard with a timer on it; it
is the thing happening anyway, with a delay.** That is why those get no default
at all. And if every remaining task depends on that one answer, say so and stop —
that is a real block, and it is worth the human knowing about.

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
