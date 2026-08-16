import type { Goal } from "../lib/api";
import { encodePathSegment, formatDate, statusLabel } from "../lib/ui";
import { Table } from "@cloudflare/kumo/components/table";
import { EmptyState } from "./StateMessage";

interface Props {
  goals: Goal[];
}

const columnScope = { scope: "col" } as const;

export function GoalTable({ goals }: Props) {
  if (goals.length === 0) return <EmptyState>No active goals are in progress. Resume work on a goal to see it here.</EmptyState>;

  return (
    <div className="table-scroll">
      <Table className="min-w-[42rem] w-full border-collapse text-left text-sm">
        <caption className="sr-only">Active goal list</caption>
        <Table.Header className="border-b-2 border-ink-300 text-xs uppercase tracking-wide text-ink-700">
          <Table.Row>
            <Table.Head {...columnScope} className="px-3 py-3 font-semibold">Goal</Table.Head>
            <Table.Head {...columnScope} className="w-36 px-3 py-3 font-semibold">Status</Table.Head>
            <Table.Head {...columnScope} className="w-48 px-3 py-3 font-semibold">Updated at</Table.Head>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {goals.map((goal) => (
            <Table.Row className="border-b border-line align-top last:border-b-0" key={goal.id}>
              <Table.Cell className="px-3 py-4">
                <a
                  className="focus-ring text-clamp-2 font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                  href={`/goals/${encodePathSegment(goal.id)}`}
                  title={goal.title}
                >
                  {goal.title}
                </a>
                <p className="mt-1 font-mono text-xs text-ink-500">{goal.id}</p>
              </Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{statusLabel(goal.status)}</Table.Cell>
              <Table.Cell className="px-3 py-4 text-ink-700">{formatDate(goal.updated_at)}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
    </div>
  );
}
