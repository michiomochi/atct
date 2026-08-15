import { Button } from "@base-ui/react/button";
import { useState } from "react";
import type { Decision, TaskView } from "../lib/api";
import { ApiError, releaseTask } from "../lib/api";
import { formatHeldFor, statusLabel } from "../lib/ui";
import { DecisionAnswerForm } from "./DecisionAnswerForm";
import { EmptyState } from "./StateMessage";

interface Props {
  tasks: TaskView[];
  mode: "now" | "needs_decision" | "next";
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

function ClaimCell({ task }: { task: TaskView }) {
  return (
    <div className="space-y-1 text-ink-700">
      <p>{task.claimed_by || "Unclaimed"}</p>
      <p className="text-xs text-ink-500">{task.claimed_by ? formatHeldFor(task.held_for_seconds) : "No active claim"}</p>
    </div>
  );
}

function TaskTitle({ task }: { task: TaskView }) {
  return (
    <div>
      <p className="text-clamp-2 max-w-[24rem] font-medium text-ink-950" title={task.title}>{task.title}</p>
      <p className="mt-1 font-mono text-xs text-ink-500">{task.id}</p>
    </div>
  );
}

function DecisionCell({ decision, onRefresh }: { decision: Decision; onRefresh: () => void }) {
  return <DecisionAnswerForm decision={decision} onUpdated={onRefresh} />;
}

export function TaskTable({ tasks, mode, onRefresh }: Props) {
  if (tasks.length === 0) {
    const message = mode === "needs_decision" ? "No tasks are waiting for a decision. Tasks appear here when they need an answer." : "No tasks are in this column. Tasks appear here when they match this stage.";
    return <EmptyState>{message}</EmptyState>;
  }

  const needsDecision = mode === "needs_decision";
  const rows = tasks.flatMap((task) => {
    const decisions = needsDecision && (task.open_decisions ?? []).length > 0 ? task.open_decisions ?? [] : [null];
    return decisions.map((decision) => ({ task, decision }));
  });

  return (
    <div className="table-scroll">
      <table className={needsDecision ? "min-w-[70rem] w-full border-collapse text-left text-sm" : "min-w-[52rem] w-full border-collapse text-left text-sm"}>
        <caption className="sr-only">{needsDecision ? "Tasks awaiting decisions and answers" : "Task list"}</caption>
        <thead className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <tr>
            <th className="w-64 px-3 py-3 font-semibold" scope="col">Task</th>
            {needsDecision && <th className="min-w-[34rem] px-3 py-3 font-semibold" scope="col">Decision</th>}
            <th className="w-40 px-3 py-3 font-semibold" scope="col">Status</th>
            <th className="w-40 px-3 py-3 font-semibold" scope="col">Claim</th>
            <th className="w-36 px-3 py-3 font-semibold" scope="col">Action</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ task, decision }) => (
            <tr className="border-b border-line align-top last:border-b-0" key={`${task.id}-${decision?.id ?? "task"}`}>
              <td className="px-3 py-4"><TaskTitle task={task} /></td>
              {needsDecision && (
                <td className="px-3 py-4">
                  {decision ? <DecisionCell decision={decision} onRefresh={onRefresh} /> : <span className="text-ink-500">No decision details</span>}
                </td>
              )}
              <td className="px-3 py-4 text-ink-700">{statusLabel(task.status)}</td>
              <td className="px-3 py-4"><ClaimCell task={task} /></td>
              <td className="px-3 py-4"><TaskRelease task={task} onRefresh={onRefresh} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
