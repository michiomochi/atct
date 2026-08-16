export const en = {
  "app.name": "ATCT",
  "nav.inbox": "Inbox",
  "locale.label": "Language",
  "locale.en": "English",
  "locale.ja": "Japanese",

  "inbox.eyebrow": "INBOX",
  "inbox.title": "Inbox",
  "inbox.description":
    "Review decisions awaiting answers, answered decisions that are not yet applied, tasks needing attention, and active goals.",
  "inbox.openDecisions.title": "Decisions awaiting an answer",
  "inbox.openDecisions.empty":
    "No decisions are waiting for an answer. Answer a decision to move it forward.",
  "inbox.unapplied.title": "Answered decisions not yet applied",
  "inbox.unapplied.empty":
    "No answered decisions are waiting to be applied. Apply an answer when it is ready.",
  "inbox.attention.title": "Tasks needing attention",
  "inbox.attention.empty":
    "No tasks are related to an outstanding decision. They will appear here when a decision needs attention.",
  "inbox.activeGoals.title": "Active goals",
  "inbox.activeGoals.empty":
    "No active goals are in progress. Resume work on a goal to see it here.",

  "inbox.error.load": "Could not load the inbox.",

  "decision.caption.list": "Decision list",
  "decision.column.question": "Question",
  "decision.column.status": "Status",
  "decision.column.answer": "Answer",
  "decision.column.answeredBy": "Answered by",
  "decision.column.goal": "Goal",
  "decision.column.createdAt": "Created at",

  "goal.caption.activeList": "Active goal list",
  "goal.column.goal": "Goal",
  "goal.column.status": "Status",
  "goal.column.updatedAt": "Updated at",

  "task.caption.attention": "Tasks needing attention",
  "task.column.task": "Task",
  "task.column.goal": "Goal",
  "task.column.status": "Status",
  "task.column.claimedBy": "Claimed by",
  "task.column.claimDuration": "Claim duration",
  "task.claim.noHolder": "Unclaimed",

  "form.goal.project.label": "Project",
  "form.goal.title.label": "Title",
  "form.goal.description.label": "Description",
  "form.goal.project.placeholder": "Select a project",
  "form.goal.submit": "Create goal",
  "form.goal.cancel": "Cancel",
  "form.goal.action.new": "New goal",
  "form.goal.action.creating": "Creating...",
  "form.goal.noProject":
    "Run atct project add in a repository to register your first project.",
  "form.goal.overload.description":
    "Showing all {{count}} registered projects. Use the selector to choose one.",
  "form.goal.error.load": "Unable to load projects.",
  "form.goal.error.create": "Unable to create goal.",
  "form.goal.error.required": "Select a project and enter a title.",
  "form.goal.error.conflict":
    "The project changed while this goal was being created. Reload projects and try again.",

  "state.loadingLabel": "Loading {{label}}",
  "state.retry": "Retry",

  "duration.seconds": "{{value}}s",
  "duration.minutes": "{{value}}m",
  "duration.hours": "{{value}}h",
  "duration.none": "-",
} as const;

export type TranslationKey = keyof typeof en;
