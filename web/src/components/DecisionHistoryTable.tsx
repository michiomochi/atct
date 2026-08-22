import { useState } from "react";
import { reviseDecision, type DecisionHistoryEntry, type Option } from "../lib/api";
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
  const [revisingDecisionID, setRevisingDecisionID] = useState<string | null>(null);
  const [revisionOptions, setRevisionOptions] = useState("");
  const [revisionSubmitting, setRevisionSubmitting] = useState(false);
  const [revisionMessage, setRevisionMessage] = useState<{ decisionID: string; text: string; error: boolean } | null>(null);
  const changeAssumptionLabel = t("goal.history.changeAssumption");

  const submitRevision = async (decisionID: string) => {
    const options = revisionOptions
      .split(/[\n,]/)
      .map((label): Option => ({ label: label.trim(), description: "", consequence: "" }))
      .filter((option) => option.label !== "");
    if (options.length === 0) {
      setRevisionMessage({ decisionID, text: t("goal.history.revision.invalid"), error: true });
      return;
    }

    setRevisionSubmitting(true);
    setRevisionMessage(null);
    try {
      await reviseDecision(decisionID, { options });
      setRevisionMessage({ decisionID, text: t("goal.history.revision.created"), error: false });
      setRevisingDecisionID(null);
      setRevisionOptions("");
    } catch (error) {
      setRevisionMessage({
        decisionID,
        text: error instanceof Error ? error.message : t("goal.history.revision.error"),
        error: true,
      });
    } finally {
      setRevisionSubmitting(false);
    }
  };

  return (
    <section className="border-t border-line pt-5" data-testid="decision-history" aria-labelledby="decision-history-heading">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <h2 id="decision-history-heading" className="font-display text-lg font-semibold text-ink-950">
          {t("goal.history.title")}
        </h2>
        {omittedCount > 0 && <p className="text-sm text-ink-700">{t("goal.history.omitted", { count: omittedCount })}</p>}
      </div>
      <div className="table-scroll mt-4">
        <Table className="min-w-[52rem] w-full border-collapse text-left text-sm">
          <caption className="sr-only">{t("goal.history.caption")}</caption>
          <Table.Header className="border-b-2 border-ink-300 text-sm text-ink-700">
            <Table.Row>
              <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("goal.history.column.question")}</Table.Head>
              <Table.Head {...columnScope} className="w-64 px-3 py-3 font-semibold">{t("goal.history.column.answer")}</Table.Head>
              <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("goal.history.column.answeredAt")}</Table.Head>
              <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">{t("goal.history.column.appliedAt")}</Table.Head>
              <Table.Head {...columnScope} className="w-72 px-3 py-3 font-semibold">{changeAssumptionLabel}</Table.Head>
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
                    {settlement && <p className="mt-1 text-sm text-ink-500">{settlement}</p>}
                  </Table.Cell>
                  <Table.Cell className="whitespace-nowrap px-3 py-4 text-ink-700">{decision.answered_at ? formatDateTime(locale, decision.answered_at) : "-"}</Table.Cell>
                  <Table.Cell className="whitespace-nowrap px-3 py-4 text-ink-700">{decision.applied_at ? formatDateTime(locale, decision.applied_at) : "-"}</Table.Cell>
                  <Table.Cell className="px-3 py-4 text-ink-700">
                    {revisingDecisionID === decision.decision_id ? (
                      <form
                        className="space-y-2"
                        onSubmit={(event) => {
                          event.preventDefault();
                          void submitRevision(decision.decision_id);
                        }}
                      >
                        <label className="sr-only" htmlFor={`revision-options-${decision.decision_id}`}>
                          {changeAssumptionLabel}
                        </label>
                        <input
                          id={`revision-options-${decision.decision_id}`}
                          className="w-full rounded border border-line px-2 py-1 text-sm"
                          value={revisionOptions}
                          onChange={(event) => setRevisionOptions(event.target.value)}
                          placeholder={t("goal.history.revision.placeholder")}
                          disabled={revisionSubmitting}
                        />
                        <div className="flex gap-2">
                          <button
                            className="rounded bg-ink-950 px-2 py-1 text-sm font-semibold text-white disabled:opacity-50"
                            type="submit"
                            disabled={revisionSubmitting}
                          >
                            {revisionSubmitting ? t("goal.history.revision.creating") : t("goal.history.revision.create")}
                          </button>
                          <button
                            className="rounded border border-line px-2 py-1 text-sm font-semibold text-ink-700"
                            type="button"
                            onClick={() => {
                              setRevisingDecisionID(null);
                              setRevisionOptions("");
                              setRevisionMessage(null);
                            }}
                            disabled={revisionSubmitting}
                          >
                            {t("goal.history.revision.cancel")}
                          </button>
                        </div>
                      </form>
                    ) : (
                      <button
                        className="rounded border border-line px-2 py-1 text-sm font-semibold text-ink-700 hover:border-ink-500"
                        type="button"
                        onClick={() => {
                          setRevisingDecisionID(decision.decision_id);
                          setRevisionOptions("");
                          setRevisionMessage(null);
                        }}
                      >
                        {changeAssumptionLabel}
                      </button>
                    )}
                    {revisionMessage?.decisionID === decision.decision_id && (
                      <p className={`mt-2 text-sm ${revisionMessage.error ? "text-red-700" : "text-ink-700"}`} role="status">
                        {revisionMessage.text}
                      </p>
                    )}
                  </Table.Cell>
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table>
      </div>
    </section>
  );
}
