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
import { Fragment, useState } from "react";
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
  const [openDecisionId, setOpenDecisionId] = useState<string | null>(null);

  if (decisions.length === 0) return <EmptyState>{emptyText}</EmptyState>;

  return (
    <div className="table-scroll">
      <Table className="min-w-[64rem] w-full border-collapse text-left text-base">
        <caption className="sr-only">{t("decision.caption.list")}</caption>
          <Table.Header className="border-b-2 border-ink-300 text-base text-ink-700">
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
            const isOpen = unanswered && openDecisionId === decision.id;
            const recommendation = unanswered ? decisionRecommendationLabel(locale, decision.default_option) : undefined;
            const autoSettlementSeconds = unanswered ? decisionAutoSettlementSeconds(decision.default_after_ms) : undefined;
            const autoSettlement = autoSettlementSeconds === undefined
              ? undefined
              : t("decision.autoSettlesIn", { duration: formatDuration(locale, autoSettlementSeconds) });
            const questionHref = decision.task_id
              ? `/tasks/${encodePathSegment(decision.task_id)}`
              : `/goals/${encodePathSegment(decision.goal_id)}`;
            return (
              <Fragment key={decision.id}>
                <Table.Row className="border-b border-line align-top last:border-b-0">
                  <Table.Cell className="px-3 py-4">
                    {unanswered ? (
                      <div className="flex items-start gap-2">
                        <button
                          type="button"
                          aria-expanded={isOpen}
                          aria-label={t("decision.column.question")}
                          className="focus-ring shrink-0 cursor-pointer text-ink-700 hover:text-ink-950"
                          onClick={() => {
                            setOpenDecisionId((current) => (current === decision.id ? null : decision.id));
                          }}
                        >
                          <span aria-hidden="true">{isOpen ? "▼" : "▶"}</span>
                        </button>
                        <a
                          className="focus-ring text-clamp-2 w-fit max-w-[34rem] font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                          href={questionHref}
                          title={decision.question}
                        >
                          {decision.question}
                        </a>
                      </div>
                    ) : (
                      <a
                        className="focus-ring text-clamp-2 w-fit max-w-[34rem] font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                        href={questionHref}
                        title={decision.question}
                      >
                        {decision.question}
                      </a>
                    )}
                    <p className="mt-1 font-mono text-base text-ink-500">{decisionKindLabel(locale, decision.kind)}</p>
                    {recommendation && <p className="mt-1 text-base font-medium text-accent-700">{recommendation}: {decision.default_option}</p>}
                    {autoSettlement && <p className="mt-1 text-base text-ink-500">{autoSettlement}</p>}
                    {settlement && <p className="mt-1 text-base text-ink-500">{settlement}</p>}
                  </Table.Cell>
                  <Table.Cell className="px-3 py-4 text-ink-700">{decision.project_name || "-"}</Table.Cell>
                  <Table.Cell className="px-3 py-4">
                    <a
                      className="focus-ring font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                      href={`/goals/${encodePathSegment(decision.goal_id)}`}
                    >
                      {decision.goal_headline || "-"}
                    </a>
                  </Table.Cell>
                  <Table.Cell className="px-3 py-4 text-ink-700">{formatDateTime(locale, decision.created_at)}</Table.Cell>
                </Table.Row>
                {isOpen && (
                  <Table.Row className="border-b border-line align-top">
                    <Table.Cell colSpan={4} className="px-3 py-4">
                      <div className="space-y-4">
                        <p className="whitespace-pre-wrap font-medium text-ink-950">{decision.question}</p>
                        {(recommendation || autoSettlement || settlement) && (
                          <div className="space-y-1 text-base">
                            {recommendation && <p className="font-medium text-accent-700">{recommendation}: {decision.default_option}</p>}
                            {autoSettlement && <p className="text-ink-500">{autoSettlement}</p>}
                            {settlement && <p className="text-ink-500">{settlement}</p>}
                          </div>
                        )}
                        {decision.options.length > 0 && (
                          <ul className="space-y-3">
                            {decision.options.map((option) => {
                              const optionRecommendation = decisionRecommendationLabel(
                                locale,
                                decision.default_option,
                                option.label,
                              );
                              return (
                                <li className="border-l-2 border-line pl-3" key={`${decision.id}-${option.label}`}>
                                  <p className="font-medium text-ink-950">
                                    {option.label}
                                    {optionRecommendation && (
                                      <span className="ml-2 text-base font-medium text-accent-700">{optionRecommendation}</span>
                                    )}
                                  </p>
                                  <p className="mt-1 text-base text-ink-700">{option.description}</p>
                                  <p className="mt-1 text-base text-ink-700">{option.consequence}</p>
                                </li>
                              );
                            })}
                          </ul>
                        )}
                      </div>
                    </Table.Cell>
                  </Table.Row>
                )}
              </Fragment>
            );
          })}
        </Table.Body>
      </Table>
    </div>
  );
}
