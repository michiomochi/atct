import { Table } from "@cloudflare/kumo/components/table";
import { useTranslation } from "react-i18next";
import { formatDateTime } from "../i18n";
import type { ProposedGoal } from "../lib/api";

interface Props {
  goals: ProposedGoal[];
}

export function ProposedGoalTable({ goals }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  return (
    <div className="overflow-x-auto">
      <Table>
        <thead>
          <Table.Row className="border-b border-line text-left">
            <th className="px-3 py-2 text-xs font-semibold text-ink-500">{t("form.goal.title.label")}</th>
            <th className="px-3 py-2 text-xs font-semibold text-ink-500">{t("goal.project")}</th>
            <th className="px-3 py-2 text-xs font-semibold text-ink-500">{t("task.detail.createdAt")}</th>
            <th className="px-3 py-2 text-xs font-semibold text-ink-500">{t("task.column.action")}</th>
          </Table.Row>
        </thead>
        <Table.Body>
          {goals.map((goal) => (
            <Table.Row className="border-b border-line align-top last:border-b-0" key={goal.id}>
              <Table.Cell className="px-3 py-4">
                <a
                  className="focus-ring text-clamp-2 w-fit max-w-[34rem] font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                  href={`/goals/${encodeURIComponent(goal.id)}`}
                  title={goal.title}
                >
                  {goal.title}
                </a>
                {goal.description && (
                  <p className="text-clamp-2 mt-1 block max-w-[32rem] break-words text-sm text-ink-500">
                    {goal.description}
                  </p>
                )}
              </Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{goal.project_name || "-"}</Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{formatDateTime(locale, goal.created_at)}</Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{t("dashboard.proposed.approveHint")}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </div>
  );
}
