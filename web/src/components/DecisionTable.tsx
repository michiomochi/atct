import type { Decision } from "../lib/api";
import { encodePathSegment, formatDate, statusLabel } from "../lib/ui";
import { EmptyState } from "./StateMessage";

interface Props {
  decisions: Decision[];
  emptyText: string;
}

export function DecisionTable({ decisions, emptyText }: Props) {
  if (decisions.length === 0) return <EmptyState>{emptyText}</EmptyState>;

  return (
    <div className="table-scroll">
      <table className="min-w-[58rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">判断の一覧</caption>
        <thead className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <tr>
            <th className="px-3 py-3 font-semibold" scope="col">質問</th>
            <th className="w-36 px-3 py-3 font-semibold" scope="col">状態</th>
            <th className="w-52 px-3 py-3 font-semibold" scope="col">回答</th>
            <th className="w-40 px-3 py-3 font-semibold" scope="col">回答者</th>
            <th className="w-48 px-3 py-3 font-semibold" scope="col">Goal</th>
            <th className="w-44 px-3 py-3 font-semibold" scope="col">作成日時</th>
          </tr>
        </thead>
        <tbody>
          {decisions.map((decision) => {
            const answer = [decision.answer_label, decision.answer_text].filter(Boolean).join(" — ");
            return (
              <tr className="border-b border-line align-top last:border-b-0" key={decision.id}>
                <td className="px-3 py-4">
                  <p className="text-clamp-2 max-w-[34rem] font-medium text-ink-950" title={decision.question}>
                    {decision.question}
                  </p>
                  <p className="mt-1 font-mono text-xs text-ink-500">{decision.kind}</p>
                </td>
                <td className="px-3 py-4 text-ink-700">{statusLabel(decision.status)}</td>
                <td className="px-3 py-4 text-ink-700">
                  {answer ? <p className="text-clamp-2" title={answer}>{answer}</p> : "—"}
                </td>
                <td className="px-3 py-4 text-ink-700">{decision.answered_by || "—"}</td>
                <td className="px-3 py-4">
                  <a
                    className="focus-ring font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                    href={`/goals/${encodePathSegment(decision.goal_id)}`}
                  >
                    {decision.goal_id}
                  </a>
                </td>
                <td className="px-3 py-4 text-ink-700">{formatDate(decision.created_at)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
