import { Button } from "@cloudflare/kumo/components/button";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { formatDateTime } from "../i18n";
import {
  fetchTask,
  fetchTaskCommitDiff,
  subscribeToDecisionEvents,
  type Decision,
  type DecisionHistoryEntry,
  type TaskCommitDiff,
  type TaskDetailResponse,
} from "../lib/api";
import { resolveRouteID } from "../lib/ui";
import { AreaLoading, ErrorState } from "./StateMessage";
import { DecisionAnswerForm } from "./DecisionAnswerForm";
import { DecisionHistoryTable } from "./DecisionHistoryTable";

interface Props {
  id: string;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: TaskDetailResponse }
  | { kind: "error"; message: string };

type CommitDiffState =
  | { kind: "loading" }
  | { kind: "ready"; data: TaskCommitDiff }
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
  const [commitDiffStates, setCommitDiffStates] = useState<Record<string, CommitDiffState>>({});
  const commitDiffStatesRef = useRef<Record<string, CommitDiffState>>({});
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  const setCommitDiffState = useCallback((sha: string, next: CommitDiffState) => {
    const nextStates = { ...commitDiffStatesRef.current, [sha]: next };
    commitDiffStatesRef.current = nextStates;
    setCommitDiffStates(nextStates);
  }, []);

  const loadCommitDiff = useCallback(
    async (sha: string) => {
      if (commitDiffStatesRef.current[sha]) {
        return;
      }
      setCommitDiffState(sha, { kind: "loading" });
      try {
        const diff = await fetchTaskCommitDiff(resolvedID, sha);
        setCommitDiffState(sha, { kind: "ready", data: diff });
      } catch (reason) {
        setCommitDiffState(sha, { kind: "error", message: errorMessage(reason, t("goal.error.load")) });
      }
    },
    [resolvedID, setCommitDiffState, t],
  );

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

  useEffect(() => {
    commitDiffStatesRef.current = {};
    setCommitDiffStates({});
  }, [resolvedID]);

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
            <h1 className="mt-2 font-display text-3xl font-semibold tracking-tight text-ink-950">{data.task.title}</h1>
            {data.task.description.trim() && (
              <section className="mt-5 max-w-3xl">
                <h2 className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("task.detail.description")}</h2>
                <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-ink-700">{data.task.description}</p>
              </section>
            )}
          </div>

          <section className="min-w-0 border-t border-line pt-5" aria-labelledby="task-attributes-heading">
            <h2 id="task-attributes-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">
              {t("task.detail.attributes")}
            </h2>
            <dl className="mt-6 grid min-w-0 gap-x-6 gap-y-4 border-t border-line pt-5 sm:grid-cols-2">
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase tracking-wide text-ink-700">{t("task.detail.files")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">
                  {displayValue([...data.task.files ?? [], data.task.declare_key].filter(Boolean).join(" · "), noValue)}
                </dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase tracking-wide text-ink-700">{t("task.detail.agent")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(data.task.agent, noValue)}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase tracking-wide text-ink-700">{t("task.detail.claimedRun")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">{displayValue(data.task.claimed_by, noValue)}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase tracking-wide text-ink-700">{t("task.detail.order")}</dt>
                <dd className="mt-1 text-sm text-ink-950">{data.task.order}</dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase tracking-wide text-ink-700">{t("task.detail.createdAt")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">
                  {displayValue(data.task.created_at ? formatDateTime(locale, data.task.created_at) : undefined, noValue)}
                </dd>
              </div>
              <div className="min-w-0">
                <dt className="text-xs font-semibold uppercase tracking-wide text-ink-700">{t("task.detail.updatedAt")}</dt>
                <dd className="mt-1 break-words text-sm text-ink-950">
                  {displayValue(data.task.updated_at ? formatDateTime(locale, data.task.updated_at) : undefined, noValue)}
                </dd>
              </div>
            </dl>
          </section>

          {taskCommits.length > 0 && (
            <section className="min-w-0 border-t border-line pt-5" aria-labelledby="task-commits-heading">
              <h2 id="task-commits-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">
                {t("task.detail.commits")}
              </h2>
              <ul className="mt-6 space-y-4">
                {taskCommits.map((commit) => {
                  const diffState = commitDiffStates[commit.sha];

                  return (
                    <li key={commit.sha} className="min-w-0 border-t border-line pt-4 first:border-t-0 first:pt-0">
                    <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-sm">
                      <span className="font-mono text-ink-950">{commit.short_sha}</span>
                      <span className="min-w-0 break-words text-ink-950">{commit.subject}</span>
                    </div>
                    <div className="mt-2 flex flex-wrap items-baseline gap-3 text-sm text-ink-700">
                      <span className="whitespace-nowrap">
                        {commit.files_changed} {t("task.detail.commitFiles")} · +{commit.insertions} −{commit.deletions}
                      </span>
                      {!commit.in_history && <span className="text-accent-700">{t("task.detail.commitMissing")}</span>}
                    </div>
                    <details
                      className="mt-4 min-w-0"
                      onToggle={(event) => {
                        if (event.currentTarget.open) {
                          void loadCommitDiff(commit.sha);
                        }
                      }}
                    >
                      <summary className="focus-ring cursor-pointer text-sm font-medium text-accent-700 hover:text-accent-600">
                        {t("task.detail.commitDiff")}
                      </summary>
                      {diffState?.kind === "loading" && (
                        <AreaLoading label={t("task.detail.commitDiff")} />
                      )}
                      {diffState?.kind === "error" && (
                        <p className="mt-4 break-words text-sm text-danger-700" role="alert">
                          {diffState.message}
                        </p>
                      )}
                      {diffState?.kind === "ready" &&
                        (diffState.data.in_history ? (
                          <div className="mt-4 min-w-0 max-w-full space-y-4">
                            <ul className="min-w-0 space-y-2 text-sm text-ink-700">
                              {diffState.data.files.map((file) => (
                                <li key={file.path} className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
                                  <span className="min-w-0 break-words font-mono">{file.path}</span>
                                  {file.binary ? (
                                    <span className="text-ink-700">{t("task.detail.commitDiffBinary")}</span>
                                  ) : (
                                    <span className="whitespace-nowrap">
                                      +{file.insertions} −{file.deletions}
                                    </span>
                                  )}
                                </li>
                              ))}
                            </ul>
                            <pre className="min-w-0 max-w-full overflow-x-auto whitespace-pre rounded border border-line bg-surface p-4 font-mono text-xs leading-5 text-ink-800">
                              {diffState.data.body}
                            </pre>
                            {diffState.data.omitted_lines > 0 && (
                              <p className="text-sm text-ink-700">
                                {t("task.detail.commitDiffOmitted", {
                                  count: diffState.data.omitted_lines,
                                })}
                              </p>
                            )}
                      </div>
                        ) : (
                          <p className="mt-4 text-sm text-ink-700">{t("task.detail.commitDiffEmpty")}</p>
                        ))}
                    </details>
                    </li>
                  );
                })}
              </ul>
            </section>
          )}
        </>
      )}

      {updatePending && (
        <div className="border border-notice-800 bg-notice-100 px-4 py-4 text-sm text-notice-800" role="status" aria-live="polite">
          <p>{t("state.updateAvailable")}</p>
          <Button
            type="button"
            className="focus-ring mt-3 border border-notice-800 bg-surface px-3 py-2 text-sm font-medium text-notice-800 hover:bg-notice-100"
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
          <h2 id="task-answer-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("task.detail.answer")}</h2>
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
