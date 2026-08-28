# 取り下げを watch に届ける（ゴール 179）

**取り下げは開いた決定を持たないゴールで無音になる。**`WithdrawActiveGoal` の publish が
`len(openDecisions) > 0` で門を作っているため、決定 0 件のゴールを取り下げると
イベントが 1 つも流れない。担当していた subcommander の `atct watch -goal N` は無音のまま、
その subcommander が起こした executor は働き続ける。

## 何を流すか

### 1. `goal.withdrawn`（新規・無条件）

    EventGoalWithdrawn = "goal.withdrawn"

    type GoalWithdrawnEvent struct {
        GoalID               int64
        ProjectID            int64
        Reason               string
        DroppedTaskIDs       []int64
        ClosedTaskHandoffIDs []string
        WithdrawnDecisionIDs []int64
    }

**`DetectionEvent` を再利用しない。**取り下げは検知ではなく状態遷移であり、
運ぶべき値（理由・畳んだタスクと handoff の一覧）が `DetectionEvent` に無い。
**`DetectionEvent` にフィールドを足す選択も採らない**——ゴール 192 が同じ構造体を
拡張中で、同じ行に二重の変更が入る。

**publish は門を持たない。**決定・タスク・handoff が 0 件でも流す。取り下げの tx は
ゴールの状態を必ず変えるので、流さない理由が無い。`publishAll()` も同様に無条件にする。

新しい型を足したので `internal/httpapi/server.go` の `eventMatchesGoalID` に case を
足す。ここを忘れると `goal_id` 指定の watch では落ちる（`default:` が `false` を返す）。
`eventProjectID` にも足す——`-project` の watch でプロジェクトを引けないと、
`eventProjectID` が 0 を返して「全プロジェクトに通す」側に倒れる。

### 2. 強制完了した task handoff の `handoff_reported`（既存名）

取り下げの tx は `q.CompleteTaskHandoff` を直接呼んで開いた task handoff を全部閉じる
（`goal.go:683-701`）。**通常の `CompleteTaskHandoff` は閉じた後に `handoff_reported` を
publish するが、取り下げ経路はそれを飛ばしている。**閉じ方は同じで通知だけが無い、という
非対称である。ここを揃える。

claim ロック（`handoffIsDelegation` が偽）は通常経路でも publish しないので、同じ条件で外す。

## executor が dropped を知る経路（完了条件 4）

**subcommander が受け取って伝える。executor 向けの経路は作らない。**

理由は 3 つある。

1. **タスク単位のストリームが存在しない。**`atct watch` は `-project` と `-goal` だけを
   受け、`parseEventFilter` も `project_id` / `goal_id` しか読まない。`task_id` を足すには
   `cmd/atct/watch*.go` を触る必要があるが、**ゴール 192 が `formatWatchDecision`
   （`watch.go:821` 付近）を書き換え中である。**
2. **executor は watch を張っていない。**`doc/execution-flow.md` の通り、executor の
   入力は task handoff の依頼、出力は subcommander への報告である。executor ごとに
   ストリームを張ると、192 が再設計中のプロセスに 4 つ目の通知経路が増える。
3. **止められるのは subcommander である。**executor の pane を閉じるのも、依頼を
   取り消すのも subcommander の役割（`atct` スキルの `## Roles`）。取り下げを知るべき
   相手は既に `-goal N` を張っている。

**そのうえで、伝達を判断ではなく機械的な指示にする。**`GoalWithdrawnEvent` が
`DroppedTaskIDs` と `ClosedTaskHandoffIDs` を運ぶので、subcommander は再問い合わせ
なしに「どの executor を止めるか」を名指しできる。一覧を持たない通知なら、受け取った
subcommander が結局 `atct_goal_get` を叩き直すことになる。

さらに、閉じた handoff ごとに `handoff_reported` が流れるので、**executor を抱えている
ケースでは `atct watch -goal N` が今日そのまま行を出す**
（`atct handoff reported: task N (handoff X): <理由>`）。

## 残る欠落

**`atct watch` の CLI は未知のイベント名を黙って捨てる。**`formatWatchDecision` は
名前を完全一致で振り分け、`default:` が `("", false)` を返し、`emitWatchDecisionWithState`
が `!ok` で何も出さずに戻る。**`goal.withdrawn` に case を足さないと、CLI の行は出ない。**

必要な変更は `cmd/atct/watch.go` の 1 case だけだが、**このファイルはゴール 192 が
同じ `formatWatchDecision` を書き換え中である**ため、179 では触らない。

    $ mb=$(git merge-base main wt/goal-192); git diff -U0 $mb wt/goal-192 -- cmd/atct/watch.go | grep '^@@'
    @@ -55,0 +56 @@ type watchDecision struct {
    @@ -821 +822,10 @@ func formatWatchDecision(eventName string, decision watchDecision) (string, bool

**この欠落が実害になるのは「タスクも決定も 0 件のゴールを取り下げたとき」だけ**である。
handoff を抱えているゴールは `handoff_reported` の行が出る。**止めるべき executor が
居ないケースだけが、CLI で無音のまま残る。**ダッシュボードは
`web/src/lib/ui.ts` の `DECISION_EVENT_NAMES` に `goal.withdrawn` を足すので更新される。

## 検査

**決定 0 件と 1 件以上を同じ数だけ置く。**決定 1 件のゴールだけで検査すると、
`len(openDecisions) > 0` の門をそのまま見逃す。

**均衡はテスト自身が検算する。**表に 0 件のケースと 1 件以上のケースを数え、
一致しなければ落とす。規約として書くだけでは、後からケースを足すときに崩れる。
