export const en = {
  "app.name": "ATCT",
  "nav.inbox": "Inbox",
  "locale.label": "Language",
  "locale.en": "English",
  "locale.ja": "日本語",

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

  "state.loadingLabel": "Loading {{label}}",
  "state.retry": "Retry",

  "duration.seconds": "{{value}}s",
  "duration.minutes": "{{value}}m",
  "duration.hours": "{{value}}h",
  "duration.none": "-",
} as const;

export type TranslationKey = keyof typeof en;
