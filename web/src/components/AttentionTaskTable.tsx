import type { TaskView } from "../lib/api";
import { formatDuration } from "../i18n";
import { encodePathSegment, statusLabel } from "../lib/ui";
import { Table } from "@cloudflare/kumo/components/table";
import { useTranslation } from "react-i18next";
import { EmptyState } from "./StateMessage";

interface Props {
  tasks: TaskView[];
}

const columnScope = { scope: "col" } as const;

export function AttentionTaskTable({ tasks }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  if (tasks.length === 0) return <EmptyState>{t("inbox.attention.empty")}</EmptyState>;

  return (
    <div className="table-scroll">
      <Table className="min-w-[58rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">{t("task.caption.attention")}</caption>
        <Table.Header className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <Table.Row>
            <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("task.column.task")}</Table.Head>
            <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("task.column.goal")}</Table.Head>
            <Table.Head {...columnScope} className="w-32 px-3 py-3 font-semibold">{t("task.column.status")}</Table.Head>
            <Table.Head {...columnScope} className="w-44 px-3 py-3 font-semibold">{t("task.column.claimedBy")}</Table.Head>
            <Table.Head {...columnScope} className="w-32 px-3 py-3 font-semibold">{t("task.column.claimDuration")}</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {tasks.map((task) => (
            <Table.Row className="border-b border-line align-top last:border-b-0" key={task.id}>
              <Table.Cell className="px-3 py-4">
                <p className="text-clamp-2 max-w-[34rem] font-medium text-ink-950" title={task.title}>{task.title}</p>
                <p className="mt-1 font-mono text-xs text-ink-500">{task.id}</p>
              </Table.Cell>
              <Table.Cell className="px-3 py-4">
                <a
                  className="focus-ring font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                  href={`/goals/${encodePathSegment(task.goal_id)}`}
                >
                  {task.goal_id}
                </a>
              </Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{statusLabel(locale, task.status)}</Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{task.claimed_by || t("task.claim.noHolder")}</Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{task.claimed_by ? formatDuration(locale, task.held_for_seconds) : "-"}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </div>
  );
}
