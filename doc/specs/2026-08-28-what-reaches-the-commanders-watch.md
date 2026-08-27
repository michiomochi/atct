# commander の watch に何が届くか

ゴール 184。`atct watch` が流すイベントを、**受け手の粒度**で分類する。

## 問題

commander は `atct watch`（`-goal` なし＝プロジェクト全体）しか選べない。
2026-08-27 の実測では、届いた通知のうち

    task handoff の完了            63 通 -> commander の行動 0 件
    他人の決定の既定適用           11 通 -> commander の行動 0 件
    goal handoff の完了            28 通 -> 28 件すべて行動
    人間の承認・却下               34 通 -> 34 件すべて行動

ゴール 178 は subcommander が commander へ「送る」ことを止めたが、
ATCT -> commander の watch という流路は手つかずだった。**口を閉じても受信箱は開いていた。**

## 決めたこと

### 1. 分類の軸は「役割」ではなく「watch の走査範囲（scope）」である

commander の推奨は「`session.role` から既定を導く」だった。**採らない。**
`atct watch` は CLI プロセスであり、agent session を持たない。
`-session` フラグも環境変数も無く、`atct_role` が使う claim は MCP セッションに紐づく。
役割から導くには watch にセッション識別を持たせる新規作業が要り、それは 184 の完了条件のどれでもない。

代わりに、既にある `-goal` の有無を軸にする。

    goal scope     `-goal N` あり。**subcommander。**そのゴールのイベントを全部流す（今までどおり）
    project scope  `-goal` なし。**commander。**ゴール粒度のイベントだけ流す

`-goal` の有無は今日すでに `runWatch` まで届いている値なので、新しい入力を増やさない。

### 2. task と goal の区別は `task_id` の有無で付く

`EventHandoffReported`（`handoff_reported`）は task handoff と goal handoff の両方が使う。
イベント名では粒度が分からない。**実装を確認した結果、`task_id` で分けられる。**

    internal/store/task_handoff.go  CompleteTaskHandoffReport -> DetectionEvent{GoalID, TaskID, HandoffID}
    internal/store/goal_handoff.go  CompleteGoalHandoffReport -> DetectionEvent{GoalID,         HandoffID}

goal handoff は `TaskID` を積まない。よって `task_id == ""` が goal 粒度である。
これが 63 通を落として 28 通を残す仕掛けそのものである。

### 3. 分類表

project scope（commander）で流すものだけを列挙する。goal scope は全件そのまま流す。

| イベント | project scope | 理由 |
|---|---|---|
| `decision.approved` | 流す | 人間の承認。完了条件 3 |
| `decision.rejected` | 流す | 人間の却下。完了条件 3 |
| `decision.answered`（人間が答えた） | 流す | 同上 |
| `decision.answered`（既定適用） | **止める** | 出した本人が poll する。実測 11 通・行動 0 件 |
| `goal.created` | 流す | 新しい active ゴールへの気づき。実測 2 回の 1 回 |
| `wakeup` | 流す（ただし後述の間引きあり） | 放置ゴールへの気づき。実測 2 回のもう 1 回 |
| `wakeup.discrepancy` | 流す | 検知器の不整合。daemon の故障であり誰の担当でもない |
| `wakeup.evaluate_failed` | 流す | 同上 |
| `handoff_reported`（`task_id` なし） | 流す | goal handoff の完了。実測 28 通・全件行動 |
| `handoff_reported`（`task_id` あり） | **止める** | task handoff の完了。実測 63 通・行動 0 件 |
| `handoff_yielded` | **止める** | task 単位のみ |
| `detection.completion_report_missing` | 流す | goal 粒度 |
| `detection.commits_missing` | 流す | goal 粒度 |
| `detection.undeclared_goal` | 流す | goal 粒度 |
| `detection.all_tasks_dropped` | 流す | goal 粒度 |
| `detection.unclaimed_doing` | **止める** | task 粒度。担当 subcommander の watch に届いている |
| `detection.handoff_unreceived` | **止める** | task handoff のみを見る検知（`GetTaskGoalID` 経由） |
| `detection.handoff_unreported` | **止める** | 同上 |
| `detection.claim_undelegated` | **止める** | task 粒度 |
| `detection.claim_stale` | **止める** | task 粒度 |
| `detection.decision_answered_unapplied` | **止める** | 完了条件 4。出した本人が poll する |
| `detection.decision_default_unapplied` | **止める** | 同じ理由 |

**未知のイベント名は project scope で流す。**落とすほうを既定にすると、
新しいイベントを足した人が分類表を知らないまま commander から消える。流して気づかせる。

### 4. 完了条件 4 は「ゴール単位」までしか絞れない

`detection.decision_answered_unapplied` を「その決定を出したセッションにだけ」届けるには、
watch が自分の agent session を知っている必要がある。持っていない（決定 1 で述べたとおり）。
`domain.Decision` には `AgentSessionID` があるので daemon 側の材料は揃っているが、
受け手側が名乗れない。

実際に運用している形では、ゴール N の決定を適用できるのはゴール N の subcommander であり、
その watch は `-goal N` である。よって **goal scope に流し、project scope で止める**ことで
条件 4 の目的（commander に流さない・出した本人には届く）は満たす。
セッション単位の厳密な宛先指定は別ゴールとして残す。

### 5. wakeup の 30 秒ごとの再送を止める

watch は wakeup を**描画内容が変わらなければ**落とす。それでも 30 秒ごとに流れていたのは、
行に `unstarted_tasks` `untouched_tasks` `delegated_tasks` という task 粒度の数が入っており、
**task が動くたびに内容が変わる**からである。

project scope では、**ゴール粒度の 3 項目
（`actionable_goal_count` / `unassigned_goal_count` / `unassigned_goal_ids`）が
前回配信時から変わったときだけ**通す。行そのものは削らない。

行を削らない理由: 実測で行動に繋がった 2 回はどちらもゴール粒度の項目由来だが、
task 粒度の数は「その 1 回が来たとき」の状況説明として読む価値がある。
**流す回数の問題であって、行の中身の問題ではない。**

## 置き場所

分類は `cmd/atct/watch_scope.go` に純関数として置き、`cmd/atct/watch.go` からは
2 か所（スナップショット配信ループと SSE 受信ループ）で呼ぶだけにする。

daemon 側（`internal/httpapi/server.go` の `eventPasses`）に置かない理由:
**`/api/inbox` のスナップショット経路は CLI にしか無い。**起動直後に未適用の決定を
そのまま流すこの経路は SSE を通らないので、daemon 側だけでは既定適用の 11 通を止められない。
分類の真実を 1 か所に保つため、両経路が通る CLI 側に置く。

## ゴール 185 との境界

185 は**どう張るか**（二重防止・`atct watch -project` の追加）、184 は**何を流すか**。

**どちらが先に着地しても壊れない。**184 は `-goal` が空かどうかだけを見る。
185 が `-project` を足しても `-goal` は空のままなので、`-project` 指定の watch は
自動的に project scope の分類を受ける。**185 が足す引数の中身が 184 の分類である。**
184 は新しいフラグを足さず、`runWatch` の引数も変えない。
`watch.go` への変更は呼び出し 2 行に閉じる。

## 完了条件との対応

1. task 単位の完了通知が commander に流れない -> 表の `handoff_reported`(`task_id` あり) / `handoff_yielded`
2. subcommander は今までどおり -> goal scope は恒等（全件通す）
3. 人間の承認と却下は誰の watch にも届く -> 表の `decision.*`
4. `decision_answered_unapplied` は出した本人に -> 決定 4（ゴール単位まで）
5. 63 -> 0、28 -> 28 -> 実 DB の写しに対する再計測検査
6. (1) を壊すと落ちる検査 -> `cmd/atct/watch_scope_test.go`
