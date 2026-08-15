import type { Goal } from "../lib/api";
import { encodePathSegment, formatDate, statusLabel } from "../lib/ui";
import { EmptyState } from "./StateMessage";

interface Props {
  goals: Goal[];
}

export function GoalTable({ goals }: Props) {
  if (goals.length === 0) return <EmptyState>No active goals are in progress. Resume work on a goal to see it here.</EmptyState>;

  return (
    <div className="table-scroll">
      <table className="min-w-[42rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">Active goal list</caption>
        <thead className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <tr>
            <th className="px-3 py-3 font-semibold" scope="col">Goal</th>
            <th className="w-36 px-3 py-3 font-semibold" scope="col">Status</th>
            <th className="w-48 px-3 py-3 font-semibold" scope="col">Updated at</th>
          </tr>
        </thead>
        <tbody>
          {goals.map((goal) => (
            <tr className="border-b border-line align-top last:border-b-0" key={goal.id}>
              <td className="px-3 py-4">
                <a
                  className="focus-ring text-clamp-2 font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                  href={`/goals/${encodePathSegment(goal.id)}`}
                  title={goal.title}
                >
                  {goal.title}
                </a>
                <p className="mt-1 font-mono text-xs text-ink-500">{goal.id}</p>
              </td>
              <td className="px-3 py-4 text-ink-700">{statusLabel(goal.status)}</td>
              <td className="px-3 py-4 text-ink-700">{formatDate(goal.updated_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
