import { Button } from "@cloudflare/kumo/components/button";
import { Dialog } from "@cloudflare/kumo/components/dialog";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  ApiError,
  approveDecision,
  fetchGoal,
  fetchGoalHandoffHistory,
  fetchGoalReviewExchanges,
  rejectDecision,
  subscribeToDecisionEvents,
  withdrawGoal,
  type Decision,
  type Goal,
  type GoalResponse,
} from "../lib/api";
import { formatDateTime } from "../i18n";
import { body, findOpenGoalReview, hasCompletionReport, headline, resolveRouteID, statusLabel, type CompletionReportFields } from "../lib/ui";
import { AreaLoading, ErrorState } from "./StateMessage";
import { GoalDiff } from "./GoalDiff";
import { TaskCommitList } from "./TaskCommitList";
import { TaskTable } from "./TaskTable";
import { HandoffTimeline } from "./HandoffTimeline";
import { ReviewExchanges } from "./ReviewExchanges";
import { UnattachedDecisionList } from "./UnattachedDecisionList";

interface Props {
  id: string;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: GoalDetailData }
  | { kind: "error"; message: string };

interface GoalDetailData {
  goal: GoalResponse;
  goalReview?: Decision;
  unattachedDecisions: Decision[];
}

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

const completionReportFields = [
  { key: "work_done", label: "goal.completion.report.workDone" },
  { key: "now_possible", label: "goal.completion.report.nowPossible" },
  { key: "how_to_verify", label: "goal.completion.report.howToVerify" },
  { key: "surprises", label: "goal.completion.report.surprises" },
  { key: "needs_review", label: "goal.completion.report.needsReview" },
] as const;

function CompletionReport({ goal }: { goal: Goal }) {
  const { t } = useTranslation();
  const structuredReport: CompletionReportFields = {
    work_done: goal.work_done,
    now_possible: goal.now_possible,
    how_to_verify: goal.how_to_verify,
    surprises: goal.surprises,
    needs_review: goal.needs_review,
  };
  const report = !hasCompletionReport(structuredReport) && goal.result_summary.trim() !== ""
    ? { ...structuredReport, work_done: goal.result_summary }
    : structuredReport;

  if (!hasCompletionReport(report)) return null;

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="completion-report" aria-labelledby="completion-report-heading">
      <h2 id="completion-report-heading" className="font-display text-lg font-semibold text-ink-950">{t("goal.completion.report.title")}</h2>
      <div className="mt-4 grid min-w-0 gap-6">
        {completionReportFields.map(({ key, label }) => {
          const value = report[key].trim() ? report[key] : t("goal.completion.report.empty");
          return (
            <section key={key} className="min-w-0 border-t border-line pt-3">
              <h3 className="font-display text-base font-semibold text-ink-950">{t(label)}</h3>
              <p className="mt-2 whitespace-pre-wrap break-words text-base leading-6 text-ink-800">{value}</p>
            </section>
          );
        })}
      </div>
    </section>
  );
}

type GoalReviewAction = "approve" | "reject";

function GoalReview({
  decision,
  onUpdated,
  reason,
  onReasonChange,
}: {
  decision: Decision;
  onUpdated: () => void;
  reason: string;
  onReasonChange: (reason: string) => void;
}) {
  const { t } = useTranslation();
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const reasonID = `goal-review-reason-${decision.id}`;

  async function submit(action: GoalReviewAction) {
    const trimmedReason = reason.trim();
    if (submitting || (action === "reject" && trimmedReason === "")) return;

    setSubmitError(null);
    setConflict(false);
    setSubmitting(true);
    try {
      if (action === "approve") {
        await approveDecision(decision.id);
      } else {
        await rejectDecision(decision.id, trimmedReason);
      }
      onUpdated();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
      } else {
        setSubmitError(errorMessage(error, t("goal.review.error.update")));
      }
    } finally {
      setSubmitting(false);
    }
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const submitter = (event.nativeEvent as SubmitEvent).submitter;
    const action: GoalReviewAction = submitter instanceof HTMLButtonElement && submitter.value === "reject" ? "reject" : "approve";
    void submit(action);
  }

  if (conflict) {
    return (
      <section className="min-w-0 border-t border-line pt-5" data-testid="goal-review" aria-labelledby="goal-review-heading">
        <h2 id="goal-review-heading" className="font-display text-lg font-semibold text-ink-950">{t("goal.review.title")}</h2>
        <div className="mt-4 border border-notice-800 bg-notice-100 px-4 py-4 text-base text-notice-800" role="alert">
          <p>{t("goal.review.conflict")}</p>
          <Button
            type="button"
            variant="outline"
            className="focus-ring mt-3 px-3 py-2 text-base font-medium"
            onClick={onUpdated}
          >
            {t("goal.review.fetchLatest")}
          </Button>
        </div>
      </section>
    );
  }

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="goal-review" aria-labelledby="goal-review-heading">
      <h2 id="goal-review-heading" className="font-display text-lg font-semibold text-ink-950">{t("goal.review.title")}</h2>
      <p className="mt-2 max-w-3xl text-base leading-6 text-ink-700">{t("goal.review.description")}</p>
      <p className="mt-4 max-w-3xl whitespace-pre-wrap break-words text-base leading-6 text-ink-950">{decision.question}</p>
      <form className="mt-4 min-w-0 max-w-3xl border-l-2 border-accent-600 pl-4" onSubmit={handleSubmit} noValidate>
        <label className="mb-3 block text-base text-ink-800" htmlFor={reasonID}>
          {t("goal.review.reason")} <span className="text-ink-500">{t("form.optional")}</span>
          <textarea
            className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-base leading-6 text-ink-950"
            id={reasonID}
            value={reason}
            onChange={(event) => onReasonChange(event.target.value)}
          />
        </label>
        {submitError && <p className="mb-3 text-base text-danger-700" role="alert">{submitError}</p>}
        <div className="flex flex-wrap gap-3">
          <Button
            type="submit"
            value="approve"
            variant="primary"
            disabled={submitting}
            className="focus-ring px-3 py-2 text-base font-medium disabled:cursor-wait disabled:opacity-60"
          >
            {submitting ? t("goal.review.submitting") : t("goal.review.approve")}
          </Button>
          <Button
            type="submit"
            value="reject"
            variant="secondary-destructive"
            disabled={submitting || reason.trim() === ""}
            className="focus-ring px-3 py-2 text-base font-medium disabled:cursor-not-allowed disabled:opacity-60"
          >
            {t("goal.review.reject")}
          </Button>
        </div>
      </form>
    </section>
  );
}

function GoalWithdrawal({ goal, onUpdated }: { goal: Goal; onUpdated: () => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const reasonID = `goal-withdraw-reason-${goal.id}`;

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmedReason = reason.trim();
    if (!trimmedReason || submitting) return;

    setSubmitError(null);
    setSubmitting(true);
    try {
      await withdrawGoal(goal.id, trimmedReason);
      onUpdated();
    } catch (error) {
      setSubmitError(errorMessage(error, t("goal.error.load")));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      {goal.status === "active" && (
        <Dialog.Trigger
          render={(triggerProps) => (
            <Button
              {...triggerProps}
              data-testid="goal-withdraw-trigger"
              type="button"
              variant="secondary-destructive"
              className="focus-ring shrink-0 px-3 py-2 text-base font-medium"
            >
              {t("goal.withdraw.submit")}
            </Button>
          )}
        />
      )}
      <Dialog className="p-6">
        <Dialog.Title className="mb-4 font-display text-xl font-semibold text-ink-950">
          {t("goal.withdraw.title")}
        </Dialog.Title>
        <p className="mb-4 max-w-3xl text-base leading-6 text-ink-700">{t("goal.withdraw.description")}</p>
        <form className="min-w-0 max-w-3xl" onSubmit={handleSubmit} noValidate>
          <label className="mb-3 block text-base text-ink-800" htmlFor={reasonID}>
            {t("goal.withdraw.reason")}
            <textarea
              className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-base leading-6 text-ink-950"
              id={reasonID}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              required
              aria-required="true"
            />
          </label>
          {submitError && <p className="mb-3 text-base text-danger-700" role="alert">{submitError}</p>}
          <div className="flex flex-wrap gap-3">
            <Button
              type="submit"
              variant="secondary-destructive"
              disabled={submitting || reason.trim() === ""}
              className="focus-ring px-3 py-2 text-base font-medium disabled:cursor-not-allowed disabled:opacity-60"
            >
              {t("goal.withdraw.submit")}
            </Button>
            <Dialog.Close
              render={(closeProps) => (
                <Button {...closeProps} type="button" variant="outline" className="focus-ring px-3 py-2 text-base">
                  {t("form.goal.cancel")}
                </Button>
              )}
            />
          </div>
        </form>
      </Dialog>
    </Dialog.Root>
  );
}

function relatedGoalLink(id: string, headline: string) {
  return (
    <a
      className="focus-ring inline-block w-fit max-w-full text-left text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
      href={`/goals/${encodeURIComponent(id)}`}
    >
      <span className="text-clamp-2 block max-w-[32rem] break-words font-medium" title={headline}>
        {headline}
      </span>
    </a>
  );
}

export function GoalDetail({ id }: Props) {
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [goalReviewReason, setGoalReviewReason] = useState("");
  const [updatePending, setUpdatePending] = useState(false);
  const { t, i18n } = useTranslation();
  const pathname = id === "_" && typeof window !== "undefined" ? window.location.pathname : "";
  const resolvedID = resolveRouteID(id, pathname, "/goals/");

  const handleGoalReviewReasonChange = useCallback((reason: string) => {
    setGoalReviewReason(reason);
  }, []);

  const load = useCallback(async () => {
    setUpdatePending(false);
    setGoalReviewReason("");
    setState({ kind: "loading" });
    try {
      const goal = await fetchGoal(resolvedID);
      const goalReview = findOpenGoalReview(goal.unattached_decisions);
      const unattachedDecisions = goal.unattached_decisions.filter(
        (decision) => decision.kind === "decision" && decision.status === "open",
      );
      setState({ kind: "ready", data: { goal, goalReview, unattachedDecisions } });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason, t("goal.error.load")) });
    }
  }, [resolvedID, t]);

  const handleDecisionEvent = useCallback(() => {
    setUpdatePending(true);
  }, []);

  const fetchHandoffHistory = useCallback(
    (handoffID: string, afterID: number) => fetchGoalHandoffHistory(resolvedID, handoffID, afterID),
    [resolvedID],
  );

  const fetchReviewExchanges = useCallback(() => fetchGoalReviewExchanges(resolvedID), [resolvedID]);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(handleDecisionEvent);
  }, [handleDecisionEvent, load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const retry = () => void load();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  const tasks = data?.goal.goal.tasks ?? [];
  const taskCommits = data?.goal.task_commits ?? [];
  const taskDecisionCount = data?.goal.needs_decision.flatMap((task) => task.open_decisions ?? []).length ?? 0;
  const hasAttention = Boolean(data && (data.goalReview || data.unattachedDecisions.length > 0 || taskDecisionCount > 0));
  const goalBody = data ? body(data.goal.goal.content) : "";
  const relatedGoals = data
    ? { derivedFrom: data.goal.derived_from, derived: data.goal.derived_goals, next: data.goal.goal.next_goals }
    : undefined;
  const hasRelatedGoals = Boolean(relatedGoals && (relatedGoals.derivedFrom || relatedGoals.derived.length > 0 || relatedGoals.next.length > 0));

  return (
    <main className="min-w-0 max-w-full space-y-10 overflow-x-hidden px-0.5">
      <div className="border-b border-line pb-6">
        <a className="focus-ring text-base font-medium text-accent-700 hover:text-accent-600" href="/">
          {t("goal.backToDashboard")}
        </a>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-4 sm:flex-nowrap">
          <h1 className="min-w-0 flex-1 break-words font-display text-3xl font-semibold text-ink-950">
            {data ? headline(data.goal.goal.content) : t("goal.title")}
          </h1>
          {data && <GoalWithdrawal goal={data.goal.goal} onUpdated={load} />}
        </div>
        {data && (
          <p className="mt-3 min-w-0 break-words text-base text-ink-700">
            {[
              data.goal.goal.project_name || "-",
              `${t("goal.column.status")}: ${statusLabel(locale, data.goal.goal.status)}`,
              `${t("goal.column.updatedAt")}: ${formatDateTime(locale, data.goal.goal.updated_at)}`,
            ].join(" · ")}
          </p>
        )}
        {data?.goal.goal.project_archived && (
          <p className="mt-3 border border-notice-800 bg-notice-100 px-4 py-3 text-base text-notice-800" role="status">
            {t("goal.projectArchived", { name: data.goal.goal.project_name || "" })}
          </p>
        )}
      </div>

      {updatePending && (
        <div className="border border-notice-800 bg-notice-100 px-4 py-4 text-base text-notice-800" role="status" aria-live="polite">
          <p>{t("state.updateAvailable")}</p>
          <Button
            type="button"
            variant="outline"
            className="focus-ring mt-3 px-3 py-2 text-base font-medium"
            onClick={() => void load()}
          >
            {t("state.fetchLatest")}
          </Button>
        </div>
      )}

      {data && hasAttention && (
        <section className="min-w-0 space-y-6" data-testid="attention" aria-labelledby="attention-heading">
          <h2 id="attention-heading" className="font-display text-xl font-semibold text-ink-950">{t("goal.attention.title")}</h2>
          {data.goalReview && (
            <GoalReview
              decision={data.goalReview}
              onUpdated={load}
              reason={goalReviewReason}
              onReasonChange={handleGoalReviewReasonChange}
            />
          )}
          <UnattachedDecisionList decisions={data.unattachedDecisions} onRefresh={load} />
          {taskDecisionCount > 0 && (
            <p className="max-w-3xl text-base leading-6 text-ink-800">
              {t("goal.attention.taskDecisions", { count: taskDecisionCount })}{" "}
              <a className="focus-ring text-accent-700 underline underline-offset-4" href="#task-list">{t("goal.attention.toTasks")}</a>
            </p>
          )}
        </section>
      )}

      {goalBody && (
        <section className="min-w-0 border-t border-line pt-5" aria-labelledby="goal-body-heading">
          <h2 id="goal-body-heading" className="font-display text-lg font-semibold text-ink-950">{t("goal.body.title")}</h2>
          <p className="mt-2 max-w-3xl whitespace-pre-wrap break-words text-base leading-6 text-ink-800">{goalBody}</p>
        </section>
      )}

      {data && <CompletionReport goal={data.goal.goal} />}

      {data && (
        <section className="min-w-0 space-y-4 border-t border-line pt-5" data-testid="spec-plan" aria-labelledby="spec-plan-heading">
          <h2 id="spec-plan-heading" className="font-display text-lg font-semibold text-ink-950">{t("goal.specPlan.title")}</h2>
          {([{ key: "spec", label: "goal.spec" }, { key: "plan", label: "goal.plan" }] as const).map(({ key, label }) => {
            const text = data.goal.goal[key].trim();
            if (!text) {
              return (
                <p key={key} className="text-base text-ink-700">{t(label)}: {t("goal.requestReport.unset")}</p>
              );
            }
            return (
              <details key={key} className="min-w-0">
                <summary className="focus-ring cursor-pointer text-base font-medium text-ink-950">
                  {t(label)} <span className="text-ink-700">{t("goal.specPlan.lines", { count: text.split("\n").length })}</span>
                </summary>
                <p className="mt-2 max-w-3xl whitespace-pre-wrap break-words text-base leading-6 text-ink-800">{data.goal.goal[key]}</p>
              </details>
            );
          })}
        </section>
      )}

      <section id="task-list" className="min-w-0 border-t border-line pt-5" data-testid="task-list" aria-labelledby="task-list-heading">
        <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
          <h2 id="task-list-heading" className="font-display text-lg font-semibold text-ink-950">{t("goal.tasks.title")}</h2>
          {data && <p className="text-base text-ink-700">{tasks.length}</p>}
        </div>
        <div className="mt-4 min-w-0">
          {state.kind === "loading" && <AreaLoading label={t("goal.tasks.title")} />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && (
            <TaskTable
              tasks={tasks}
              mode="goal"
              onRefresh={load}
              openDecisions={data.goal.needs_decision.flatMap((task) => task.open_decisions ?? [])}
              decisionHistory={data.goal.decision_history}
            />
          )}
        </div>
      </section>

      <GoalDiff goalID={resolvedID} />

      {taskCommits.length > 0 && (
        <section className="min-w-0 border-t border-line pt-5" aria-labelledby="goal-commits-heading">
          <h2 id="goal-commits-heading" className="font-display text-lg font-semibold text-ink-950">
            {t("goal.commits.title")}
          </h2>
          <div className="mt-6 space-y-8">
            {taskCommits.map(({ task_id, task_title, commits }) => (
              <div key={task_id} className="min-w-0">
                <h3 className="font-display text-base font-semibold text-ink-950">
                  <a
                    className="focus-ring inline-block w-fit max-w-full text-left text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                    href={`/tasks/${encodeURIComponent(task_id)}`}
                  >
                    <span className="text-clamp-2 block max-w-[32rem] break-words font-medium" title={task_title}>
                      {task_title}
                    </span>
                  </a>
                </h3>
                <div className="mt-4">
                  <TaskCommitList taskID={task_id} commits={commits} />
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {relatedGoals && hasRelatedGoals && (
        <section className="min-w-0 space-y-6 border-t border-line pt-5" data-testid="related-goals" aria-labelledby="goal-related-heading">
          <h2 id="goal-related-heading" className="font-display text-lg font-semibold text-ink-950">{t("goal.related.title")}</h2>
          {relatedGoals.derivedFrom && (
            <div className="min-w-0">
              <h3 className="font-display text-base font-semibold text-ink-950">{t("goal.derivedFrom.title")}</h3>
              <ul className="mt-3 space-y-3">
                <li>{relatedGoalLink(relatedGoals.derivedFrom.id, relatedGoals.derivedFrom.headline)}</li>
              </ul>
            </div>
          )}
          {relatedGoals.derived.length > 0 && (
            <div className="min-w-0">
              <h3 className="font-display text-base font-semibold text-ink-950">{t("goal.derivedGoals.title")}</h3>
              <ul className="mt-3 space-y-3">
                {relatedGoals.derived.map(({ id, headline }) => <li key={id}>{relatedGoalLink(id, headline)}</li>)}
              </ul>
            </div>
          )}
          {relatedGoals.next.length > 0 && (
            <div className="min-w-0">
              <h3 className="font-display text-base font-semibold text-ink-950">{t("goal.nextGoals.title")}</h3>
              <ul className="mt-3 space-y-3">
                {relatedGoals.next.map(({ id, headline, status }) => (
                  <li key={id} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                    {relatedGoalLink(id, headline)}
                    <span className="text-base text-ink-700">
                      {t("goal.column.status")}: {statusLabel(locale, status)}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </section>
      )}

      {data && <ReviewExchanges fetchHistory={fetchReviewExchanges} />}

      {data && <HandoffTimeline handoffs={data.goal.handoffs ?? []} fetchHistory={fetchHandoffHistory} />}

    </main>
  );
}
