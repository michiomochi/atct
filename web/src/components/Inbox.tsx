import { useCallback, useEffect, useState } from "react";
import { fetchInbox, subscribeToDecisionEvents, type InboxResponse } from "../lib/api";
import { AttentionTaskTable } from "./AttentionTaskTable";
import { DecisionTable } from "./DecisionTable";
import { GoalTable } from "./GoalTable";
import { AreaLoading, ErrorState } from "./StateMessage";
import { Section } from "./Section";

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: InboxResponse }
  | { kind: "error"; message: string };

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "受信箱の取得に失敗しました。";
}

export function Inbox() {
  const [state, setState] = useState<LoadState>({ kind: "loading" });

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      setState({ kind: "ready", data: await fetchInbox() });
    } catch (reason) {
      setState({ kind: "error", message: errorMessage(reason) });
    }
  }, []);

  useEffect(() => {
    void load();
    return subscribeToDecisionEvents(() => void load());
  }, [load]);

  const data = state.kind === "ready" ? state.data : undefined;
  const retry = () => void load();

  return (
    <main className="space-y-10">
      <div className="border-b border-line pb-6">
        <p className="font-mono text-xs uppercase tracking-[0.18em] text-accent-700">Inbox</p>
        <h1 className="mt-2 font-display text-3xl font-semibold tracking-tight text-ink-950">受信箱</h1>
        <p className="mt-3 max-w-2xl text-sm leading-6 text-ink-700">
          判断、適用待ちの回答、注意が必要な Task、進行中の Goal を確認できます。
        </p>
      </div>

      <Section title="判断待ち" count={data?.open_decisions.length}>
        {state.kind === "loading" && <AreaLoading label="判断待ちを読み込み中" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <DecisionTable decisions={data.open_decisions} emptyText="判断待ちはありません。" />}
      </Section>

      <Section title="回答済み・未適用" count={data?.unapplied_decisions.length}>
        {state.kind === "loading" && <AreaLoading label="回答済み・未適用を読み込み中" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <DecisionTable decisions={data.unapplied_decisions} emptyText="回答済み・未適用はありません。" />}
      </Section>

      <Section title="注意が必要な Task" count={data?.attention_tasks.length}>
        {state.kind === "loading" && <AreaLoading label="注意が必要な Task を読み込み中" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <AttentionTaskTable tasks={data.attention_tasks} />}
      </Section>

      <Section title="進行中の Goal" count={data?.active_goals.length}>
        {state.kind === "loading" && <AreaLoading label="進行中の Goal を読み込み中" />}
        {state.kind === "error" && <ErrorState message={state.message} onRetry={retry} />}
        {data && <GoalTable goals={data.active_goals} />}
      </Section>
    </main>
  );
}
