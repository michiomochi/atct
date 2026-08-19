import type { Decision } from "../lib/api";
import { formatDateTime, formatDuration } from "../i18n";
import {
  decisionAutoSettlementSeconds,
  decisionKindLabel,
  decisionRecommendationLabel,
  decisionSettlementLabel,
  encodePathSegment,
} from "../lib/ui";
import { Table } from "@cloudflare/kumo/components/table";
import { useTranslation } from "react-i18next";
import { EmptyState } from "./StateMessage";

interface Props {
  decisions: Decision[];
  emptyText: string;
}

const columnScope = { scope: "col" } as const;

export function DecisionTable({ decisions, emptyText }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  if (decisions.length === 0) return <EmptyState>{emptyText}</EmptyState>;

  return (
    <div className="table-scroll">
      <Table className="min-w-[64rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">{t("decision.caption.list")}</caption>
        <Table.Header className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <Table.Row>
            <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("decision.column.question")}</Table.Head>
            <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("decision.column.project")}</Table.Head>
            <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("decision.column.goal")}</Table.Head>
            <Table.Head {...columnScope} className="w-44 px-3 py-3 font-semibold">{t("decision.column.createdAt")}</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {decisions.map((decision) => {
            const settlement = decisionSettlementLabel(locale, decision.settled_by_default === true);
            const unanswered = decision.status === "open";
            const recommendation = unanswered ? decisionRecommendationLabel(locale, decision.default_option) : undefined;
            const autoSettlementSeconds = unanswered ? decisionAutoSettlementSeconds(decision.default_after_ms) : undefined;
            const autoSettlement = autoSettlementSeconds === undefined
              ? undefined
              : t("decision.autoSettlesIn", { duration: formatDuration(locale, autoSettlementSeconds) });
            return (
              <Table.Row className="border-b border-line align-top last:border-b-0" key={decision.id}>
                <Table.Cell className="px-3 py-4">
                  <a
                    className="focus-ring text-clamp-2 max-w-[34rem] font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                    href={`/goals/${encodePathSegment(decision.goal_id)}`}
                    title={decision.question}
                  >
                    {decision.question}
                  </a>
                  <p className="mt-1 font-mono text-xs text-ink-500">{decisionKindLabel(locale, decision.kind)}</p>
                  {recommendation && <p className="mt-1 text-xs font-medium text-accent-700">{recommendation}: {decision.default_option}</p>}
                  {autoSettlement && <p className="mt-1 text-xs text-ink-500">{autoSettlement}</p>}
                  {settlement && <p className="mt-1 text-xs text-ink-500">{settlement}</p>}
                </Table.Cell>
                <Table.Cell className="px-3 py-4 text-ink-700">{decision.project_name || "-"}</Table.Cell>
                <Table.Cell className="px-3 py-4">
                  <a
                    className="focus-ring font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                    href={`/goals/${encodePathSegment(decision.goal_id)}`}
                  >
                    {decision.goal_title || "-"}
                  </a>
                </Table.Cell>
                <Table.Cell className="px-3 py-4 text-ink-700">{formatDateTime(locale, decision.created_at)}</Table.Cell>
              </Table.Row>
            );
          })}
        </Table.Body>
      </Table>
    </div>
  );
}
