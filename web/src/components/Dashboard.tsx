import { Button } from "@cloudflare/kumo/components/button";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetchInbox, subscribeToDecisionEvents, type InboxResponse } from "../lib/api";
import { DecisionTable } from "./DecisionTable";
import { GoalTable } from "./GoalTable";
import { ProposedGoalTable } from "./ProposedGoalTable";
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
  const [updatePending, setUpdatePending] = useState(false);
  const goalCreateDirtyRef = useRef(false);
  const { t } = useTranslation();

  const load = useCallback(async () => {
    setUpdatePending(false);
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", data: await fetchInbox() });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason, t("dashboard.error.load")) });
    }
  }, [t]);

  const handleDecisionEvent = useCallback(() => {
    if (goalCreateDirtyRef.current) {
      setUpdatePending(true);
      return;
    }
    void load();
  }, [load]);

  const handleGoalCreateDirtyChange = useCallback((event: Event) => {
    goalCreateDirtyRef.current = (event as CustomEvent<boolean>).detail === true;
  }, []);

  const handleGoalCreated = useCallback(() => {
    void load();
  }, [load]);

  useEffect(() => {
    void load();
    window.addEventListener("atct:form-dirty", handleGoalCreateDirtyChange);
    window.addEventListener("atct:goal-created", handleGoalCreated);
    const unsubscribe = subscribeToDecisionEvents(handleDecisionEvent);
    return () => {
      window.removeEventListener("atct:form-dirty", handleGoalCreateDirtyChange);
      window.removeEventListener("atct:goal-created", handleGoalCreated);
      unsubscribe();
    };
  }, [handleDecisionEvent, handleGoalCreateDirtyChange, handleGoalCreated, load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const projectGroups = data ? groupGoalsByProject(data.active_goals) : undefined;
  const proposedGoals = data?.proposed_goals ?? [];
  const retry = () => void load();

  return (
    <main className="space-y-6">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink-950">{t("dashboard.title")}</h1>
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

      <div className="space-y-10">
        {proposedGoals.length > 0 && (
          <Section id="proposed-goals" title={t("dashboard.proposed.title")} count={proposedGoals.length}>
            <ProposedGoalTable goals={proposedGoals} />
          </Section>
        )}

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
      </Section>
    </main>
  );
}
