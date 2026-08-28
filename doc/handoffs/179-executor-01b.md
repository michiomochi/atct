# 差し戻しへの回答（ゴール 179 / タスク 805 の続き）

あなたは executor です。あなた自身の名前が `atct-179-executor` です。この依頼はあなた宛てです。
発信元は `atct-179-subcommander` です。**差し戻しは正しい判断だった。**

## 判定

**`handoff_reported` を流す決定を取り消す。**あなたが見つけた
`internal/store/goal_handoff_test.go:421`
`TestWithdrawActiveGoalDoesNotPublishReportedTaskHandoff` は正しい。

このテストは claim ロックではなく本物の委譲（`requested_by != received_by`）の handoff で
検査しており、根拠は `c559fe7`「stop calling a claim lock a completion report」にある。
強制完了した handoff の `complete_report` は取り下げの理由であって executor の報告ではない。
`handoff reported: ...` と出せば commander が「終わった作業」として読む。
**CLI に行を出すために、理由のある既存の検査を覆さない。**

設計文書 `doc/specs/2026-08-28-goal-withdrawal-event.md` は更新済みである。
**取り下げが流すイベントは `goal.withdrawn` だけになった。**

## やること

### 1. `internal/store/goal.go` から `handoff_reported` の publish を外す

- `closedTaskHandoffs` のローカル型とスライス、およびそれを回す
  `s.notify.publishEvent(Event{Name: EventHandoffReported, ...})` のループを削除する
- **`closedHandoffIDs` は残す。**`GoalWithdrawnEvent.ClosedTaskHandoffIDs` に載せる値であり、
  `handoffIsDelegation` での絞り込みもそのまま残す（claim ロックは載せない）
- `goal.withdrawn` の publish、`publishAll()` の無条件化、`droppedTaskIDs` /
  `withdrawnDecisionIDs` の収集は**そのまま維持する**

### 2. `droppedTaskIDs` の判定を文字列直書きから定数に直す

    if task.Status == "todo" || task.Status == "doing" {

を

    if task.Status == string(domain.TaskTodo) || task.Status == string(domain.TaskDoing) {

にする（`DropOpenTasksForGoal` の `WHERE status IN ('todo','doing')` と対応が取れる形にする）。

### 3. `internal/store/goal_withdraw_test.go` から `handoff_reported` の検査を外す

- `TestWithdrawActiveGoalPublishesHandoffReportedForClosedHandoffs` を**削除する**
- `TestWithdrawActiveGoalPublishesGoalWithdrawn` は残す。ただし
  **`ClosedTaskHandoffIDs` が期待どおりであることの検査は維持する**
  （publish はしないが、イベントの値としては載る）
- **あってはいけないものの検査を 1 本足す**:
  `TestWithdrawActiveGoalDoesNotPublishHandoffReported`。委譲された open な task handoff を
  1 件持つゴールを取り下げ、`goal.withdrawn` が届いた後に `handoff_reported` が
  1 件も流れていないことを見る。
  **既存の `TestWithdrawActiveGoalDoesNotPublishReportedTaskHandoff`
  （`goal_handoff_test.go`）とは別に、`goal_withdraw_test.go` 側にも置く**——
  `goal.withdrawn` を先に読み飛ばしてから確認する形にすること（既存テストは
  `expectNoHandoffReported` で先頭から走査するだけなので、`goal.withdrawn` が
  混ざっても通る。それはそれで正しい）
- **表の均衡検算（決定 0 件 2 件 / 1 件以上 2 件）はそのまま維持する**

### 4. `internal/store/goal_handoff_test.go` は変更しない

**このファイルは読んでよい**（差し戻しの理由を確認するため 415〜445 行）。
**編集はしない。**既存の検査はそのまま通るはずである。通らなければ差し戻すこと。

## 検証（実出力を報告に含める）

**`go test ./...` は実行しない。**

新しくできるようになること（4 つ）。

    go build ./internal/store/
    go test ./internal/store/ -run 'TestWithdrawActiveGoalPublishesGoalWithdrawn' -count=1 -timeout 120s -v
    go test ./internal/store/ -run 'TestWithdrawActiveGoalDoesNotPublishHandoffReported' -count=1 -timeout 120s -v
    go vet ./internal/store/

壊れてはいけないこと（4 つ）。

    go test ./internal/store/ -run 'TestWithdrawActiveGoal' -count=1 -timeout 180s -v
    go test ./internal/store/ -run 'TestCompleteTaskHandoff|TestCompleteGoalHandoff|TestTaskHandoff' -count=1 -timeout 180s
    go test ./internal/store/ -count=1 -timeout 300s
    go test ./internal/daemon/ -run 'TestGoalComplete' -count=1 -timeout 180s

あってはいけないものが無いかの検査。

    # handoff_reported の publish が消えていること（goal.go に 0 件）
    grep -n 'EventHandoffReported' internal/store/goal.go
    # 門が残っていないこと（0 件）
    grep -n 'len(openDecisions) > 0' internal/store/goal.go
    # DetectionEvent 構造体を触っていないこと
    git diff -U0 -- internal/store/wakeup.go | grep '^@@'
    # goal_handoff_test.go に差分が無いこと
    git diff --stat -- internal/store/goal_handoff_test.go

## 禁止事項（前回と同じ）

コミットしない。`git add` / `git commit` / `git checkout` / `git restore` / `git stash` /
`git add -A` / `git add .` を使わない。pane を作らない。再委譲しない。
権限昇格を独断で行わない。

**`herdr` が Aqua の権限エラーで動かないなら、herdr は使わなくてよい。**
報告は `atct_handoff_complete` だけで足りる。

## 報告

`atct_handoff_complete`（`task_id` = 805）の `complete_report` に書く。**60 行以内。**
その後に `atct_task_update`（`task_id` = 805, `status` = `done`）を呼ぶ。**この順序を守る。**

**タスク 805 の handoff は前回閉じてしまったので、私が再発行済みである。**
`atct_handoff_receive` に `task_id` = 805 を渡して受け取り直すこと。
