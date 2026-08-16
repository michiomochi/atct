import { Button } from "@cloudflare/kumo/components/button";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { readStoredLocale, resolveLocale } from "../i18n";
import {
  ApiError,
  approveCompletion,
  fetchGoal,
  fetchInbox,
  rejectCompletion,
  subscribeToDecisionEvents,
  type Decision,
  type Goal,
  type GoalResponse,
} from "../lib/api";
import {
  findOpenCompletion,
  resolveGoalID,
  validateCompletion,
  type CompletionErrors,
} from "../lib/ui";
import { NeedsDecisionList } from "./NeedsDecisionList";
import { AreaLoading, ErrorState } from "./StateMessage";
import { Section } from "./Section";
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
}

type CompletionAction = "approve" | "reject";

const ANSWERED_BY_KEY = "atct.answered_by";

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Could not load the goal.";
}

function CompletionApproval({ goal, decision, onUpdated }: { goal: Goal; decision: Decision; onUpdated: () => void }) {
  const [answeredBy, setAnsweredBy] = useState("");
  const [reason, setReason] = useState("");
  const [errors, setErrors] = useState<CompletionErrors>({});
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    const saved = window.localStorage.getItem(ANSWERED_BY_KEY);
    if (saved) setAnsweredBy(saved);
  }, []);

  const answeredByID = `completion-answered-by-${decision.id}`;
  const reasonID = `completion-reason-${decision.id}`;

  function updateAnsweredBy(value: string) {
    setAnsweredBy(value);
    window.localStorage.setItem(ANSWERED_BY_KEY, value);
  }

  async function submit(action: CompletionAction) {
    setSubmitError(null);
    setConflict(false);

    const nextErrors = validateCompletion({ answered_by: answeredBy });
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    setSubmitting(true);
    try {
      if (action === "approve") {
        await approveCompletion(decision.id, answeredBy.trim());
      } else {
        await rejectCompletion(decision.id, answeredBy.trim(), reason.trim());
      }
      onUpdated();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
      } else {
        setSubmitError(error instanceof Error ? error.message : "Could not update the completion decision.");
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
      <section className="border-t border-line pt-5" data-testid="completion-approval" aria-labelledby="completion-approval-heading">
        <h2 id="completion-approval-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">Completion awaiting approval</h2>
        <div className="mt-4 border border-notice-800 bg-notice-100 px-4 py-4 text-sm text-notice-800" role="alert">
          <p>This completion has already been reviewed in another tab or by another person.</p>
          <Button
            type="button"
            className="focus-ring mt-3 border border-notice-800 bg-surface px-3 py-2 text-sm font-medium text-notice-800 hover:bg-notice-100"
            onClick={onUpdated}
          >
            Fetch the latest decision
          </Button>
        </div>
      </section>
    );
  }

  return (
    <section className="border-t border-line pt-5" data-testid="completion-approval" aria-labelledby="completion-approval-heading">
      <h2 id="completion-approval-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">Completion awaiting approval</h2>
      <p className="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-ink-800">{goal.result_summary || "The agent did not provide a result summary."}</p>
      <form className="mt-4 max-w-3xl border-l-2 border-accent-600 pl-4" onSubmit={handleSubmit} noValidate>
        <label className="mb-3 block text-sm text-ink-800" htmlFor={answeredByID}>
          Reviewed by <span className="text-danger-700">(required)</span>
          <input
            className="focus-ring mt-1 block w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
            id={answeredByID}
            value={answeredBy}
            onChange={(event) => updateAnsweredBy(event.target.value)}
            aria-invalid={Boolean(errors.answered_by)}
            aria-describedby={errors.answered_by ? `${answeredByID}-error` : undefined}
            required
          />
          {errors.answered_by && <span className="mt-1 block text-xs text-danger-700" id={`${answeredByID}-error`}>{errors.answered_by}</span>}
        </label>
        <label className="mb-3 block text-sm text-ink-800" htmlFor={reasonID}>
          Rejection reason <span className="text-ink-500">(optional)</span>
          <textarea
            className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-sm leading-6 text-ink-950"
            id={reasonID}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
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
            {submitting ? "Submitting..." : "Approve"}
          </Button>
          <Button
            type="submit"
            value="reject"
            disabled={submitting}
            className="focus-ring border border-danger-700 bg-surface px-3 py-2 text-sm font-medium text-danger-700 transition hover:bg-danger-100 disabled:cursor-wait disabled:opacity-60"
          >
            Reject
          </Button>
        </div>
      </form>
    </section>
  );
}

export function GoalDetail({ id }: Props) {
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const { i18n } = useTranslation();
  const pathname = id === "_" && typeof window !== "undefined" ? window.location.pathname : "";
  const resolvedID = resolveGoalID(id, pathname);

  useEffect(() => {
    const nav = typeof navigator === "undefined" ? null : navigator.language;
    const next = resolveLocale(readStoredLocale(), nav);
    if (i18n.language !== next) void i18n.changeLanguage(next);
  }, [i18n]);

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      const [goal, inbox] = await Promise.all([fetchGoal(resolvedID), fetchInbox()]);
      const completion = findOpenCompletion(inbox.open_decisions.filter((decision) => decision.goal_id === goal.goal.id));
      setState({ kind: "ready", data: { goal, completion } });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason) });
    }
  }, [resolvedID]);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(() => void load());
  }, [load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const retry = () => void load();

  return (
    <main className="space-y-10">
      <div className="border-b border-line pb-6">
        <a className="focus-ring text-sm font-medium text-accent-700 hover:text-accent-900" href="/">
          Back to inbox
        </a>
        <p className="mt-6 font-mono text-xs uppercase tracking-[0.18em] text-accent-700">Goal details</p>
        <h1 className="mt-2 font-display text-3xl font-semibold tracking-tight text-ink-950">
          {data?.goal.goal.title ?? "Goal details"}
        </h1>
        {data?.goal.goal.description && <p className="mt-3 max-w-3xl whitespace-pre-wrap text-sm leading-6 text-ink-700">{data.goal.goal.description}</p>}
        {data?.goal.goal.status && <p className="mt-3 text-sm text-ink-500">Status: {data.goal.goal.status}</p>}
      </div>

      {data?.completion && <CompletionApproval goal={data.goal.goal} decision={data.completion} onUpdated={load} />}

      <div className="grid gap-10 xl:grid-cols-3">
        <Section title="Now" count={data?.goal.now.length}>
          {state.kind === "loading" && <AreaLoading label="Now" />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && <TaskTable tasks={data.goal.now} mode="now" onRefresh={load} />}
        </Section>

        <Section title="Needs decision" count={data?.goal.needs_decision.length}>
          {state.kind === "loading" && <AreaLoading label="Needs decision" />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && <NeedsDecisionList tasks={data.goal.needs_decision} onRefresh={load} />}
        </Section>

        <Section title="Next" count={data?.goal.next.length}>
          {state.kind === "loading" && <AreaLoading label="Next" />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && <TaskTable tasks={data.goal.next} mode="next" onRefresh={load} />}
        </Section>
      </div>
    </main>
  );
}
