import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { formatDateTime } from "../i18n";
import type { ReviewExchange, ReviewExchangeGap, ReviewExchangeHistory, ReviewRejectionSource } from "../lib/api";
import { AreaLoading, EmptyState, ErrorState } from "./StateMessage";

interface Props {
  fetchHistory: () => Promise<ReviewExchangeHistory>;
  // The task page is already about one task, so its cards drop the task badge.
  showTaskBadge?: boolean;
}

type State =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; history: ReviewExchangeHistory };

type TimelineItem =
  | { kind: "gaps"; gaps: ReviewExchangeGap[] }
  | { kind: "exchange"; exchange: ReviewExchange };

// One timeline: a gap sorts by before_at, an exchange by its rejection time.
// On equal times the gap goes first, since it says what precedes that moment.
// Gaps with no exchange between them fold into one note.
function mergeTimeline(history: ReviewExchangeHistory): TimelineItem[] {
  const time = (iso: string) => {
    const value = new Date(iso).getTime();
    return Number.isNaN(value) ? 0 : value;
  };
  const sorted = [
    ...history.gaps.map((gap) => ({ gap, exchange: undefined, at: time(gap.before_at) })),
    ...history.exchanges.map((exchange) => ({ gap: undefined, exchange, at: time(exchange.rejection.at) })),
  ].sort((left, right) => left.at - right.at || Number(Boolean(left.exchange)) - Number(Boolean(right.exchange)));
  const items: TimelineItem[] = [];
  for (const entry of sorted) {
    const last = items[items.length - 1];
    if (entry.exchange) items.push({ kind: "exchange", exchange: entry.exchange });
    else if (last?.kind === "gaps") last.gaps.push(entry.gap);
    else items.push({ kind: "gaps", gaps: [entry.gap] });
  }
  return items;
}

function Badge({ children }: { children: string }) {
  return <span className="border border-line px-2 py-0.5 text-sm font-medium text-ink-700">{children}</span>;
}

export function ReviewExchanges({ fetchHistory, showTaskBadge = true }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  const [state, setState] = useState<State>({ kind: "loading" });

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", history: await fetchHistory() });
    } catch (reason) {
      setState({ kind: "error", message: reason instanceof Error && reason.message ? reason.message : t("reviewExchange.error") });
    }
  }, [fetchHistory, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const sourceLabel = (source: ReviewRejectionSource) => {
    if (source === "human") return t("reviewExchange.source.human");
    if (source === "withdrawn") return t("reviewExchange.source.withdrawn");
    return t("reviewExchange.source.handoff");
  };

  const scopeBadge = (exchange: ReviewExchange) => {
    if (exchange.scope === "plan") return t("reviewExchange.scope.plan");
    if (exchange.scope === "task" && showTaskBadge) return t("reviewExchange.scope.task", { id: exchange.task_id ?? 0 });
    return null;
  };

  const items = state.kind === "ready" ? mergeTimeline(state.history) : [];

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="review-exchanges" aria-labelledby="review-exchanges-heading">
      <h2 id="review-exchanges-heading" className="font-display text-lg font-semibold text-ink-950">
        {t("reviewExchange.title")}
      </h2>
      <div className="mt-4">
        {state.kind === "loading" && <AreaLoading label={t("reviewExchange.loading")} />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={() => void load()} />}
        {state.kind === "ready" && items.length === 0 && <EmptyState>{t("reviewExchange.empty")}</EmptyState>}
        {state.kind === "ready" && items.length > 0 && (
          <div className="space-y-6">
            {items.map((item) => {
              if (item.kind === "gaps") {
                const first = item.gaps[0];
                return (
                  <details className="min-w-0 py-2 text-sm text-ink-700" key={`gaps-${first.scope}-${first.handoff_id}`}>
                    <summary className="focus-ring cursor-pointer break-words">
                      {`${t("reviewExchange.gap")} ${t("reviewExchange.gapCount", { total: item.gaps.length })} `}
                      <time dateTime={first.before_at}>{formatDateTime(locale, first.before_at)}</time>
                    </summary>
                    <ul className="mt-2 space-y-1 pl-4">
                      {item.gaps.map((gap) => (
                        <li className="min-w-0 break-all" key={`${gap.scope}-${gap.handoff_id}`}>
                          <span>{gap.handoff_id}</span>{" "}
                          <time dateTime={gap.before_at}>{formatDateTime(locale, gap.before_at)}</time>
                        </li>
                      ))}
                    </ul>
                  </details>
                );
              }
              const { exchange } = item;
              const { rejection, response } = exchange;
              const badge = scopeBadge(exchange);
              const actor = rejection.source === "handoff" && rejection.actor_session_id !== undefined
                ? t("reviewExchange.actor.session", { id: rejection.actor_session_id })
                : rejection.source === "human" ? t("reviewExchange.actor.human") : null;
              return (
                <article className="min-w-0 border border-line bg-surface px-4 py-4" key={`${rejection.at}-${exchange.scope}-${exchange.task_id ?? 0}-${rejection.decision_id ?? exchange.handoff_id ?? ""}`}>
                  <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-sm text-ink-700">
                    {badge && <Badge>{badge}</Badge>}
                    <span className="font-semibold text-ink-950">{sourceLabel(rejection.source)}</span>
                    {actor && <span>{actor}</span>}
                    <time dateTime={rejection.at}>{formatDateTime(locale, rejection.at)}</time>
                  </div>
                  <p className="mt-1 whitespace-pre-wrap break-words text-base leading-6 text-ink-950">{rejection.reason}</p>
                  <div className="mt-4 min-w-0 border-l-2 border-line pl-4">
                    {response ? (
                      <>
                        <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-sm text-ink-700">
                          <span className="font-semibold text-ink-950">{t("reviewExchange.response")}</span>
                          <span>{t("reviewExchange.actor.session", { id: response.author_session_id })}</span>
                          <time dateTime={response.at}>{formatDateTime(locale, response.at)}</time>
                          <span className="break-all">{response.handoff_id}</span>
                        </div>
                        <p className="mt-1 whitespace-pre-wrap break-words text-base leading-6 text-ink-950">{response.report}</p>
                      </>
                    ) : (
                      <p className="text-base text-ink-700">{t("reviewExchange.pending")}</p>
                    )}
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}
