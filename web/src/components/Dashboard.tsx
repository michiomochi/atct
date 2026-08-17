import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
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

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

export function Inbox() {
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const { t } = useTranslation();

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", data: await fetchInbox() });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason, t("inbox.error.load")) });
    }
  }, [t]);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(() => void load());
  }, [load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const retry = () => void load();

  return (
    <main className="space-y-10">
      <div className="border-b border-line pb-6">
        <p className="font-mono text-xs uppercase tracking-[0.18em] text-accent-700">{t("inbox.eyebrow")}</p>
        <h1 className="mt-2 font-display text-3xl font-semibold tracking-tight text-ink-950">{t("inbox.title")}</h1>
        <p className="mt-3 max-w-2xl text-sm leading-6 text-ink-700">
          {t("inbox.description")}
        </p>
      </div>

      <Section id="open-decisions" title={t("inbox.openDecisions.title")} count={data?.open_decisions.length}>
        {state.kind === "loading" && <AreaLoading label={t("inbox.openDecisions.title")} />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <DecisionTable decisions={data.open_decisions} emptyText={t("inbox.openDecisions.empty")} />}
      </Section>

      <Section id="unapplied-decisions" title={t("inbox.unapplied.title")} count={data?.unapplied_decisions.length}>
        {state.kind === "loading" && <AreaLoading label={t("inbox.unapplied.title")} />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <DecisionTable decisions={data.unapplied_decisions} emptyText={t("inbox.unapplied.empty")} />}
      </Section>

      <Section id="attention-tasks" title={t("inbox.attention.title")} count={data?.attention_tasks.length}>
        {state.kind === "loading" && <AreaLoading label={t("inbox.attention.title")} />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <AttentionTaskTable tasks={data.attention_tasks} />}
      </Section>

      <Section id="active-goals" title={t("inbox.activeGoals.title")} count={data?.active_goals.length}>
        {state.kind === "loading" && <AreaLoading label={t("inbox.activeGoals.title")} />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <GoalTable goals={data.active_goals} />}
        {data && <GoalCreateForm onCreated={load} />}
      </Section>
    </main>
  );
}
