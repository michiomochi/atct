export const DECISION_EVENT_NAMES = [
  "decision.created",
  "decision.answered",
  "decision.withdrawn",
  "decision.applied",
  "decision.approved",
  "decision.rejected",
] as const;

export type DecisionEventName = (typeof DECISION_EVENT_NAMES)[number];

export function isDecisionEventName(value: string): value is DecisionEventName {
  return (DECISION_EVENT_NAMES as readonly string[]).includes(value);
}

export function formatHeldFor(totalSeconds: number): string {
  const seconds = Math.max(0, Math.floor(Number.isFinite(totalSeconds) ? totalSeconds : 0));
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  const remainingSeconds = seconds % 60;

  if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
  if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  if (minutes > 0) return remainingSeconds > 0 ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`;
  return `${remainingSeconds}s`;
}

export interface AnswerInput {
  answer_label: string;
  answer_text: string;
  answered_by: string;
}

export type AnswerErrors = Partial<Record<keyof AnswerInput, string>>;

export function validateAnswer(input: AnswerInput): AnswerErrors {
  const errors: AnswerErrors = {};
  const hasLabel = input.answer_label.trim().length > 0;
  const hasText = input.answer_text.trim().length > 0;

  if (input.answered_by.trim().length === 0) {
    errors.answered_by = "回答者を入力してください。";
  }

  if (!hasLabel && !hasText) {
    const message = "ラベルまたは回答文を入力してください。";
    errors.answer_label = message;
    errors.answer_text = message;
  }

  return errors;
}

export function encodePathSegment(value: string): string {
  return encodeURIComponent(value);
}

export function formatDate(value: string | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("ja-JP", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

export function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    todo: "未着手",
    doing: "進行中",
    done: "完了",
    blocked: "停止中",
    open: "判断待ち",
    answered: "回答済み",
    applied: "適用済み",
    approved: "承認済み",
    rejected: "却下済み",
    withdrawn: "取り下げ",
    active: "進行中",
    completed: "完了",
  };
  return labels[status] ?? status;
}
