import type { Goal } from "../lib/api";
import { formatDateTime } from "../i18n";
import { encodePathSegment, headline, sortTasksByOrder, statusLabel } from "../lib/ui";
import { Table } from "@cloudflare/kumo/components/table";
import { Fragment, useState } from "react";
import { useTranslation } from "react-i18next";
import { EmptyState } from "./StateMessage";

interface Props {
  goals: Goal[];
  showProject?: boolean;
}

const columnScope = { scope: "col" } as const;

export function GoalTable({ goals, showProject = true }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  const [openGoals, setOpenGoals] = useState<Record<string, boolean>>({});

  if (goals.length === 0) return <EmptyState>{t("dashboard.activeGoals.empty")}</EmptyState>;

  return (
    <div className="table-scroll">
      <Table className="min-w-[48rem] w-full border-collapse text-left text-base">
        <caption className="sr-only">{t("goal.caption.activeList")}</caption>
        <Table.Header className="border-b-2 border-ink-300 text-base text-ink-700">
          <Table.Row>
            <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("goal.column.goal")}</Table.Head>
            {showProject && <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("goal.column.project")}</Table.Head>}
            <Table.Head {...columnScope} className="w-36 px-3 py-3 font-semibold">{t("goal.column.status")}</Table.Head>
            <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("goal.column.updatedAt")}</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {goals.map((goal) => {
            const tasks = sortTasksByOrder(goal.tasks ?? []);
            const goalStatus = goal.awaiting_decision ? t("status.awaitingDecision") : statusLabel(locale, goal.status);
            const isOpen = openGoals[goal.id] ?? false;
            const goalLink = (
              <a
                className="focus-ring text-clamp-2 w-fit font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                href={`/goals/${encodePathSegment(goal.id)}`}
                title={headline(goal.content)}
              >
                {headline(goal.content)}
              </a>
            );
            return (
              <Fragment key={goal.id}>
                <Table.Row className="border-b border-line align-top last:border-b-0">
                  <Table.Cell className="px-3 py-4">
                    {tasks.length > 0 ? (
                      <div className="flex items-start gap-2">
                        <button
                          type="button"
                          aria-expanded={isOpen}
                          aria-label={t("goal.tasks.title")}
                          className="focus-ring shrink-0 cursor-pointer text-ink-700 hover:text-ink-950"
                          onClick={() => {
                            setOpenGoals((current) => ({
                              ...current,
                              [goal.id]: !(current[goal.id] ?? false),
                            }));
                          }}
                        >
                          <span aria-hidden="true">{isOpen ? "▼" : "▶"}</span>
                        </button>
                        {goalLink}
                      </div>
                    ) : (
                      goalLink
                    )}
                  </Table.Cell>
                  {showProject && <Table.Cell className="px-3 py-4 text-ink-700">{goal.project_name || "-"}</Table.Cell>}
                  <Table.Cell className="px-3 py-4 text-ink-700">{goalStatus}</Table.Cell>
                  <Table.Cell className="px-3 py-4 text-ink-700">{formatDateTime(locale, goal.updated_at)}</Table.Cell>
                </Table.Row>
                {isOpen && tasks.map((task, index) => (
                  <Table.Row className="border-b border-line align-top last:border-b-0" key={task.id}>
                    <Table.Cell className="px-3 py-3 text-ink-700">
                      <div className="flex items-start pl-6">
                        <span aria-hidden="true" className="mr-2 shrink-0 text-ink-500">
                          {index === tasks.length - 1 ? "└─" : "├─"}
                        </span>
                        <a
                          className="focus-ring inline-block w-fit max-w-full text-left text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                          href={`/tasks/${encodePathSegment(task.id)}`}
                        >
                          <span className="text-clamp-2 block max-w-[32rem] break-words font-medium" title={task.title}>
                            {task.title}
                          </span>
                        </a>
                      </div>
                    </Table.Cell>
                    {showProject && <Table.Cell className="px-3 py-3" />}
                    <Table.Cell className="px-3 py-3 text-ink-700">
                      {task.open_decisions.length > 0 && !["done", "completed", "withdrawn"].includes(task.status)
                        ? t("status.awaitingDecision")
                        : statusLabel(locale, task.status)}
                    </Table.Cell>
                    <Table.Cell className="px-3 py-3 text-ink-700">{formatDateTime(locale, task.updated_at)}</Table.Cell>
                  </Table.Row>
                ))}
              </Fragment>
            );
          })}
        </Table.Body>
      </Table>
    </div>
  );
}
