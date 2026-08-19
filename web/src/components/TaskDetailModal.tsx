import {
  Dialog,
  DialogClose,
  DialogDescription,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
} from "@cloudflare/kumo/components/dialog";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { formatDateTime } from "../i18n";
import type { Decision, DecisionHistoryEntry, TaskView } from "../lib/api";
import { filterDecisionsByTask } from "../lib/ui";
import { DecisionAnswerForm } from "./DecisionAnswerForm";
import { DecisionHistoryTable } from "./DecisionHistoryTable";

interface Props {
  task: TaskView;
  openDecisions: Decision[];
  decisionHistory: DecisionHistoryEntry[];
  onUpdated: () => void;
  children: ReactNode;
}

function displayValue(value: string | undefined, fallback: string): string {
  return value?.trim() || fallback;
}

export function TaskDetailModal({ task, openDecisions, decisionHistory, onUpdated, children }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  const taskOpenDecisions = filterDecisionsByTask(openDecisions, task.id);
  const taskHistory = filterDecisionsByTask(decisionHistory, task.id);
  const noValue = t("task.detail.none");

  return (
    <DialogRoot>
      <DialogTrigger
        render={(triggerProps) => (
          <button
            {...triggerProps}
            className="focus-ring block min-w-0 max-w-full text-left"
            type="button"
          >
            {children}
          </button>
        )}
      />
      <Dialog className="min-w-0 max-w-3xl p-6" data-testid="task-detail-modal">
        <DialogTitle className="font-display text-2xl font-semibold tracking-tight text-ink-950">
          {task.title}
        </DialogTitle>
        <DialogDescription className="mt-2 text-sm leading-6 text-ink-700">
          {t("task.detail.description")}
        </DialogDescription>

        <dl className="mt-6 grid min-w-0 gap-x-6 gap-y-4 border-t border-line pt-5 sm:grid-cols-2">
          <div className="min-w-0">
            <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("task.detail.files")}</dt>
            <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(task.declare_key, noValue)}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("task.detail.agent")}</dt>
            <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(task.agent, noValue)}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("task.detail.claimedRun")}</dt>
            <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(task.claimed_by, noValue)}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("task.detail.order")}</dt>
            <dd className="mt-1 text-sm text-ink-950">{task.order}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("task.detail.createdAt")}</dt>
            <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(task.created_at ? formatDateTime(locale, task.created_at) : undefined, noValue)}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("task.detail.updatedAt")}</dt>
            <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(task.updated_at ? formatDateTime(locale, task.updated_at) : undefined, noValue)}</dd>
          </div>
        </dl>

        {taskOpenDecisions.length > 0 && (
          <section className="mt-6 min-w-0 border-t border-line pt-5" data-testid="task-answer-form">
            <h2 className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("task.detail.answer")}</h2>
            <div className="mt-4 space-y-5">
              {taskOpenDecisions.map((decision) => (
                <DecisionAnswerForm key={decision.id} decision={decision} onUpdated={onUpdated} />
              ))}
            </div>
          </section>
        )}

        {taskHistory.length > 0 && (
          <div className="mt-6 min-w-0">
            <DecisionHistoryTable decisions={taskHistory} omittedCount={0} />
          </div>
        )}

        <div className="mt-6 flex justify-end border-t border-line pt-5">
          <DialogClose
            render={(closeProps) => (
              <button
                {...closeProps}
                className="focus-ring border border-line bg-surface px-3 py-2 text-sm font-medium text-ink-800 hover:border-ink-500 hover:bg-paper"
                type="button"
              >
                {t("task.detail.close")}
              </button>
            )}
          />
        </div>
      </Dialog>
    </DialogRoot>
  );
}
