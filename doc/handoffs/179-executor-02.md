# 依頼: goal.withdrawn を goal 指定の watch とダッシュボードに通す（ゴール 179 / タスク 806）

あなたは executor です。あなた自身の名前が `atct-179-executor` です。この依頼はあなた宛てです。
報告先は `atct-179-subcommander` です。**タスク 805 の続きの作業である。**

## 冒頭で必ず行うこと（ATCT）

First call `atct_session_identify` with a stable session key that remains unchanged for this
session and identifies only you. Your agent name is suitable. Do this before any other atct call.

Then record receipt of the handoff by calling `atct_handoff_receive` with only the `task_id`
provided in this request (**806**). Do this before starting work. Do not pass a handoff ID or
session; ATCT supplies them.

Then invoke the `atct_role` MCP tool with `expected_role` set to `executor`. If it reports
`matches: false`, do not start work; return the task.

When the work is complete, record completion by calling `atct_handoff_complete` with the
`task_id` provided in this request and a `complete_report`. The `complete_report` must say what
was done, what was verified, what could not be verified, and paths changed.

Only then close the task, by calling `atct_task_update` with the `task_id` provided in this
request and `status` set to `done`. This order is required: a terminal status closes any open
task handoff and replaces its `complete_report`, so the report has to be recorded first.

**呼んでよい atct ツールは 5 つ**（805 と同じ）: `atct_session_identify`,
`atct_handoff_receive`, `atct_role`, `atct_handoff_complete`, `atct_task_update`。
**呼んではならない 13 個**: `atct_goal_handoff_complete`, `atct_goal_handoff_receive`,
`atct_goal_handoff_request`, `atct_goal_claim`, `atct_goal_release`, `atct_goal_complete`,
`atct_goal_update_content`, `atct_project_claim`, `atct_project_release`, `atct_task_claim`,
`atct_handoff_request`, `atct_task_declare`, `atct_decision_ask`。

## 0. まず設計をレビューせよ

成り立つならそのまま実装に進む。成り立たないと判断したら、実装せずに
`atct-179-subcommander` へ差し戻すこと。**あなたが代わりに設計を決めてはならない。**

## 1. 決定事項（変更不可）

1. **`eventMatchesGoalID` に `store.GoalWithdrawnEvent` の case を足す。**
   値型とポインタ型の両方を書く（既存の `DetectionEvent` と同じ形）。
   判定は `data.GoalID != 0 && data.GoalID == goalID`。
2. **`eventProjectID` にも同じ 2 つの case を足す。**足さないと `default:` が 0 を返し、
   `eventPasses` の `eventProjectID != 0` 条件が偽になって**全プロジェクトの
   `-project` watch に通ってしまう**。`data.ProjectID` を返す。
3. **`web/src/lib/ui.ts` の `DECISION_EVENT_NAMES` に `"goal.withdrawn"` を足す。**
   位置は `"goal.created"` の**直後**。ここに無い名前はダッシュボードが黙って捨てるので、
   取り下げても画面が更新されない。
4. **`cmd/atct/watch.go` には触らない。**ゴール 192 が `formatWatchDecision` を
   書き換え中である。CLI の行が出ないことは既知の欠落として
   `doc/specs/2026-08-28-goal-withdrawal-event.md` の「残る欠落」に記録済みである。
   **勝手に埋めない。**

## 2. 実装項目

### 2-1. `internal/httpapi/server.go`

(a) `eventMatchesGoalID`（1436〜1453 行）に `store.GoalWithdrawnEvent` /
`*store.GoalWithdrawnEvent` の case を足す。`DetectionEvent` の case の**直後**に置く。

(b) `eventProjectID`（1455〜1495 行）に同じ 2 つの case を足す。

### 2-2. `internal/httpapi/server_test.go` に検査を足す

**新しいテスト 1 本**: `TestSSEGoalScopedStreamDeliversGoalWithdrawn`。表駆動。
ケースは 4 つで、**開いた決定 0 件が 2 件、1 件以上が 2 件**である。

| ケース名 | 開いた決定 |
|---|---|
| `no decisions` | 0 件 |
| `no decisions with open task` | 0 件 |
| `one decision` | 1 件 |
| `two decisions with open task` | 2 件 |

**表の均衡をテスト自身が検算する。**表を回す前に 0 件のケース数と 1 件以上のケース数を
数え、一致しなければ `t.Fatalf` で落とす（両方の数をメッセージに出す）。
**コメントで済ませない。**

各ケースの手順。

1. `f := newBareFixture(t)` — `f.goal` は creator が `human` なので `active` である
2. `srv := newTestServer(t, f.store)` / `defer srv.Close()`
3. 決定を張るケースは `f.store.AskDecision` を**ストリームを開く前**に呼ぶ
   （`decision.created` を拾わないため）
4. `eventsURLWithGoal(srv.URL, idText(f.goal.ID))` で SSE を開く
   （ヘルパは `server_test.go:2623` にある）
5. `f.store.WithdrawActiveGoal(f.ctx, f.goal.ID, "<理由>")`
6. `readSSEFrame`（2517 行）を**繰り返し呼び、`frame.event` が
   `store.EventGoalWithdrawn` の frame を待つ**。store 側は `goal.withdrawn` を
   最初に publish するが、`decision.withdrawn` が続くので**順序に依存しない形**にする。
   最大 10 frame まで読んで見つからなければ `t.Fatalf`
7. `frame.data` を `store.GoalWithdrawnEvent` に `json.Unmarshal` し、
   `GoalID` / `ProjectID` / `Reason` を検査する

**新しいテスト 2 本目**: `TestSSEGoalScopedStreamFiltersOtherGoalsWithdrawal`
（あってはいけないものが無いかの検査）。プロジェクト内に別のゴールを 2 つ作り、
片方の goal_id で SSE を開き、**もう片方**を取り下げる。その後 keepalive を
`f.store.PublishEvent` で流し、**最初に届く frame が keepalive であること**
（= 他ゴールの `goal.withdrawn` が漏れていないこと）を見る。
既存の `TestSSEGoalIDPublishesKeepaliveButNotWakeup`（3049 行付近）が同じ形である。

### 2-3. `web/src/lib/ui.ts` と `web/src/lib/ui.test.ts`

(a) `ui.ts` の `DECISION_EVENT_NAMES`（6〜27 行）の `"goal.created"` の直後に
`"goal.withdrawn"` を足す。**なぜ足すかのコメントを 1 行付ける**
（既存の `handoff_reported` の上にあるコメントと同じ調子で日本語で書く）。

(b) `ui.test.ts` の `describe("decision SSE events")` 内の
`expect(DECISION_EVENT_NAMES).toEqual([...])`（377〜399 行）の一覧に同じ位置で足す。

`web/src/lib/api.test.ts` は網羅的な一覧ではなく例示なので**変更しない。**

## 3. 読む対象（具体名・これ以外は読まない）

| ファイル | 範囲 | 行数 |
|---|---|---|
| `doc/specs/2026-08-28-goal-withdrawal-event.md` | 全部 | 124 |
| `internal/httpapi/server.go` | 1355〜1500 | 146 |
| `internal/httpapi/server_test.go` | 396〜413, 723〜760, 2505〜2630, 3040〜3095 | 237 |
| `internal/store/wakeup.go` | 1〜95 | 95 |
| `web/src/lib/ui.ts` | 1〜40 | 40 |
| `web/src/lib/ui.test.ts` | 375〜400 | 26 |

合計 668 行。**この一覧に無いファイルを読む必要が出たら、実装せずに差し戻すこと。**

## 4. 触らないもの

- **`cmd/atct/watch.go` / `cmd/atct/watch_scope.go`** — 192 が編集中
- **`internal/store/`** — タスク 805 で終わっている。ここでは変更しない
- **`web/src/lib/api.test.ts`**、**`web/src/lib/api.ts`**
- **`skills/atct/SKILL.md`**、**`script/release.sh`**
- **`internal/httpapi/ws.go`** — 名前のフィルタを持たないので変更不要
- 一覧に無いことを勝手に足さない・削らない。指定外のファイルに触る必要が出たら、
  実装せずに `atct-179-subcommander` に聞くこと。**他の誰かがやってくれると仮定するのも誤りである。**

## 5. 検証（このコマンドをそのまま実行し、実出力を報告に含める）

**`go test ./...` は実行しないこと。**このマシンは swap が枯渇する。

新しくできるようになること（4 つ）。

    go build ./internal/httpapi/
    go test ./internal/httpapi/ -run 'TestSSEGoalScopedStreamDeliversGoalWithdrawn' -count=1 -timeout 180s -v
    go test ./internal/httpapi/ -run 'TestSSEGoalScopedStreamFiltersOtherGoalsWithdrawal' -count=1 -timeout 180s -v
    cd web && npm test -- --run src/lib/ui.test.ts

壊れてはいけないこと（4 つ）。

    go test ./internal/httpapi/ -run 'TestSSE' -count=1 -timeout 180s
    go test ./internal/httpapi/ -count=1 -timeout 300s
    go vet ./internal/httpapi/
    cd web && npx tsc --noEmit

**あってはいけないものが無いかの検査**（結果を報告に含める）。

    # cmd/atct と internal/store に差分が無いこと（805 のコミット後の差分だけが出る想定）
    git status --porcelain -uall -- cmd/atct internal/store
    # eventProjectID に case を足し忘れていないこと（2 件出るべき）
    grep -c 'GoalWithdrawnEvent' internal/httpapi/server.go
    # web の一覧に入っていること
    grep -n 'goal.withdrawn' web/src/lib/ui.ts web/src/lib/ui.test.ts

## 6. 禁止事項

- **コミットしない。**`git add` も `git commit` も行わない
- `git checkout` / `git restore` / `git stash` / `git add -A` / `git add .` を使わない
- pane を作らない。サブエージェントを起動しない。再委譲しない
- 権限昇格を独断で行わない（失敗したら報告して返す）
- `herdr` は `herdr agent prompt atct-179-subcommander` だけ使ってよい

## 7. 報告

`atct_handoff_complete`（`task_id` = 806）の `complete_report` に書く。**80 行以内。**

1. `git diff --stat` の実出力
2. 5 章の 8 コマンド + 3 検査の**実出力**
3. 表の均衡検算のコード（10 行以内）
4. 検証できなかったことがあれば明記する

報告の後に `atct_task_update`（`task_id` = 806, `status` = `done`）を呼ぶ。**この順序を守る。**
