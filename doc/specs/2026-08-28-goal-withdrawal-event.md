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

### 2. 強制完了した task handoff の `handoff_reported` は流さない（一度採って取り消した）

取り下げの tx は `q.CompleteTaskHandoff` を直接呼んで開いた task handoff を全部閉じる
（`goal.go:683-701`）。通常の `CompleteTaskHandoff` は閉じた後に `handoff_reported` を
publish するのに、取り下げ経路はそれを飛ばしている。**この非対称を揃えれば、既存の CLI が
そのまま行を出す**——ので最初はそう決めた。**取り消した。**

**既存の検査がこの沈黙を意図として固定している。**

    internal/store/goal_handoff_test.go:421
    func TestWithdrawActiveGoalDoesNotPublishReportedTaskHandoff

このテストは claim ロックではなく**本物の委譲**（`requested_by != received_by`）の handoff で
検査しており、取り下げでは `handoff_reported` が流れないことを要求する。根拠は
`c559fe7`「stop calling a claim lock a completion report」にある。

> Nobody delegated it and nobody reports on it, yet its completion was announced
> all the same. A commander read three such lines as completion reports in one
> day and diagnosed the tree against work that had not started.

**強制完了した handoff の `complete_report` は取り下げの理由であって、executor の報告ではない。**
`handoff reported: task N ... <理由>` と出せば、commander が「終わった作業」として読む——
`c559fe7` が消したのと同じ嘘になる。**CLI に行を出すために、理由のある既存の検査を
覆さない。**

したがって取り下げが流すイベントは `goal.withdrawn` **だけ**である。

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

**ただしこの伝達は、下の「残る欠落」が埋まるまで CLI の行としては届かない。**
SSE / WebSocket の購読者（ダッシュボード）には届く。

## 残る欠落

**`atct watch` の CLI は未知のイベント名を黙って捨てる。**`formatWatchDecision` は
名前を完全一致で振り分け、`default:` が `("", false)` を返し、`emitWatchDecisionWithState`
が `!ok` で何も出さずに戻る。**`goal.withdrawn` に case を足さないと、CLI の行は出ない。**

既存の検査がこの落とし方を意図として書いている。

    $ go test ./cmd/atct/ -run 'TestEmitWatchDetectionIgnoresUnknownEvent' -count=1 -v
    === RUN   TestEmitWatchDetectionIgnoresUnknownEvent
    --- PASS: TestEmitWatchDetectionIgnoresUnknownEvent (0.00s)
    ok  	github.com/michiomochi/atct/cmd/atct	0.710s

必要な変更は `formatWatchDecision` に 1 case だけである。**`watchDecision` は
`GoalID`（`json:"goal_id"`）と `Reason`（`json:"reason"`）を既に持っているので、
構造体の変更は要らない。**

    case store.EventGoalWithdrawn:  // 定数は使わず "goal.withdrawn" 直書きが既存の作法
        return fmt.Sprintf("atct goal withdrawn (goal_id: %s): %s", decision.GoalID, decision.Reason), true

**それでも 179 では触らない。**ゴール 192 が同じ `formatWatchDecision` を書き換え中である。

    $ mb=$(git merge-base main wt/goal-192); git diff -U0 $mb wt/goal-192 -- cmd/atct/watch.go | grep '^@@'
    @@ -55,0 +56 @@ type watchDecision struct {
    @@ -821 +822,10 @@ func formatWatchDecision(eventName string, decision watchDecision) (string, bool

**この欠落は取り下げの全ケースに及ぶ。**取り下げると `atct watch -goal N` は今のところ
無音のままである——`detection.all_tasks_dropped` も助けにならない。検知器は
`wakeup.go:180` で `goal.Status != domain.GoalActive` を `continue` するので、
**取り下げ済みのゴールは検知の対象から外れる。**

ダッシュボードは `web/src/lib/ui.ts` の `DECISION_EVENT_NAMES` に `goal.withdrawn` を
足すので更新される。**192 がマージされた後に、上の 1 case を当てる必要がある。**

## 検査

**決定 0 件と 1 件以上を同じ数だけ置く。**決定 1 件のゴールだけで検査すると、
`len(openDecisions) > 0` の門をそのまま見逃す。

**均衡はテスト自身が検算する。**表に 0 件のケースと 1 件以上のケースを数え、
一致しなければ落とす。規約として書くだけでは、後からケースを足すときに崩れる。
