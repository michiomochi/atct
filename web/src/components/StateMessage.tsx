import { Button } from "@base-ui/react/button";
import type { ReactNode } from "react";

interface MessageProps {
  children: ReactNode;
}

export function EmptyState({ children }: MessageProps) {
  return <p className="border border-dashed border-line px-4 py-6 text-sm text-ink-700">{children}</p>;
}

export function AreaLoading({ label }: { label: string }) {
  return (
    <div className="space-y-3 border border-line bg-surface px-4 py-5" aria-busy="true" aria-label={`${label}を読み込み中`}>
      <span className="sr-only">{label}を読み込み中</span>
      <div className="h-3 w-1/3 animate-pulse bg-line" />
      <div className="h-3 w-4/5 animate-pulse bg-line" />
      <div className="h-3 w-2/3 animate-pulse bg-line" />
    </div>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="border border-danger-700 bg-danger-100 px-4 py-4 text-sm text-danger-700" role="alert">
      <p>{message}</p>
      <Button
        type="button"
        className="focus-ring mt-3 border border-danger-700 bg-surface px-3 py-2 text-sm font-medium text-danger-700 transition hover:bg-danger-100 disabled:cursor-wait disabled:opacity-60"
        onClick={onRetry}
      >
        再試行
      </Button>
    </div>
  );
}
