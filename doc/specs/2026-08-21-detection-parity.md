# 検知条件を Stop hook と SSE の両方で扱う

日付: 2026-08-21
ゴール: Stop hook で扱う検知条件は SSE でも通知し、逆も同様にする（goal 62）

## 発端

README のゴールで全タスクを done にしたあと、**完了報告を出し忘れたまま 43 分気づかなかった。**
人間に「これってどうなってる？」と聞かれて発覚した。

## 測ったこと

### SSE はこの条件を最初から運んでいない

`publishEvent` が出すイベントは 6 種類で、全部 `decision.*` である。

    decision.answered  decision.applied  decision.approved
    decision.created   decision.rejected decision.withdrawn

加えて `wakeup` と `wakeup.discrepancy` がある。**検知条件をそのまま運ぶイベントは無い。**

### `wakeup` は未着手タスクがあるときしか出ない

`internal/daemon/wakeup.go` の発火条件はこの 1 行である。

    active := state.ActiveGoalCount > 0 && state.UnstartedTaskCount > 0

**「全タスクが done で完了報告が無い」は未着手タスクが 0 件なので、原理的に発火しない。**
今回見逃したのはこれである。故障ではなく、条件がその形をしていない。

### 運んでいる中身も足りない

`WakeupEvent` が持つのは件数 3 つ（`active_goal_count` / `unstarted_task_count` /
`waiting_answer_count`）だけ。**どの条件が立っているかも、どのゴールかも入っていない。**
一方 `WakeupState` は `CompletedGoals` も `DroppedGoals` も `UnclaimedDoingTasks` も
既に持っている。**計算済みのものを捨てている。**

### 両側の対応表（実測）

| 検知条件 | Stop hook | SSE |
|---|---|---|
| 人間が答えた決定 | あり | あり（`decision.answered`） |
| 既定が適用された決定 | あり | あり（`decision.answered`） |
| 既定の無い決定を待っている | あり | **無し** |
| 作業ロック保持・未着手タスクあり | あり | 部分的（件数のみ） |
| 取り残された作業ロック | あり | **無し** |
| 未宣言の active ゴール | あり | **無し** |
| 完了報告の出し忘れ | あり | **無し** |
| コミットの紐づけ忘れ | あり | **無し** |
| 全タスクが dropped | あり | **無し** |
| 作業ロック無しの doing | あり | **無し** |

**11 条件のうち 8 つが SSE に無い。**

## 決定

### 条件の判定を 1 箇所に集め、両方がそれを読む

いま判定は 2 箇所に分かれている。`internal/store/wakeup.go` が `WakeupState` を作り、
`cmd/atct/pending.go` がそれを読みつつ**自前でも判定している**（コミットの紐づけ忘れは
pending.go の中だけにある）。

**判定は `internal/store` に寄せる。**`pending.go` は文言を組むだけ、daemon はイベントに
載せるだけにする。**同じ条件を 2 箇所で判定すると、いずれ食い違う。**今日、上限 20 が
4 箇所に散って食い違いうる状態だったのを直したばかりである。

### イベントは条件ごとに 1 つにする

`wakeup` を件数の通知のままにせず、**条件ごとの名前を持つイベント**にする。

    detection.completion_report_missing
    detection.commits_missing
    detection.undeclared_goal
    detection.stale_work_lock
    detection.unclaimed_doing
    detection.all_tasks_dropped

**1 つの `wakeup` に詰め込まない。**受け手が「何が起きたか」を文字列の解析で判別する形は、
条件が増えるたびに壊れる。

各イベントは `project_id` と、**対象の goal_id か task_id** を持つ。件数だけでは
受け手が次に何をすればよいか決められない。

### 発火条件から「未着手タスクがあること」を外す

いまの `active := ActiveGoalCount > 0 && UnstartedTaskCount > 0` は、
**未着手タスクの有無を全条件の前提にしている。**完了報告の出し忘れも、紐づけ忘れも、
取り残された作業ロックも、未着手タスクが 0 件で成立する。**条件ごとに判定する。**

### 収束すること

各イベントは、**その条件が解消したら出なくなる。**同じ条件・同じ対象について
繰り返し出さない（`watch` の `delivered` と同じ重複排除を条件ごとに持つ）。
**解消しても出続ける通知は、読まれなくなる。**

### Stop hook 側に足りないものは無い

対応表のとおり、Stop hook は 11 条件すべてを持っている。**この作業は SSE 側を
Stop hook にそろえる片方向である。**逆方向に足すものは、いまは無い。

## やらないこと

- **決定のイベント（`decision.*`）を検知条件に寄せない。**あれは「起きたこと」の通知で、
  検知条件は「いま成り立っている状態」の通知である。別のものとして残す
- 通知の間引きや優先度づけ。**まず全部出す。**うるさければ後で減らす

## 検証

- 全タスクが done で完了報告の無いゴールを作ると `detection.completion_report_missing` が
  1 回だけ出ること
- **完了報告を出すと出なくなること**（収束）
- **未着手タスクが 0 件でも出ること**（今回見逃した条件そのもの。否定側）
- 同じ条件・同じゴールで繰り返し出ないこと（否定側）
- `pending` の出力が、条件の判定を store に寄せた後も変わらないこと
