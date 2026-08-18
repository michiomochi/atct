import type { DecisionHistoryEntry } from "../lib/api";
import { formatDateTime } from "../i18n";
import { decisionSettlementLabel } from "../lib/ui";
import { Table } from "@cloudflare/kumo/components/table";
import { useTranslation } from "react-i18next";

interface Props {
  decisions: DecisionHistoryEntry[];
  omittedCount: number;
}

const columnScope = { scope: "col" } as const;

export function DecisionHistoryTable({ decisions, omittedCount }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  return (
    <section className="border-t border-line pt-5" data-testid="decision-history" aria-labelledby="decision-history-heading">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <h2 id="decision-history-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">
          {t("goal.history.title")}
        </h2>
        {omittedCount > 0 && <p className="text-sm text-ink-600">{t("goal.history.omitted", { count: omittedCount })}</p>}
      </div>
      <div className="table-scroll mt-4">
        <Table className="min-w-[52rem] w-full border-collapse text-left text-sm">
          <caption className="sr-only">{t("goal.history.caption")}</caption>
          <Table.Header className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
            <Table.Row>
              <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("goal.history.column.question")}</Table.Head>
              <Table.Head {...columnScope} className="w-64 px-3 py-3 font-semibold">{t("goal.history.column.answer")}</Table.Head>
              <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("goal.history.column.answeredAt")}</Table.Head>
              <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("goal.history.column.appliedAt")}</Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {decisions.map((decision) => {
              const answer = [decision.answer_label, decision.answer_text].filter(Boolean).join(" - ");
              const settlement = decisionSettlementLabel(locale, decision.settled_by_default === true);
              return (
                <Table.Row className="border-b border-line align-top last:border-b-0" key={decision.decision_id}>
                  <Table.Cell className="max-w-[28rem] break-words px-3 py-4 font-medium text-ink-950">{decision.question}</Table.Cell>
                  <Table.Cell className="max-w-64 break-words px-3 py-4 text-ink-700">
                    <p>{answer || "-"}</p>
                    {settlement && <p className="mt-1 text-xs text-ink-500">{settlement}</p>}
                  </Table.Cell>
                  <Table.Cell className="whitespace-nowrap px-3 py-4 text-ink-700">{decision.answered_at ? formatDateTime(locale, decision.answered_at) : "-"}</Table.Cell>
                  <Table.Cell className="whitespace-nowrap px-3 py-4 text-ink-700">{decision.applied_at ? formatDateTime(locale, decision.applied_at) : "-"}</Table.Cell>
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table>
      </div>
    </section>
  );
}
