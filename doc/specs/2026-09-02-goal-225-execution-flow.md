# Goal 225: execution-flow を実装する

## Outcome

`doc/execution-flow.md` を commander / subcommander / executor の実装上の正本にする。goal、plan、task の handoff は request・receive・review request・review receive・complete・reject を状態として持ち、作業者ではなくレビューした委譲者が handoff を閉じる。goal の人間レビューと統合後の6部完成報告は commander が書く。

この変更は、task ごとに worker を作り直す変更ではない。subcommander は未委譲 task が残る間、完了済みの executor pane に新しい task handoff を渡す。新しい pane は並列性、worktree 分離、コンテキスト枯渇、または話題変更が必要な場合だけ作る。

## Evidence and scope

調査は [2026-09-02-goal-225-execution-flow-gaps.md](../investigations/2026-09-02-goal-225-execution-flow-gaps.md) にある。現在の store / migration / daemon / MCP surface には plan handoff と review 遷移が存在せず、task status も handoff completion から独立している。既存の B1〜B4 の設計決定は [2026-09-01-execution-flow-b1-b4.md](2026-09-01-execution-flow-b1-b4.md) を維持し、本 goal はそれを実装可能な状態機械へ接続する。

対象は handoff state、role 導出、通知、MCP/CLI contract、手順文書と回帰テストである。無関係な worktree 作成、公開、chezmoi apply は対象外である。

## Decisions

### 1. review は handoff ごとの状態であり、task status はその派生である

task handoff は `requested -> received -> review_requested -> review_received -> completed` を通常経路とする。reviewer が reject した場合は handoff を閉じず `received` へ戻し、executor は同じ task / pane で修正する。task status は handoff の transaction で `todo -> doing -> review -> done` と更新し、reject は `doing` へ戻す。これにより task status と handoff が食い違わない。

goal handoff は同じ review protocol を使う。plan handoff は subcommander が設計成果物を commander に出す逆向きの review record であり、task claim を作らない。plan が受理されるまで task handoff を request できない。

### 2. claim と receive response は同じ事実を返す

project claim、open かつ received な goal handoff、open かつ received な task handoff の順で role を導出する。receive は caller の stable session key を確定する唯一の入口とし、成功 response へ role と claim evidence を含める。`atct_role` は診断 API として残すが、通常の開始手順の必須な二回目呼び出しにはしない。

### 3. 完成報告は commander に集約し、二つの report を混ぜない

subcommander は goal handoff review を request する。commander が受理・レビュー・complete を行い、その report は「goal handoff を受理して閉じた」要約である。人間 review の承認後、commander は main への統合を行い、`goals` の唯一の6部完成報告を書く。人間却下時は commander が新しい goal handoff を request し、subcommander は receive から再開する。

### 4. 新しい API は対象名を含む。互換性は明示的に移行する

task の外部 API は `task.handoff.*` / `atct_task_handoff_*` とする。generic `handoff.*` は同じ release で黙って意味を変えず、互換 alias と deprecation test を持つか、利用者を全て移行した後に削除する。plan / review API は store、daemon、MCP schema、CLI の各層で同じ名前を使う。

### 5. notification は状態遷移の一回の書き手から publish する

review request、review receive、review reject、human goal review の各 event は state transition transaction が publish する。SSE filter、`atct watch`、Codex/Claude monitor は goal / project scope を保った同じ event を配送する。receiver は重複を作らず、通知で起きた role が該当 handoff を受理して次へ進む。

Goal 219 の completion decision #613 は 2026-09-02 17:06 に `rejected -> applied` となったが、subcommander monitor には rejection が届かず、ユーザー会話後の poll で初めて認識された。`internal/store/notify.go` の `publishEvent` は容量16の channel に `default` 送信を行うため、遅い/切断中 subscriber の event を捨てる。既存の再接続 snapshot は `/api/inbox` の `unapplied_decisions` だけなので、即時 applied になった completion rejection を回復できない。

resume は snapshot だけで済ませず、state transition と同一 transaction で durable event outbox に書かれた scope 内の遷移を cursor/sequence で照合する。outbox は `(project_id, sequence)` を主キーにし、sequence は project 内で単調増加する。stable event ID は `project_id:sequence`、payload は event type・goal_id・task_id・transition data・occurred_at である。post-commit の `publishEvent` は低遅延の最適化であり、outbox の代替ではない。

watcher は durable delivery cursor を `(watcher_key, project_id, goal_id nullable)` で持つ。render が成功した後だけ cursor を進めるため、crash/restart は同じ event を再表示しうる。従って契約は **durable at-least-once delivery** であり、process restart をまたぐ exactly-once ではない。live event と reconnect backfill の重複は、実行中 process の bounded `project_id:sequence` set で抑止する。reconnect は high-watermark までの outbox を cursor から読み、live stream と event ID で merge する。

outbox は project ごとに直近 10,000 event か30日を保持する。cursor が最古 retained sequence より前なら API は `stale_cursor`（HTTP 410 と最古/current sequence）を返す。watcher は current goal/project state、open/review handoff、open/unapplied decisions を出力する full scoped reconciliation を行い、その出力が成功してから cursor を current sequence へ進める。保持範囲内では #613 型の applied decision rejection と completed-handoff reopen の request/receive を必ず backfill する。Goal 222 の health persistence には依存しない。

### 6. 設定変更は source と approved diff を分ける

ATCT / orchestration の手順変更は管理 source を編集する。chezmoi 管理である場合は `chezmoi diff` を提示して明示承認を待つ。承認前に `chezmoi apply` は呼ばない。実装 task はこの境界を越えない。

## Work-unit boundaries

1. migration と store state machine: plan/review records、status transaction、claim/authorization。
2. daemon / MCP / CLI contract: named APIs、receive response、commander-only completion、compatibility aliases。
3. events / watch / monitors: publish、scope filters、wake-up、cursor-backed reconciliation、重複抑止。
4. skills / wrapper contracts: worker reuse conditions、role-specific review flow、approved-diff-only chezmoi operation。
5. end-to-end regression: commander → subcommander → executor、reject/retry、human approval/rejection、worker reuse/new-worker conditions。

Units 1 and 2 are serial. Unit 3 starts after event types are fixed by unit 1. Unit 4 starts after API names are fixed by unit 2. Unit 5 is last. No executor receives an implementation unit until the commander accepts the accompanying plan.

## Acceptance criteria

- Store and daemon tests prove every allowed transition, reject invalid ordering, and preserve a received handoff on review reject.
- MCP/schema and CLI tests prove the named task / goal / plan review contracts and receive role payload.
- SSE, watch, and both monitor paths deliver only the intended goal/project notifications; reconnect reconciliation is durable at-least-once across restart, suppresses live/backfill duplicates only within the running watcher process, and permits replay after a crash/restart for a dropped decision rejection or completed-handoff reopen.
- End-to-end tests prove that a free executor receives a later unassigned task without a new pane, while documented isolation/context/topic conditions create a new one.
- Role and completion tests prove commander alone closes goal review and writes the final six-part goal report.
- Skills and wrapper tests agree with the implementation; any chezmoi source change has a reviewed diff and no apply occurs before explicit approval.

## Non-goals and open validation

The Task 3 implementation design review read the existing HTTP human-review path and its approval tests. The existing `/api/decisions/{id}/approve|reject` endpoints durably persist open/answered/applied decision state and already support goal-scoped records (`KindCompletion` and `KindGoalApproval`) with no task ID. Task 3 therefore reuses the `decisions` table and HTTP decision lifecycle with a distinct goal-review kind, rather than adding a parallel `goal_reviews` table or overloading the final completion report. The commander requests a goal review, human approval/rejection is persisted through the existing HTTP decision endpoint, and only the commander’s later `goal.complete` call writes the final six report fields and closes the goal. Rejection leaves the goal active and does not reopen a handoff automatically; a commander explicitly creates the next handoff.

This specification also does not change the existing withdrawal gate without confirming `WithdrawActiveGoal` behavior. That remains an explicit plan validation step, not implicit implementation work.
