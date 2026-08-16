import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { readStoredLocale, resolveLocale } from "../i18n";
import { fetchInbox, subscribeToDecisionEvents, type InboxResponse } from "../lib/api";
import { AttentionTaskTable } from "./AttentionTaskTable";
import { DecisionTable } from "./DecisionTable";
import { GoalCreateForm } from "./GoalCreateForm";
import { GoalTable } from "./GoalTable";
import { AreaLoading, ErrorState } from "./StateMessage";
import { Section } from "./Section";

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: InboxResponse }
  | { kind: "error"; message: string };

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Could not load the inbox.";
}

export function Inbox() {
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const { i18n } = useTranslation();

  useEffect(() => {
    const nav = typeof navigator === "undefined" ? null : navigator.language;
    const next = resolveLocale(readStoredLocale(), nav);
    if (i18n.language !== next) void i18n.changeLanguage(next);
  }, [i18n]);

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", data: await fetchInbox() });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason) });
    }
  }, []);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(() => void load());
  }, [load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const retry = () => void load();

  return (
    <main className="space-y-10">
      <div className="border-b border-line pb-6">
        <p className="font-mono text-xs uppercase tracking-[0.18em] text-accent-700">Inbox</p>
        <h1 className="mt-2 font-display text-3xl font-semibold tracking-tight text-ink-950">Inbox</h1>
        <p className="mt-3 max-w-2xl text-sm leading-6 text-ink-700">
          Review decisions awaiting answers, answered decisions that are not yet applied, tasks needing attention, and active goals.
        </p>
      </div>

      <Section title="Decisions awaiting an answer" count={data?.open_decisions.length}>
        {state.kind === "loading" && <AreaLoading label="Decisions awaiting an answer" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <DecisionTable decisions={data.open_decisions} emptyText="No decisions are waiting for an answer. Answer a decision to move it forward." />}
      </Section>

      <Section title="Answered decisions not yet applied" count={data?.unapplied_decisions.length}>
        {state.kind === "loading" && <AreaLoading label="Answered decisions not yet applied" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <DecisionTable decisions={data.unapplied_decisions} emptyText="No answered decisions are waiting to be applied. Apply an answer when it is ready." />}
      </Section>

      <Section title="Tasks needing attention" count={data?.attention_tasks.length}>
        {state.kind === "loading" && <AreaLoading label="Tasks needing attention" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <AttentionTaskTable tasks={data.attention_tasks} />}
      </Section>

      <Section title="Active goals" count={data?.active_goals.length}>
        {state.kind === "loading" && <AreaLoading label="Active goals" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <GoalTable goals={data.active_goals} />}
        {data && <GoalCreateForm onCreated={load} />}
      </Section>
    </main>
  );
}
