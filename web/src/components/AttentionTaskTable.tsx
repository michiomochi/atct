import type { TaskView } from "../lib/api";
import { encodePathSegment, formatHeldFor, statusLabel } from "../lib/ui";
import { EmptyState } from "./StateMessage";

interface Props {
  tasks: TaskView[];
}

export function AttentionTaskTable({ tasks }: Props) {
  if (tasks.length === 0) return <EmptyState>No tasks are related to an outstanding decision. They will appear here when a decision needs attention.</EmptyState>;

  return (
    <div className="table-scroll">
      <table className="min-w-[58rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">Tasks needing attention</caption>
        <thead className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <tr>
            <th className="px-3 py-3 font-semibold" scope="col">Task</th>
            <th className="w-40 px-3 py-3 font-semibold" scope="col">Goal</th>
            <th className="w-32 px-3 py-3 font-semibold" scope="col">Status</th>
            <th className="w-44 px-3 py-3 font-semibold" scope="col">Claimed by</th>
            <th className="w-32 px-3 py-3 font-semibold" scope="col">Claim duration</th>
          </tr>
        </thead>
        <tbody>
          {tasks.map((task) => (
            <tr className="border-b border-line align-top last:border-b-0" key={task.id}>
              <td className="px-3 py-4">
                <p className="text-clamp-2 max-w-[34rem] font-medium text-ink-950" title={task.title}>{task.title}</p>
                <p className="mt-1 font-mono text-xs text-ink-500">{task.id}</p>
              </td>
              <td className="px-3 py-4">
                <a
                  className="focus-ring font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                  href={`/goals/${encodePathSegment(task.goal_id)}`}
                >
                  {task.goal_id}
                </a>
              </td>
              <td className="px-3 py-4 text-ink-700">{statusLabel(task.status)}</td>
              <td className="px-3 py-4 text-ink-700">{task.claimed_by || "Unclaimed"}</td>
              <td className="px-3 py-4 text-ink-700">{task.claimed_by ? formatHeldFor(task.held_for_seconds) : "-"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
