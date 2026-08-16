import type { Decision } from "../lib/api";
import { encodePathSegment, formatDate, statusLabel } from "../lib/ui";
import { Table } from "@cloudflare/kumo/components/table";
import { useTranslation } from "react-i18next";
import { EmptyState } from "./StateMessage";

interface Props {
  decisions: Decision[];
  emptyText: string;
}

const columnScope = { scope: "col" } as const;

export function DecisionTable({ decisions, emptyText }: Props) {
  const { t } = useTranslation();

  if (decisions.length === 0) return <EmptyState>{emptyText}</EmptyState>;

  return (
    <div className="table-scroll">
      <Table className="min-w-[58rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">{t("decision.caption.list")}</caption>
        <Table.Header className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <Table.Row>
            <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("decision.column.question")}</Table.Head>
            <Table.Head {...columnScope} className="w-36 px-3 py-3 font-semibold">{t("decision.column.status")}</Table.Head>
            <Table.Head {...columnScope} className="w-52 px-3 py-3 font-semibold">{t("decision.column.answer")}</Table.Head>
            <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("decision.column.answeredBy")}</Table.Head>
            <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("decision.column.goal")}</Table.Head>
            <Table.Head {...columnScope} className="w-44 px-3 py-3 font-semibold">{t("decision.column.createdAt")}</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {decisions.map((decision) => {
            const answer = [decision.answer_label, decision.answer_text].filter(Boolean).join(" - ");
            return (
              <Table.Row className="border-b border-line align-top last:border-b-0" key={decision.id}>
                <Table.Cell className="px-3 py-4">
                  <p className="text-clamp-2 max-w-[34rem] font-medium text-ink-950" title={decision.question}>
                    {decision.question}
                  </p>
                  <p className="mt-1 font-mono text-xs text-ink-500">{decision.kind}</p>
                </Table.Cell>
                <Table.Cell className="px-3 py-4 text-ink-700">{statusLabel(decision.status)}</Table.Cell>
                <Table.Cell className="px-3 py-4 text-ink-700">
                  {answer ? <p className="text-clamp-2" title={answer}>{answer}</p> : "-"}
                </Table.Cell>
                <Table.Cell className="px-3 py-4 text-ink-700">{decision.answered_by || "-"}</Table.Cell>
                <Table.Cell className="px-3 py-4">
                  <a
                    className="focus-ring font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                    href={`/goals/${encodePathSegment(decision.goal_id)}`}
                  >
                    {decision.goal_id}
                  </a>
                </Table.Cell>
                <Table.Cell className="px-3 py-4 text-ink-700">{formatDate(decision.created_at)}</Table.Cell>
              </Table.Row>
            );
          })}
        </Table.Body>
      </Table>
    </div>
  );
}
