import type { ReactNode } from "react";

interface Props {
  id: string;
  title: string;
  count?: number;
  children: ReactNode;
}

export function Section({ id, title, count, children }: Props) {
  return (
    <section className="pt-5" aria-labelledby={`${id}-heading`}>
      <div className="mb-4 flex items-baseline justify-between gap-4">
        <h2 id={`${id}-heading`} className="font-display text-lg font-semibold tracking-tight text-ink-950">
          {title} <span className="font-mono text-sm font-normal text-ink-500">{count === undefined ? "..." : count}</span>
        </h2>
      </div>
      {children}
    </section>
  );
}
