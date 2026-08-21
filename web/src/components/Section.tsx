import type { ReactNode } from "react";

interface Props {
  id: string;
  title: string;
  count?: number;
  action?: ReactNode;
  children: ReactNode;
}

export function Section({ id, title, count, action, children }: Props) {
  return (
    <section className="pt-5" aria-labelledby={`${id}-heading`}>
      <div className="mb-4 flex items-baseline justify-between gap-4">
        <h2 id={`${id}-heading`} className="font-display text-lg font-semibold tracking-tight text-ink-950">
          {title} <span className="font-mono text-[0.9em] font-normal text-ink-500">{count === undefined ? "..." : count}</span>
        </h2>
        {action}
      </div>
      {children}
    </section>
  );
}
