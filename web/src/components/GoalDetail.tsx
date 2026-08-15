import { useCallback, useEffect, useState } from "react";
import { fetchGoal, subscribeToDecisionEvents, type GoalResponse } from "../lib/api";
import { AreaLoading, ErrorState } from "./StateMessage";
import { Section } from "./Section";
import { TaskTable } from "./TaskTable";

interface Props {
  id: string;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: GoalResponse }
  | { kind: "error"; message: string };

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Could not load the goal.";
}

export function GoalDetail({ id }: Props) {
  const [state, setState] = useState<LoadState>({ kind: "loading" });

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", data: await fetchGoal(id) });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason) });
    }
  }, [id]);

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
          {data?.goal.title ?? "Goal details"}
        </h1>
        {data?.goal.description && <p className="mt-3 max-w-3xl whitespace-pre-wrap text-sm leading-6 text-ink-700">{data.goal.description}</p>}
        {data?.goal.status && <p className="mt-3 text-sm text-ink-500">Status: {data.goal.status}</p>}
      </div>

      <div className="grid gap-10 xl:grid-cols-3">
        <Section title="Now" count={data?.now.length}>
          {state.kind === "loading" && <AreaLoading label="Now" />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && <TaskTable tasks={data.now} mode="now" onRefresh={load} />}
        </Section>

        <Section title="Needs decision" count={data?.needs_decision.length}>
          {state.kind === "loading" && <AreaLoading label="Needs decision" />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && <TaskTable tasks={data.needs_decision} mode="needs_decision" onRefresh={load} />}
        </Section>

        <Section title="Next" count={data?.next.length}>
          {state.kind === "loading" && <AreaLoading label="Next" />}
          {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
          {data && <TaskTable tasks={data.next} mode="next" onRefresh={load} />}
        </Section>
      </div>
    </main>
  );
}
