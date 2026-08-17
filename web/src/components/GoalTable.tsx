import type { Goal } from "../lib/api";
import { formatDateTime } from "../i18n";
import { encodePathSegment, statusLabel } from "../lib/ui";
import { Table } from "@cloudflare/kumo/components/table";
import { useTranslation } from "react-i18next";
import { EmptyState } from "./StateMessage";

interface Props {
  goals: Goal[];
}

const columnScope = { scope: "col" } as const;

export function GoalTable({ goals }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  if (goals.length === 0) return <EmptyState>{t("dashboard.activeGoals.empty")}</EmptyState>;

  return (
    <div className="table-scroll">
      <Table className="min-w-[48rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">{t("goal.caption.activeList")}</caption>
        <Table.Header className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <Table.Row>
            <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("goal.column.goal")}</Table.Head>
            <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("goal.column.project")}</Table.Head>
            <Table.Head {...columnScope} className="w-36 px-3 py-3 font-semibold">{t("goal.column.status")}</Table.Head>
            <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("goal.column.updatedAt")}</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {goals.map((goal) => (
            <Table.Row className="border-b border-line align-top last:border-b-0" key={goal.id}>
              <Table.Cell className="px-3 py-4">
                <a
                  className="focus-ring text-clamp-2 font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                  href={`/goals/${encodePathSegment(goal.id)}`}
                  title={goal.title}
                >
                  {goal.title}
                </a>
                <p className="mt-1 font-mono text-xs text-ink-500">{goal.id}</p>
              </Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{goal.project_name || "-"}</Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{statusLabel(locale, goal.status)}</Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{formatDateTime(locale, goal.updated_at)}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </div>
  );
}
