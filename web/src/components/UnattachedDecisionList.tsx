import { useTranslation } from "react-i18next";
import type { Decision } from "../lib/api";
import { DecisionAnswerForm } from "./DecisionAnswerForm";

interface Props {
  decisions: Decision[];
  onRefresh: () => void;
}

export function UnattachedDecisionList({ decisions, onRefresh }: Props) {
  const { t } = useTranslation();

  if (decisions.length === 0) return null;

  return (
    <section
      className="border-t border-line pt-5"
      data-testid="unattached-decision-list"
      aria-labelledby="unattached-decisions-heading"
    >
      <div className="mb-4 flex items-baseline justify-between gap-4">
        <h2 id="unattached-decisions-heading" className="font-display text-lg font-semibold tracking-tight text-ink-950">
          {t("goal.unattached.title")} <span className="font-mono text-sm font-normal text-ink-500">{decisions.length}</span>
        </h2>
      </div>
      <ol className="min-w-0 space-y-6">
        {decisions.map((decision) => (
          <li className="min-w-0 border-t border-line pt-5 first:border-t-0 first:pt-0" key={decision.id}>
            <DecisionAnswerForm decision={decision} onUpdated={onRefresh} />
          </li>
        ))}
      </ol>
    </section>
  );
}
