import { Button } from "@cloudflare/kumo/components/button";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { formatDateTime } from "../i18n";
import type { Handoff } from "../lib/api";
import { EmptyState } from "./StateMessage";

interface Props {
  handoffs: Handoff[];
  fetchHistory: (handoffID: string, afterID: number) => Promise<Handoff>;
}

function mergeHandoff(current: Handoff, next: Handoff): Handoff {
  const entries = new Map(current.entries.map((entry) => [entry.id, entry]));
  for (const entry of next.entries) entries.set(entry.id, entry);
  return {
    ...current,
    ...next,
    entries: Array.from(entries.values()).sort((left, right) => left.id - right.id),
  };
}

export function HandoffTimeline({ handoffs, fetchHistory }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  const [items, setItems] = useState(handoffs);
  const [loadingID, setLoadingID] = useState<string | null>(null);
  const [error, setError] = useState<{ handoffID: string; message: string } | null>(null);

  useEffect(() => {
    setItems(handoffs);
  }, [handoffs]);

  const loadMore = async (handoff: Handoff) => {
    setLoadingID(handoff.id);
    setError(null);
    try {
      const page = await fetchHistory(handoff.id, handoff.next_after_id);
      setItems((current) => current.map((item) => item.id === handoff.id ? mergeHandoff(item, page) : item));
    } catch (reason) {
      setError({ handoffID: handoff.id, message: reason instanceof Error ? reason.message : t("handoff.timeline.error") });
    } finally {
      setLoadingID(null);
    }
  };

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="handoff-timeline" aria-labelledby="handoff-timeline-heading">
      <h2 id="handoff-timeline-heading" className="font-display text-lg font-semibold text-ink-950">
        {t("handoff.timeline.title")}
      </h2>
      {items.length === 0 ? (
        <div className="mt-4">
          <EmptyState>{t("handoff.timeline.empty")}</EmptyState>
        </div>
      ) : (
        <div className="mt-5 space-y-6">
          {items.map((handoff) => (
            <article className="min-w-0 border border-line bg-surface px-4 py-4" key={handoff.id}>
              <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
                <h3 className="break-words font-display text-base font-semibold text-ink-950">
                  {t("handoff.timeline.thread", { scope: handoff.scope, id: handoff.id })}
                </h3>
                <p className="break-all text-sm text-ink-700">{handoff.id}</p>
              </div>
              {handoff.entries.length === 0 ? (
                <p className="mt-4 text-base text-ink-700">{t("handoff.timeline.noEntries")}</p>
              ) : (
                <ol className="mt-4 space-y-4 border-l-2 border-line pl-4">
                  {handoff.entries.map((entry) => (
                    <li className="min-w-0" key={entry.id}>
                      <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-sm text-ink-700">
                        <span className="font-semibold text-ink-950">{entry.kind}</span>
                        <span>{t("handoff.timeline.id", { id: entry.id })}</span>
                        {entry.in_reply_to_id !== undefined && <span>{t("handoff.timeline.reply", { id: entry.in_reply_to_id })}</span>}
                        <span>{t("handoff.timeline.author", { author: entry.author_session_id })}</span>
                        <time dateTime={entry.created_at}>{formatDateTime(locale, entry.created_at)}</time>
                      </div>
                      <p className="mt-1 whitespace-pre-wrap break-words text-base leading-6 text-ink-950">{entry.body}</p>
                    </li>
                  ))}
                </ol>
              )}
              {error?.handoffID === handoff.id && <p className="mt-3 text-base text-danger-700" role="alert">{error.message}</p>}
              {handoff.has_more && (
                <Button
                  type="button"
                  variant="outline"
                  className="focus-ring mt-4 px-3 py-2 text-base font-medium disabled:cursor-wait disabled:opacity-60"
                  disabled={loadingID === handoff.id}
                  onClick={() => void loadMore(handoff)}
                >
                  {loadingID === handoff.id ? t("handoff.timeline.loading") : t("handoff.timeline.loadMore")}
                </Button>
              )}
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
