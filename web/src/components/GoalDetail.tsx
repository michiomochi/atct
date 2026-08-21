import { Button } from "@cloudflare/kumo/components/button";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  ApiError,
  approveDecision,
  fetchGoal,
  rejectDecision,
  subscribeToDecisionEvents,
  withdrawGoal,
  type Decision,
  type Goal,
  type GoalResponse,
} from "../lib/api";
import { formatDateTime } from "../i18n";
import { body, findOpenCompletion, findOpenGoalApproval, hasCompletionReport, headline, resolveRouteID, statusLabel, type CompletionReportFields } from "../lib/ui";
import { AreaLoading, ErrorState } from "./StateMessage";
import { TaskTable } from "./TaskTable";

interface Props {
  id: string;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: GoalDetailData }
  | { kind: "error"; message: string };

interface GoalDetailData {
  goal: GoalResponse;
  completion?: Decision;
  goalApproval?: Decision;
}

type CompletionAction = "approve" | "reject";

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

const completionReportFields = [
  { key: "work_done", label: "goal.completion.report.workDone" },
  { key: "now_possible", label: "goal.completion.report.nowPossible" },
  { key: "how_to_verify", label: "goal.completion.report.howToVerify" },
  { key: "surprises", label: "goal.completion.report.surprises" },
  { key: "needs_review", label: "goal.completion.report.needsReview" },
  { key: "next_steps", label: "goal.completion.report.nextSteps" },
] as const;

function CompletionReport({ goal }: { goal: Goal }) {
  const { t } = useTranslation();
  const structuredReport: CompletionReportFields = {
    work_done: goal.work_done,
    now_possible: goal.now_possible,
    how_to_verify: goal.how_to_verify,
    surprises: goal.surprises,
    needs_review: goal.needs_review,
    next_steps: goal.next_steps,
  };
  const report = !hasCompletionReport(structuredReport) && goal.result_summary.trim() !== ""
    ? { ...structuredReport, work_done: goal.result_summary }
    : structuredReport;

  if (!hasCompletionReport(report)) return null;

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="completion-report" aria-labelledby="completion-report-heading">
      <h2 id="completion-report-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("goal.completion.report.title")}</h2>
      <div className="mt-4 grid min-w-0 gap-6 sm:grid-cols-2">
        {completionReportFields.map(({ key, label }) => {
          const value = report[key].trim() ? report[key] : t("goal.completion.report.empty");
          return (
            <section key={key} className="min-w-0 border-t border-line pt-3">
              <h3 className="font-display text-base font-semibold text-ink-950">{t(label)}</h3>
              <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-6 text-ink-800">{value}</p>
            </section>
          );
        })}
      </div>
    </section>
  );
}

function CompletionApproval({
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

  const reasonID = `completion-reason-${decision.id}`;

  async function submit(action: CompletionAction) {
    setSubmitError(null);
    setConflict(false);

    setSubmitting(true);
    try {
      if (action === "approve") {
        await approveDecision(decision.id);
      } else {
        await rejectDecision(decision.id, reason.trim());
      }
      onUpdated();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
      } else {
        setSubmitError(error instanceof Error ? error.message : t("goal.completion.error.update"));
      }
    } finally {
      setSubmitting(false);
    }
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const submitter = (event.nativeEvent as SubmitEvent).submitter;
    const action: CompletionAction = submitter instanceof HTMLButtonElement && submitter.value === "reject" ? "reject" : "approve";
    void submit(action);
  }

  if (conflict) {
    return (
      <section className="min-w-0 border-t border-line pt-5" data-testid="completion-approval" aria-labelledby="completion-approval-heading">
        <h2 id="completion-approval-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("goal.completion.title")}</h2>
        <div className="mt-4 border border-notice-800 bg-notice-100 px-4 py-4 text-sm text-notice-800" role="alert">
          <p>{t("goal.completion.conflict")}</p>
          <Button
            type="button"
            className="focus-ring mt-3 border border-notice-800 bg-surface px-3 py-2 text-sm font-medium text-notice-800 hover:bg-notice-100"
            onClick={onUpdated}
          >
            {t("goal.completion.fetchLatest")}
          </Button>
        </div>
      </section>
    );
  }

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="completion-approval" aria-labelledby="completion-approval-heading">
      <h2 id="completion-approval-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("goal.completion.title")}</h2>
      <form className="mt-4 min-w-0 max-w-3xl border-l-2 border-accent-600 pl-4" onSubmit={handleSubmit} noValidate>
        <label className="mb-3 block text-sm text-ink-800" htmlFor={reasonID}>
          {t("goal.completion.reason")} <span className="text-ink-500">{t("form.optional")}</span>
          <textarea
            className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-sm leading-6 text-ink-950"
            id={reasonID}
            value={reason}
            onChange={(event) => onReasonChange(event.target.value)}
          />
        </label>
        {submitError && <p className="mb-3 text-sm text-danger-700" role="alert">{submitError}</p>}
        <div className="flex flex-wrap gap-3">
          <Button
            type="submit"
            value="approve"
            disabled={submitting}
            className="focus-ring border border-accent-700 bg-accent-700 px-3 py-2 text-sm font-medium text-white transition hover:bg-accent-600 disabled:cursor-wait disabled:opacity-60"
          >
            {submitting ? t("goal.completion.submitting") : t("goal.completion.approve")}
          </Button>
          <Button
            type="submit"
            value="reject"
            disabled={submitting}
            className="focus-ring border border-danger-700 bg-surface px-3 py-2 text-sm font-medium text-danger-700 transition hover:bg-danger-100 disabled:cursor-wait disabled:opacity-60"
          >
            {t("goal.completion.reject")}
          </Button>
        </div>
      </form>
    </section>
  );
}

type GoalApprovalAction = "approve" | "reject";

function GoalApproval({
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
  const [submitting, setSubmitting] = useState(false);

  const reasonID = `goal-approval-reason-${decision.id}`;

  async function submit(action: GoalApprovalAction) {
    const trimmedReason = reason.trim();
    if (submitting || (action === "reject" && trimmedReason === "")) return;

    setSubmitError(null);
    setSubmitting(true);
    try {
      if (action === "approve") {
        await approveDecision(decision.id);
      } else {
        await rejectDecision(decision.id, trimmedReason);
      }
      onUpdated();
    } catch (error) {
      setSubmitError(errorMessage(error, t("goal.error.load")));
    } finally {
      setSubmitting(false);
    }
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const submitter = (event.nativeEvent as SubmitEvent).submitter;
    const action: GoalApprovalAction = submitter instanceof HTMLButtonElement && submitter.value === "reject" ? "reject" : "approve";
    void submit(action);
  }

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="goal-approval" aria-labelledby="goal-approval-heading">
      <h2 id="goal-approval-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("goal.approval.title")}</h2>
      <p className="mt-2 max-w-3xl text-sm leading-6 text-ink-700">{t("goal.approval.description")}</p>
      <form className="mt-4 min-w-0 max-w-3xl border-l-2 border-accent-600 pl-4" onSubmit={handleSubmit} noValidate>
        <label className="mb-3 block text-sm text-ink-800" htmlFor={reasonID}>
          {t("goal.completion.reason")}
          <textarea
            className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-sm leading-6 text-ink-950"
            id={reasonID}
            value={reason}
            onChange={(event) => onReasonChange(event.target.value)}
            required
            aria-required="true"
          />
        </label>
        {submitError && <p className="mb-3 text-sm text-danger-700" role="alert">{submitError}</p>}
        <div className="flex flex-wrap gap-3">
          <Button
            type="submit"
            value="approve"
            disabled={submitting}
            className="focus-ring border border-accent-700 bg-accent-700 px-3 py-2 text-sm font-medium text-white transition hover:bg-accent-600 disabled:cursor-wait disabled:opacity-60"
          >
            {submitting ? t("goal.completion.submitting") : t("goal.approval.approve")}
          </Button>
          <Button
            type="submit"
            value="reject"
            disabled={submitting || reason.trim() === ""}
            className="focus-ring border border-danger-700 bg-surface px-3 py-2 text-sm font-medium text-danger-700 transition hover:bg-danger-100 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {t("goal.approval.reject")}
          </Button>
        </div>
      </form>
    </section>
  );
}

function GoalWithdrawal({ goal, onUpdated }: { goal: Goal; onUpdated: () => void }) {
  const { t } = useTranslation();
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
    <section className="min-w-0 border-t border-line pt-5" data-testid="goal-withdraw" aria-labelledby="goal-withdraw-heading">
      <h2 id="goal-withdraw-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("goal.withdraw.title")}</h2>
      <p className="mt-2 max-w-3xl text-sm leading-6 text-ink-700">{t("goal.withdraw.description")}</p>
      <form className="mt-4 min-w-0 max-w-3xl border-l-2 border-danger-700 pl-4" onSubmit={handleSubmit} noValidate>
        <label className="mb-3 block text-sm text-ink-800" htmlFor={reasonID}>
          {t("goal.withdraw.reason")}
          <textarea
            className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-sm leading-6 text-ink-950"
            id={reasonID}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            required
            aria-required="true"
          />
        </label>
        {submitError && <p className="mb-3 text-sm text-danger-700" role="alert">{submitError}</p>}
        <Button
          type="submit"
          disabled={submitting || reason.trim() === ""}
          className="focus-ring border border-danger-700 bg-surface px-3 py-2 text-sm font-medium text-danger-700 transition hover:bg-danger-100 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {t("goal.withdraw.submit")}
        </Button>
      </form>
    </section>
  );
}

export function GoalDetail({ id }: Props) {
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [completionReason, setCompletionReason] = useState("");
  const [goalApprovalReason, setGoalApprovalReason] = useState("");
  const [updatePending, setUpdatePending] = useState(false);
  const completionReasonRef = useRef("");
  const goalApprovalReasonRef = useRef("");
  const { t, i18n } = useTranslation();
  const pathname = id === "_" && typeof window !== "undefined" ? window.location.pathname : "";
  const resolvedID = resolveRouteID(id, pathname, "/goals/");

  const handleCompletionReasonChange = useCallback((reason: string) => {
    completionReasonRef.current = reason;
    setCompletionReason(reason);
  }, []);

  const handleGoalApprovalReasonChange = useCallback((reason: string) => {
    goalApprovalReasonRef.current = reason;
    setGoalApprovalReason(reason);
  }, []);

  const load = useCallback(async () => {
    setUpdatePending(false);
    completionReasonRef.current = "";
    setCompletionReason("");
    goalApprovalReasonRef.current = "";
    setGoalApprovalReason("");
    setState({ kind: "loading" });
    try {
      const goal = await fetchGoal(resolvedID);
      const completion = findOpenCompletion(goal.unattached_decisions);
      const goalApproval = findOpenGoalApproval(goal.unattached_decisions);
      setState({ kind: "ready", data: { goal, completion, goalApproval } });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason, t("goal.error.load")) });
    }
  }, [resolvedID, t]);

  const handleDecisionEvent = useCallback(() => {
    if (completionReasonRef.current.trim() !== "" || goalApprovalReasonRef.current.trim() !== "") {
      setUpdatePending(true);
      return;
    }
    void load();
  }, [load]);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(handleDecisionEvent);
  }, [handleDecisionEvent, load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const retry = () => void load();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  const tasks = data?.goal.goal.tasks ?? [];

  return (
    <main className="min-w-0 max-w-full space-y-10 overflow-x-hidden">
      <div className="border-b border-line pb-6">
        <a className="focus-ring text-sm font-medium text-accent-700 hover:text-accent-900" href="/">
          {t("goal.backToDashboard")}
        </a>
        <h1 className="mt-2 font-display text-3xl font-semibold tracking-tight text-ink-950">
          {data ? headline(data.goal.goal.content) : t("goal.title")}
        </h1>
        {data && body(data.goal.goal.content) && <p className="mt-3 max-w-3xl whitespace-pre-wrap text-sm leading-6 text-ink-700">{body(data.goal.goal.content)}</p>}
        {data && (
          <dl className="mt-5 grid min-w-0 gap-x-6 gap-y-3 border-t border-line pt-4 sm:grid-cols-3">
            <div className="min-w-0">
              <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("goal.project")}</dt>
              <dd className="mt-1 break-words text-sm text-ink-950">{data.goal.goal.project_name || "-"}</dd>
            </div>
            <div className="min-w-0">
              <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("goal.column.status")}</dt>
              <dd className="mt-1 break-words text-sm text-ink-950">{statusLabel(locale, data.goal.goal.status)}</dd>
            </div>
            <div className="min-w-0">
              <dt className="text-xs font-semibold uppercase tracking-wide text-ink-600">{t("goal.column.updatedAt")}</dt>
              <dd className="mt-1 break-words text-sm text-ink-950">{formatDateTime(locale, data.goal.goal.updated_at)}</dd>
            </div>
          </dl>
        )}
      </div>

      {updatePending && (
        <div className="border border-notice-800 bg-notice-100 px-4 py-4 text-sm text-notice-800" role="status" aria-live="polite">
          <p>{t("state.updateAvailable")}</p>
          <Button
            type="button"
            className="focus-ring mt-3 border border-notice-800 bg-surface px-3 py-2 text-sm font-medium text-notice-800 hover:bg-notice-100"
            onClick={() => void load()}
          >
            {t("state.fetchLatest")}
          </Button>
        </div>
      )}

      {data && <CompletionReport goal={data.goal.goal} />}

      {data?.completion && (
        <CompletionApproval
          decision={data.completion}
          onUpdated={load}
          reason={completionReason}
          onReasonChange={handleCompletionReasonChange}
        />
      )}

      {data?.goal.goal.status === "proposed" && data.goalApproval && (
        <GoalApproval
          decision={data.goalApproval}
          onUpdated={load}
          reason={goalApprovalReason}
          onReasonChange={handleGoalApprovalReasonChange}
        />
      )}

      <section className="min-w-0 border-t border-line pt-5" data-testid="task-list" aria-labelledby="task-list-heading">
        <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
          <h2 id="task-list-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">{t("goal.tasks.title")}</h2>
          {data && <p className="text-sm text-ink-600">{tasks.length}</p>}
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

      {data?.goal.goal.status === "active" && <GoalWithdrawal goal={data.goal.goal} onUpdated={load} />}
    </main>
  );
}
