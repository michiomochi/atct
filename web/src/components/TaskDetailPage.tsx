import { Button } from "@cloudflare/kumo/components/button";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { formatDateTime } from "../i18n";
import {
  fetchTask,
  subscribeToDecisionEvents,
  type Decision,
  type DecisionHistoryEntry,
  type TaskDetailResponse,
} from "../lib/api";
import { resolveRouteID } from "../lib/ui";
import { AreaLoading, ErrorState } from "./StateMessage";
import { DecisionAnswerForm } from "./DecisionAnswerForm";
import { DecisionHistoryTable } from "./DecisionHistoryTable";
import { TaskCommitList } from "./TaskCommitList";

interface Props {
  id: string;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: TaskDetailResponse }
  | { kind: "error"; message: string };

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

function displayValue(value: string | undefined, fallback: string): string {
  return value?.trim() || fallback;
}

function taskHistoryFor(taskID: string, history: DecisionHistoryEntry[]): DecisionHistoryEntry[] {
  return history.filter((decision) => decision.task_id === taskID);
}

function taskDecisionsFor(taskID: string, decisions: Decision[]): Decision[] {
  return decisions.filter((decision) => decision.task_id === taskID);
}

export function TaskDetailPage({ id }: Props) {
  const { t, i18n } = useTranslation();
  const pathname = id === "_" && typeof window !== "undefined" ? window.location.pathname : "";
  const resolvedID = resolveRouteID(id, pathname, "/tasks/");
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [updatePending, setUpdatePending] = useState(false);
  const dirtyDecisionIDs = useRef(new Set<string>());
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  const load = useCallback(async () => {
    dirtyDecisionIDs.current.clear();
    setUpdatePending(false);
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", data: await fetchTask(resolvedID) });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason, t("goal.error.load")) });
    }
  }, [resolvedID, t]);

  const handleDecisionEvent = useCallback(() => {
    if (dirtyDecisionIDs.current.size > 0) {
      setUpdatePending(true);
      return;
    }
    void load();
  }, [load]);

  const handleInputStateChange = useCallback((hasInput: boolean, decisionID: string) => {
    const next = new Set(dirtyDecisionIDs.current);
    if (hasInput) {
      next.add(decisionID);
    } else {
      next.delete(decisionID);
    }
    dirtyDecisionIDs.current = next;
  }, []);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(handleDecisionEvent);
  }, [handleDecisionEvent, load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const taskOpenDecisions = data ? taskDecisionsFor(data.task.id, data.open_decisions) : [];
  const taskHistory = data ? taskHistoryFor(data.task.id, data.decision_history) : [];
  const taskCommits = data?.commits ?? [];
  const noValue = t("task.detail.none");
  const retry = () => void load();

  return (
    <main className="min-w-0 max-w-full space-y-10 overflow-x-hidden">
      {data && (
        <>
          <div className="border-b border-line pb-6">
            <a
              className="focus-ring text-sm font-medium text-accent-700 hover:text-accent-600"
              href={`/goals/${encodeURIComponent(data.goal.id)}`}
            >
              {data.goal.project_name ? `${t("goal.project")}: ${data.goal.project_name}` : t("goal.backToDashboard")}
            </a>
            <h1 className="mt-2 font-display text-3xl font-semibold text-ink-950">{data.task.title}</h1>
            {data.task.description.trim() && (
              <section className="mt-5 max-w-3xl">
                <h2 className="font-display text-lg font-semibold text-ink-950">{t("task.detail.description")}</h2>
                <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-ink-700">{data.task.description}</p>
              </section>
            )}
          </div>

          <section className="min-w-0 border-t border-line pt-5" aria-labelledby="task-attributes-heading">
                <h2 id="task-attributes-heading" className="font-display text-lg font-semibold text-ink-950">
              {t("task.detail.attributes")}
            </h2>
            <dl className="mt-6 grid min-w-0 gap-x-6 gap-y-4 border-t border-line pt-5 sm:grid-cols-2">
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase text-ink-700">{t("task.detail.files")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">
                  {displayValue([...data.task.files ?? [], data.task.declare_key].filter(Boolean).join(" · "), noValue)}
                </dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase text-ink-700">{t("task.detail.agent")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(data.task.agent, noValue)}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase text-ink-700">{t("task.detail.claimedRun")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(data.task.claimed_by, noValue)}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase text-ink-700">{t("task.detail.order")}</dt>
                <dd className="mt-1 text-sm text-ink-950">{data.task.order}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase text-ink-700">{t("task.detail.createdAt")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">
                  {displayValue(data.task.created_at ? formatDateTime(locale, data.task.created_at) : undefined, noValue)}
                </dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase text-ink-700">{t("task.detail.updatedAt")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">
                  {displayValue(data.task.updated_at ? formatDateTime(locale, data.task.updated_at) : undefined, noValue)}
                </dd>
              </div>
            </dl>
          </section>

          {taskCommits.length > 0 && (
            <section className="min-w-0 border-t border-line pt-5" aria-labelledby="task-commits-heading">
              <h2 id="task-commits-heading" className="font-display text-lg font-semibold text-ink-950">
                {t("task.detail.commits")}
              </h2>
              <div className="mt-6">
                <TaskCommitList taskID={resolvedID} commits={taskCommits} />
              </div>
            </section>
          )}
        </>
      )}

      {updatePending && (
        <div className="border border-notice-800 bg-notice-100 px-4 py-4 text-sm text-notice-800" role="status" aria-live="polite">
          <p>{t("state.updateAvailable")}</p>
          <Button
            type="button"
            variant="outline"
            className="focus-ring mt-3 px-3 py-2 text-sm font-medium"
            onClick={retry}
          >
            {t("state.fetchLatest")}
          </Button>
        </div>
      )}

      {state.kind === "loading" && <AreaLoading label={t("task.detail.title")} />}
      {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}

      {data && taskOpenDecisions.length > 0 && (
        <section className="min-w-0 border-t border-line pt-5" data-testid="task-answer-form" aria-labelledby="task-answer-heading">
          <h2 id="task-answer-heading" className="font-display text-lg font-semibold text-ink-950">{t("task.detail.answer")}</h2>
          <div className="mt-4 space-y-5">
            {taskOpenDecisions.map((decision) => (
              <DecisionAnswerForm
                key={decision.id}
                decision={decision}
                onUpdated={retry}
                onInputStateChange={handleInputStateChange}
              />
            ))}
          </div>
        </section>
      )}

      {data && taskHistory.length > 0 && (
        <DecisionHistoryTable decisions={taskHistory} omittedCount={data.decision_history_omitted} />
      )}
    </main>
  );
}
