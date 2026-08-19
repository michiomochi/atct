import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetchInbox, subscribeToDecisionEvents, type InboxResponse } from "../lib/api";
import { DecisionTable } from "./DecisionTable";
import { GoalCreateForm } from "./GoalCreateForm";
import { GoalTable } from "./GoalTable";
import { AreaLoading, EmptyState, ErrorState } from "./StateMessage";
import { Section } from "./Section";
import { groupGoalsByProject } from "../lib/ui";

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: InboxResponse }
  | { kind: "error"; message: string };

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

export function Dashboard() {
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const { t } = useTranslation();

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", data: await fetchInbox() });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason, t("dashboard.error.load")) });
    }
  }, [t]);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(() => void load());
  }, [load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const projectGroups = data ? groupGoalsByProject(data.active_goals) : undefined;
  const retry = () => void load();

  return (
    <main className="space-y-6">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink-950">{t("dashboard.title")}</h1>
      </div>

      <div className="space-y-10">
        <Section id="open-decisions" title={t("dashboard.waiting.title")} count={data?.open_decisions.length}>
          {state.kind === "loading" && <AreaLoading label={t("dashboard.waiting.title")} />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && <DecisionTable decisions={data.open_decisions} emptyText={t("dashboard.openDecisions.empty")} />}
        </Section>

      </div>

      <Section id="active-goals" title={t("dashboard.projects.title")} count={projectGroups?.length}>
        {state.kind === "loading" && <AreaLoading label={t("dashboard.projects.title")} />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && projectGroups && projectGroups.length === 0 && <EmptyState>{t("dashboard.projects.empty")}</EmptyState>}
        {data && projectGroups && projectGroups.length > 0 && (
          <div className="space-y-8">
            {projectGroups.map(([projectName, goals]) => (
              <div className="space-y-3" key={projectName}>
                <h3 className="font-display text-base font-semibold tracking-tight text-ink-950">{projectName}</h3>
                <GoalTable goals={goals} showProject={false} />
              </div>
            ))}
          </div>
        )}
        {data && <GoalCreateForm onCreated={load} />}
      </Section>
    </main>
  );
}
