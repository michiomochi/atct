import type { ReactNode } from "react";

interface Props {
  title: string;
  count?: number;
  children: ReactNode;
}

export function Section({ title, count, children }: Props) {
  return (
    <section className="border-t border-line pt-5" aria-labelledby={`${title}-heading`}>
      <div className="mb-4 flex items-baseline justify-between gap-4">
        <h2 id={`${title}-heading`} className="font-display text-lg font-semibold tracking-tight text-ink-950">
          {title} <span className="font-mono text-sm font-normal text-ink-500">{count === undefined ? "..." : count}</span>
        </h2>
      </div>
      {children}
    </section>
  );
}
