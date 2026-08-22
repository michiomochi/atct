import { Button } from "@cloudflare/kumo/components/button";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

interface MessageProps {
  children: ReactNode;
}

export function EmptyState({ children }: MessageProps) {
  return <p className="border border-dashed border-line px-4 py-6 text-base text-ink-700">{children}</p>;
}

export function AreaLoading({ label }: { label: string }) {
  const { t } = useTranslation();
  const loadingLabel = t("state.loadingLabel", { label });

  return (
    <div className="space-y-3 border border-line bg-surface px-4 py-5" aria-busy="true" aria-label={loadingLabel}>
      <span className="sr-only">{loadingLabel}</span>
      <div className="h-3 w-1/3 animate-pulse bg-line" />
      <div className="h-3 w-4/5 animate-pulse bg-line" />
      <div className="h-3 w-2/3 animate-pulse bg-line" />
    </div>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  const { t } = useTranslation();

  return (
    <div className="border border-danger-700 bg-danger-100 px-4 py-4 text-base text-danger-700" role="alert">
      <p>{message}</p>
      <Button
        type="button"
        variant="secondary-destructive"
        className="focus-ring mt-3 px-3 py-2 text-base font-medium disabled:cursor-wait disabled:opacity-60"
        onClick={onRetry}
      >
        {t("state.retry")}
      </Button>
    </div>
  );
}
