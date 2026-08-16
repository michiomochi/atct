import { Button } from "@cloudflare/kumo/components/button";
import { Table } from "@cloudflare/kumo/components/table";
import { useState } from "react";
import { useTranslation } from "react-i18next";
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

const columnScope = { scope: "col" } as const;

function TaskRelease({ task, onRefresh }: { task: TaskView; onRefresh: () => void }) {
  const { t } = useTranslation();
  const [releasing, setReleasing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!task.claimed_by) return <span className="text-ink-500">{t("duration.none")}</span>;

  async function handleRelease() {
    setReleasing(true);
    setError(null);
    try {
      await releaseTask(task.id);
      onRefresh();
    } catch (reason) {
      setError(reason instanceof ApiError ? reason.message : reason instanceof Error ? reason.message : t("task.claim.error.release"));
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
        {releasing ? t("task.claim.releasing") : t("task.claim.release")}
      </Button>
      {error && <p className="mt-2 max-w-48 text-xs text-danger-700" role="alert">{error}</p>}
    </div>
  );
}

function ClaimCell({ task }: { task: TaskView }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-1 text-ink-700">
      <p>{task.claimed_by || t("task.claim.noHolder")}</p>
      <p className="text-xs text-ink-500">{task.claimed_by ? formatHeldFor(task.held_for_seconds) : t("task.claim.unclaimed")}</p>
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
  const { t } = useTranslation();
  if (tasks.length === 0) {
    const message = mode === "needs_decision" ? t("task.empty.needsDecision") : t("task.empty.column");
    return <EmptyState>{message}</EmptyState>;
  }

  const needsDecision = mode === "needs_decision";
  const rows = tasks.flatMap((task) => {
    const decisions = needsDecision && (task.open_decisions ?? []).length > 0 ? task.open_decisions ?? [] : [null];
    return decisions.map((decision) => ({ task, decision }));
  });

  return (
    <div className="table-scroll">
      <Table className={needsDecision ? "min-w-[70rem] w-full border-collapse text-left text-sm" : "min-w-[52rem] w-full border-collapse text-left text-sm"}>
        <caption className="sr-only">{needsDecision ? t("task.caption.needsDecision") : t("task.caption.list")}</caption>
        <Table.Header className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <Table.Row>
            <Table.Head {...columnScope} className="w-64 px-3 py-3 font-semibold">{t("task.column.task")}</Table.Head>
            {needsDecision && <Table.Head {...columnScope} className="min-w-[34rem] px-3 py-3 font-semibold">{t("task.column.decision")}</Table.Head>}
            <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("task.column.status")}</Table.Head>
            <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("task.column.claim")}</Table.Head>
            <Table.Head {...columnScope} className="w-36 px-3 py-3 font-semibold">{t("task.column.action")}</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {rows.map(({ task, decision }) => (
            <Table.Row className="border-b border-line align-top last:border-b-0" key={`${task.id}-${decision?.id ?? "task"}`}>
              <Table.Cell className="px-3 py-4"><TaskTitle task={task} /></Table.Cell>
              {needsDecision && (
                <Table.Cell className="px-3 py-4">
                  {decision ? <DecisionCell decision={decision} onRefresh={onRefresh} /> : <span className="text-ink-500">{t("task.decision.noDetails")}</span>}
                </Table.Cell>
              )}
              <Table.Cell className="px-3 py-4 text-ink-700">{statusLabel(task.status)}</Table.Cell>
              <Table.Cell className="px-3 py-4"><ClaimCell task={task} /></Table.Cell>
              <Table.Cell className="px-3 py-4"><TaskRelease task={task} onRefresh={onRefresh} /></Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </div>
  );
}
