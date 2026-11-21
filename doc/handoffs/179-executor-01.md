# 依頼: 取り下げイベントを store から流す（ゴール 179 / タスク 805）

あなたは executor です。あなた自身の名前が `atct-179-executor` です。この依頼はあなた宛てです。
報告先は `atct-179-subcommander` です。

## 冒頭で必ず行うこと（ATCT）

First call `atct_session_identify` with a stable session key that remains unchanged for this
session and identifies only you. Your agent name is suitable. Do this before any other atct call.

Then record receipt of the handoff by calling `atct_handoff_receive` with only the `task_id`
provided in this request (**805**). Do this before starting work. Do not pass a handoff ID or
session; ATCT supplies them.

Then invoke the `atct_role` MCP tool with `expected_role` set to `executor`. If it reports
`matches: false`, do not start work; return the task.

When the work is complete, record completion by calling `atct_handoff_complete` with the
`task_id` provided in this request and a `complete_report`. The `complete_report` must say what
was done, what was verified, what could not be verified, and paths changed.

Only then close the task, by calling `atct_task_update` with the `task_id` provided in this
request and `status` set to `done`. This order is required: a terminal status closes any open
task handoff and replaces its `complete_report`, so the report has to be recorded first.

**あなたが呼んでよい atct ツールは 5 つだけです**: `atct_session_identify`,
`atct_handoff_receive`, `atct_role`, `atct_handoff_complete`, `atct_task_update`。
いずれも `task_id` = 805 の範囲に閉じます。

**呼んではならない atct ツール（13 個・名指し）**: `atct_goal_handoff_complete`,
`atct_goal_handoff_receive`, `atct_goal_handoff_request`, `atct_goal_claim`,
`atct_goal_release`, `atct_goal_complete`, `atct_goal_update_content`,
`atct_project_claim`, `atct_project_release`, `atct_task_claim`, `atct_handoff_request`,
`atct_task_declare`, `atct_decision_ask`。

## 0. まず設計をレビューせよ

まず下の設計をレビューせよ。成り立つならそのまま実装に進む。実装上の制約で成り立たないと
判断したら、実装せずに `atct-179-executor` から `atct-179-subcommander` へ差し戻すこと。
判断は依頼を出した側が行う。**あなたが代わりに設計を決めてはならない。**

設計の全文は `doc/specs/2026-08-28-goal-withdrawal-event.md` にある（124 行）。先に読むこと。

## 1. 決定事項（変更不可）

1. **`WithdrawActiveGoal` の publish から `len(openDecisions) > 0` の門を外す。**
   決定・タスク・handoff が 0 件でもイベントを流す。`publishAll()` も無条件にする。
2. **新しいイベント名 `goal.withdrawn` と新しい型 `GoalWithdrawnEvent` を足す。**
   `DetectionEvent` を再利用しない。取り下げは検知ではなく状態遷移で、運ぶべき値
   （理由・畳んだタスクと handoff の一覧）が `DetectionEvent` に無い。
3. **`DetectionEvent` 構造体（`internal/store/wakeup.go` の 66〜75 行）には触らない。**
   フィールドを足すのも禁止。ゴール 192 が同じ行を拡張中である。
4. **`GoalWithdrawnEvent` の宣言位置は `KeepaliveEvent` の閉じ括弧より後**
   （`wakeup.go` の 82 行目以降）。**`DetectionEvent` と `KeepaliveEvent` の間には
   入れないこと。**192 がそこに挿入している。
5. **強制完了した task handoff ごとに `EventHandoffReported` も publish する。**
   通常の `CompleteTaskHandoff`（`internal/store/task_handoff.go:326-354`）は閉じた後に
   これを publish するのに、取り下げ経路（`goal.go:683-701`）は飛ばしている。
   閉じ方が同じで通知だけ無いという非対称を揃える。
   claim ロック（`handoffIsDelegation(requestedBy, receivedBy)` が偽）は通常経路でも
   publish しないので、同じ条件で除外する。
6. **`goal.withdrawn` を最初に publish する。**その後に決定ごとの `decision.withdrawn`、
   その後に handoff ごとの `handoff_reported`、最後に `publishAll()`。
7. **sqlc のクエリを追加しない。**落ちたタスクの ID は tx 内で既存の
   `q.ListTasks(ctx, goalID)` を `DropOpenTasksForGoal` より**前**に呼んで
   `status` が `todo` / `doing` の行を集める。新しい `.sql` を書くと codegen が必要になり、
   他ゴールとの衝突面が広がる。

## 2. 実装項目

### 2-1. `internal/store/wakeup.go`

(a) 12〜30 行の const ブロックの末尾に足す。

    EventGoalWithdrawn = "goal.withdrawn"

(b) `KeepaliveEvent` の閉じ括弧の**後**に型を足す。コメントは下の内容を保つこと
（なぜ一覧を event に載せるかが実装の理由である）。

    // GoalWithdrawnEvent reports that an active goal was withdrawn and what the
    // withdrawal folded up. Withdrawal drops the goal's open tasks and force-closes
    // their handoffs, so the IDs travel with the event: the goal's subcommander is
    // the only watcher scoped to the goal, and it needs to name which executors to
    // stop without a second query.
    type GoalWithdrawnEvent struct {
        GoalID               int64    `json:"goal_id"`
        ProjectID            int64    `json:"project_id"`
        Reason               string   `json:"reason"`
        DroppedTaskIDs       []int64  `json:"dropped_task_ids,omitempty"`
        ClosedTaskHandoffIDs []string `json:"closed_task_handoff_ids,omitempty"`
        WithdrawnDecisionIDs []int64  `json:"withdrawn_decision_ids,omitempty"`
    }

### 2-2. `internal/store/goal.go` の `WithdrawActiveGoal`（635〜718 行のみ）

(a) tx 内、`DropOpenTasksForGoal` の呼び出しより**前**に `q.ListTasks(ctx, goalID)` を
呼び、`status` が `"todo"` または `"doing"` の行の `ID` を `droppedTaskIDs` に集める。
`ListTasks` は `ORDER BY sort_order, id` なので順序はそのまま使う。

(b) `openTaskHandoffs` のループで、`handoffIsDelegation(handoff.RequestedBy.Int64,
handoff.ReceivedBy.Int64)` が真のものだけ `closedHandoffIDs` に `handoff.ID` を集める。
`RequestedBy` / `ReceivedBy` は `sql.NullInt64` なので、NULL は `.Int64` が 0 になり
`handoffIsDelegation` の 0 判定に乗る。

(c) `openDecisions` のループで `withdrawnDecisionIDs` に `decision.ID` を集める。

(d) `tx.Commit()` の後、プロジェクト ID を `s.GetGoal(ctx, goalID)` から取る
（取り下げ済みでも読める）。失敗したら error を返してよい。

(e) publish を次の順に書き換える。**門は一切付けない。**

    s.notify.publishEvent(Event{
        Name: EventGoalWithdrawn,
        Data: GoalWithdrawnEvent{
            GoalID:               goalID,
            ProjectID:            goal.ProjectID,
            Reason:               reason,
            DroppedTaskIDs:       droppedTaskIDs,
            ClosedTaskHandoffIDs: closedHandoffIDs,
            WithdrawnDecisionIDs: withdrawnDecisionIDs,
        },
    })
    // 既存の decision ごとの publish / publishEvent(decision.withdrawn) はそのまま
    // 続いて closedHandoffIDs ごとに EventHandoffReported を publish
    // 最後に s.notify.publishAll() を無条件で呼ぶ

`handoff_reported` の `Data` は既存の `DetectionEvent` を使う（**構造体は変更しない。
既存フィールドに値を入れるだけ**）。`task_handoff.go:343-353` と同じ形にする。

    DetectionEvent{
        DetectionID:    NewDetectionID(),
        ProjectID:      goal.ProjectID,
        GoalID:         goalID,
        TaskID:         <その handoff の TaskID>,
        HandoffID:      <handoff.ID>,
        CompleteReport: reason,
    }

### 2-3. `internal/store/goal_withdraw_test.go` に検査を足す

**新しいテスト 1 本**: `TestWithdrawActiveGoalPublishesGoalWithdrawn`。表駆動で、
ケースは次の 4 つ。**決定 0 件が 2 件、決定 1 件以上が 2 件**である。

| ケース名 | 開いた決定 | タスク |
|---|---|---|
| `no decisions no tasks` | 0 件 | 無し |
| `no decisions with delegated task` | 0 件 | todo 1 件 + 委譲された open な task handoff 1 件 |
| `one decision` | 1 件 | 無し |
| `two decisions with delegated task` | 2 件 | doing 1 件 + 委譲された open な task handoff 1 件 |

**表の均衡をテスト自身が検算する。**表を回す前に、`len(openDecisions)==0` のケース数と
`>0` のケース数を数え、一致しなければ `t.Fatalf` で落とす。**コメントで済ませない。**
落とすときのメッセージに両方の数を出す。

各ケースで検査すること。

- `s.SubscribeEvents()` を `WithdrawActiveGoal` の**前**に張る（`CreateGoal` や
  `AskDecision` が先に流すイベントを拾わないよう、購読は取り下げ直前に張る）
- 届いたイベントを 1 秒のタイマ付きで読み、`EventGoalWithdrawn` が**必ず 1 件**来ること。
  他のイベント（`decision.withdrawn` / `handoff_reported`）が混ざるので、
  名前で振り分けて `goal.withdrawn` を待つループにする
- `Data` が `GoalWithdrawnEvent` 型であること（型アサーションの失敗を `t.Fatalf`）
- `GoalID` / `ProjectID` / `Reason` が期待どおりであること
- `DroppedTaskIDs` が、取り下げ前に todo/doing だったタスクの ID と一致すること
  （`done` のタスクを 1 件混ぜたケースを 1 つ以上作り、**その ID が入っていないこと**も見る）
- `WithdrawnDecisionIDs` が開いていた決定の ID 集合と一致すること（0 件のケースでは空）
- `ClosedTaskHandoffIDs` が強制完了した handoff の ID と一致すること

**新しいテスト 2 本目**: `TestWithdrawActiveGoalPublishesHandoffReportedForClosedHandoffs`。
委譲された open な task handoff を 1 件持つゴールを取り下げ、`EventHandoffReported` が
その handoff について流れること、`DetectionEvent` の `TaskID` / `HandoffID` /
`CompleteReport` が期待どおりであることを見る。
**あわせて claim ロック（`requested_by == received_by`）の handoff では
`handoff_reported` が流れないことを別ケースで見る**（あってはいけないものが無いか）。
claim ロックを作るヘルパは `internal/store/task_handoff_test.go` にある
（`addLiveParentGoalClaim` / `addTestAgentSession` / `addGoalHandoffDirect` の周辺を読む）。

既存ヘルパを使い回すこと。`waitForHandoffReported` / `expectNoHandoffReported` は
`internal/store/task_handoff_test.go:146-180` にある。**`waitForHandoffReported` は
先頭 1 件しか見ないので、`goal.withdrawn` が先に来る取り下げ経路では使えない。**
名前で振り分ける待ち関数を新しく書くこと。

## 3. 読む対象（具体名・これ以外は読まない）

| ファイル | 範囲 | 行数 |
|---|---|---|
| `doc/specs/2026-08-28-goal-withdrawal-event.md` | 全部 | 124 |
| `internal/store/wakeup.go` | 1〜85 | 85 |
| `internal/store/goal.go` | 635〜718 | 84 |
| `internal/store/goal_withdraw_test.go` | 全部 | 174 |
| `internal/store/task_handoff.go` | 290〜360, 405〜429 | 95 |
| `internal/store/task_handoff_test.go` | 1〜200 | 200 |
| `internal/store/handoff.go` | 全部 | 10 |
| `internal/store/queries/task.sql` | 10〜30, 160〜175 | 37 |

合計 809 行。**この一覧に無いファイルを読む必要が出たら、実装せずに差し戻すこと。**
`internal/httpapi/` と `web/` と `cmd/atct/` はこの依頼の範囲外である。

## 4. 触らないもの

- **`internal/store/wakeup.go` の 66〜75 行（`DetectionEvent` 構造体）** — ゴール 192 が拡張中
- **`internal/store/goal.go` の 1〜634 行** — 146 と 183 が別の箇所を編集中。
  特に `RejectCompletion`（517 行付近）と `CompleteGoalWithReport`（434 行付近）に触らない
- **`cmd/atct/watch.go` / `cmd/atct/watch_scope.go`** — 192 が `formatWatchDecision` を書き換え中
- **`internal/httpapi/`**、**`web/`** — 別タスク（806）で扱う
- **`skills/atct/SKILL.md`**、**`script/release.sh`**
- **`internal/store/queries/*.sql` と `internal/store/sqlcgen/`** — クエリを追加しない
- 一覧に無いことを勝手に足さない・削らない。指定外のファイルに触る必要が出たら、
  実装せずに `atct-179-subcommander` に聞くこと。**他の誰かがやってくれると仮定するのも誤りである。**

## 5. 検証（このコマンドをそのまま実行し、実出力を報告に含める）

**このマシンは swap が枯渇しやすい。`go test ./...` は実行しないこと。**
対象パッケージだけに絞る。

新しくできるようになること（4 つ）。

    go build ./internal/store/
    go test ./internal/store/ -run 'TestWithdrawActiveGoalPublishesGoalWithdrawn' -count=1 -timeout 120s -v
    go test ./internal/store/ -run 'TestWithdrawActiveGoalPublishesHandoffReportedForClosedHandoffs' -count=1 -timeout 120s -v
    go vet ./internal/store/

壊れてはいけないこと（4 つ）。

    go test ./internal/store/ -run 'TestWithdrawActiveGoal' -count=1 -timeout 180s
    go test ./internal/store/ -count=1 -timeout 300s
    go test ./internal/daemon/ -run 'TestGoalComplete|Guard' -count=1 -timeout 180s
    go test ./internal/httpapi/ -run 'TestHTTPWithdrawActiveGoal' -count=1 -timeout 180s

**あってはいけないものが無いかの検査**（結果を報告に含める）。

    # DetectionEvent 構造体を触っていないこと
    git diff -U0 -- internal/store/wakeup.go | grep '^@@'
    # 門が残っていないこと（0 件であるべき）
    grep -n 'len(openDecisions) > 0' internal/store/goal.go
    # 新しい sqlc クエリを足していないこと（差分が無いこと）
    git status --porcelain -uall -- internal/store/queries internal/store/sqlcgen

## 6. 禁止事項

- **コミットしない。**`git add` も `git commit` も行わない。コミットは subcommander が行う
- `git checkout` / `git restore` / `git stash` / `git add -A` / `git add .` を使わない
- pane を作らない。サブエージェントを起動しない。再委譲しない
- 権限昇格を独断で行わない（失敗したら報告して返す）
- `herdr` は `herdr agent prompt atct-179-subcommander` だけ使ってよい

## 7. 報告

`atct_handoff_complete`（`task_id` = 805）の `complete_report` に書く。**80 行以内。**
pane へ二重に送らない。含めるべき値。

1. 変更したファイルのパスと、それぞれの追加/削除行数（`git diff --stat` の実出力）
2. 5 章の 8 コマンド + 3 検査の**実出力**（テストは `ok` / `FAIL` の行と、`-v` の
   `--- PASS` 行）
3. 表の均衡検算をどう書いたか（該当コードを 10 行以内で引用）
4. 検証できなかったことがあれば明記する

報告の後に `atct_task_update`（`task_id` = 805, `status` = `done`）を呼ぶ。**この順序を守る。**
