import { Button } from "@cloudflare/kumo/components/button";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { Decision, TaskView } from "../lib/api";
import { ApiError, releaseTask } from "../lib/api";
import { formatDuration } from "../i18n";
import { statusLabel } from "../lib/ui";
import { DecisionAnswerForm } from "./DecisionAnswerForm";
import { EmptyState } from "./StateMessage";

interface Props {
  tasks: TaskView[];
  onRefresh: () => void;
}

function TaskRelease({ task, onRefresh }: { task: TaskView; onRefresh: () => void }) {
  const [releasing, setReleasing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!task.claimed_by) return <span className="text-ink-500">-</span>;

  async function handleRelease() {
    setReleasing(true);
    setError(null);
    try {
      await releaseTask(task.id);
      onRefresh();
    } catch (reason) {
      setError(reason instanceof ApiError ? reason.message : "Could not release the task.");
    } finally {
      setReleasing(false);
    }
  }

  return (
    <div>
      <Button
        type="button"
        disabled={releasing}
        className="focus-ring border border-line bg-surface px-3 py-2 text-sm font-medium text-ink-800 hover:border-ink-500 hover:bg-paper disabled:cursor-wait disabled:opacity-60"
        onClick={handleRelease}
      >
        {releasing ? "Releasing..." : "Release claim"}
      </Button>
      {error && <p className="mt-2 max-w-48 text-xs text-danger-700" role="alert">{error}</p>}
    </div>
  );
}

function ClaimSummary({ task }: { task: TaskView }) {
  const { i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  return (
    <div className="space-y-1 text-sm text-ink-700">
      <p>{task.claimed_by || "Unclaimed"}</p>
      <p className="text-xs text-ink-500">{task.claimed_by ? formatDuration(locale, task.held_for_seconds) : "No active claim"}</p>
    </div>
  );
}

function DecisionDetails({ decision, onRefresh }: { decision: Decision; onRefresh: () => void }) {
  return (
    <div className="mt-5 border-t border-line pt-4">
      <p className="text-xs font-semibold uppercase tracking-wide text-ink-700">Decision</p>
      <DecisionAnswerForm decision={decision} onUpdated={onRefresh} />
    </div>
  );
}

export function NeedsDecisionList({ tasks, onRefresh }: Props) {
  if (tasks.length === 0) {
    return <EmptyState>No tasks are waiting for a decision. Tasks appear here when they need an answer.</EmptyState>;
  }

  const rows = tasks.flatMap((task) => {
    const decisions = task.open_decisions?.length ? task.open_decisions : [null];
    return decisions.map((decision) => ({ task, decision }));
  });

  return (
    <div className="space-y-6" data-testid="needs-decision-list">
      {rows.map(({ task, decision }) => (
        <section className="min-w-0 border-t border-line pt-5 first:border-t-0 first:pt-0" key={`${task.id}-${decision?.id ?? "task"}`}>
          <div className="min-w-0">
            <p className="text-clamp-2 font-medium text-ink-950" title={task.title}>{task.title}</p>
            <p className="mt-1 font-mono text-xs text-ink-500">{task.id}</p>
          </div>
          <div className="mt-4 grid min-w-0 gap-4 sm:grid-cols-3">
            <div>
              <p className="text-xs font-semibold uppercase tracking-wide text-ink-700">Status</p>
              <p className="mt-1 text-sm text-ink-700">{statusLabel(task.status)}</p>
            </div>
            <div>
              <p className="text-xs font-semibold uppercase tracking-wide text-ink-700">Claim</p>
              <ClaimSummary task={task} />
            </div>
            <div>
              <p className="text-xs font-semibold uppercase tracking-wide text-ink-700">Action</p>
              <div className="mt-1"><TaskRelease task={task} onRefresh={onRefresh} /></div>
            </div>
          </div>
          {decision ? <DecisionDetails decision={decision} onRefresh={onRefresh} /> : <p className="mt-5 border-t border-line pt-4 text-sm text-ink-500">No decision details</p>}
        </section>
      ))}
    </div>
  );
}
