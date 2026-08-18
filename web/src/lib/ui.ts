import type { Locale } from "../i18n";
import { en, type TranslationKey } from "../i18n/en";
import { ja } from "../i18n/ja";

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

export interface AnswerInput {
  answer_label: string;
  answer_text: string;
}

export type AnswerErrors = Partial<Record<keyof AnswerInput, string>>;

export function validateAnswer(input: AnswerInput): AnswerErrors {
  const errors: AnswerErrors = {};
  const hasLabel = input.answer_label.trim().length > 0;
  const hasText = input.answer_text.trim().length > 0;

  if (!hasLabel && !hasText) {
    const message = "Enter a label or answer text.";
    errors.answer_label = message;
    errors.answer_text = message;
  }

  return errors;
}

export interface CompletionLike {
  kind: string;
  status: string;
}

export function findOpenCompletion<T extends CompletionLike>(decisions: T[]): T | undefined {
  return decisions.find((decision) => decision.kind === "completion" && decision.status === "open");
}

export function encodePathSegment(value: string): string {
  return encodeURIComponent(value);
}

export function resolveGoalID(id: string, pathname: string): string {
  if (id !== "_") return id;

  const segment = pathname.slice("/goals/".length).split("/", 1)[0] ?? "";
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

const statusKeys: Record<string, TranslationKey> = {
  todo: "status.task.todo",
  doing: "status.task.doing",
  done: "status.task.done",
  blocked: "status.task.blocked",
  active: "status.task.active",
  completed: "status.task.completed",
  open: "status.decision.open",
  answered: "status.decision.answered",
  applied: "status.decision.applied",
  approved: "status.decision.approved",
  rejected: "status.decision.rejected",
  withdrawn: "status.decision.withdrawn",
};

const decisionKindKeys: Record<string, TranslationKey> = {
  decision: "decision.kind.decision",
  completion: "decision.kind.completion",
};

function localized(key: TranslationKey, locale: Locale): string {
  return (locale === "ja" ? ja : en)[key];
}

export function statusLabel(locale: Locale, status: string): string {
  const key = statusKeys[status];
  return key ? localized(key, locale) : status;
}

export function decisionKindLabel(locale: Locale, kind: string): string {
  const key = decisionKindKeys[kind];
  return key ? localized(key, locale) : kind;
}

export function decisionSettlementLabel(locale: Locale, settledByDefault: boolean): string | undefined {
  return settledByDefault ? localized("decision.settledByDefault", locale) : undefined;
}

export function decisionRecommendationLabel(
  locale: Locale,
  defaultOption: string | undefined,
  optionLabel?: string,
): string | undefined {
  if (!defaultOption || (optionLabel !== undefined && optionLabel !== defaultOption)) return undefined;
  return localized("decision.recommended", locale);
}

export function decisionAutoSettlementSeconds(defaultAfterMs: number | undefined): number | undefined {
  if (typeof defaultAfterMs !== "number" || !Number.isFinite(defaultAfterMs) || defaultAfterMs <= 0) return undefined;
  return Math.max(1, Math.ceil(defaultAfterMs / 1000));
}
