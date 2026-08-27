# 依頼 1: 却下されたら goal handoff を再発行する（実装と store 単体テスト）

あなたは executor です。あなた自身の名前が `atct-183-executor` です。この依頼はあなた宛てです。
報告先は `atct-183-subcommander`（`herdr agent prompt atct-183-subcommander`）。

## 0. まず設計をレビューせよ

`doc/specs/2026-08-28-reissuing-the-goal-handoff-on-rejection.md` を読め。
成り立つならそのまま実装に進め。実装上の制約で成り立たないと判断したら、**実装せずに
`atct-183-subcommander` へ差し戻せ。**判断は依頼を出した側が行う。

## 1. 決定事項（変更不可）

1. **却下の瞬間に、閉じた goal handoff を「却下前の受領者」へ受領済みの状態で再発行する。**
   commander の介入を無くすのが目的である。却下は人間の通常操作で、1 日 3 回起きている
2. **既存の行を書き換えない。**`completed_report_at` を NULL に戻さない。完了報告の記録が
   消えるため。**新しい行を足す**
3. 新しい行の ID は `<元の handoff ID>-reopen-<decision ID>`。却下 1 回につき 1 つに決まる
4. `requested_by` と `received_by` は**元の行から複写する。**新しい保持者を選ばない
5. **スキーマ変更をしない。新しい sqlc クエリを書かない。`sqlc generate` を走らせない。**
   既存の `RequestGoalHandoff` / `ReceiveGoalHandoff` / `ListGoalHandoffs` / `GetDecision`
   で足りることを確認済みである
6. **`RejectCompletion` を 1 つのトランザクションにする。**Decision の応答だけが成立して
   再発行が落ちる状態を作らない。**`RejectGoal`（`internal/store/goal.go:580` 付近）が
   既に同じ形をしている**ので、それに合わせる

## 2. 実装項目

`internal/store/goal.go` の `RejectCompletion`（現在 514-522 行）を次の形に書き換える。

1. `s.db.BeginTx` を開き、`defer tx.Rollback()` を置く
2. `sqlcgen.New(tx)` で `AnswerDecision`（`answer_label="reject"`, `answer_text=reason`,
   `answered_at=now`）を実行する。`RowsAffected() == 0` なら
   `fmt.Errorf("%w: %d", ErrDecisionNotOpen, in.DecisionID)` を返す。
   **現在の `answerDecision`（`internal/store/decision.go:270`）と同じ判定を保つ**
3. 同じ tx で `GetDecision` を引き、`goal_id` / `kind` / `agent_session_id` を得る
4. **`kind != "completion"` なら再発行しない**（そのまま commit へ進む）
5. 同じ tx で `ListGoalHandoffs(goal_id)` を引き、次を判定する
   - `completed_report_at IS NULL` の行が 1 つでもあれば**再発行しない。**
     既に別の誰かが保持者である。奪わない
   - `received_at != NULL && completed_report_at != NULL && received_by == agent_session_id`
     の行のうち `completed_report_at` が最も新しいものを選ぶ。**無ければ再発行しない**
6. 選んだ行から新しい行を作る。`RequestGoalHandoff`（ID 指定の INSERT）と
   `ReceiveGoalHandoff` を **`WithTx(tx)` で** 呼ぶ
   - `id`: `<元の handoff ID>-reopen-<decision ID>`
   - `goal_id`: そのまま
   - `requested_by` / `received_by`: 元の行から複写
   - `requested_at` / `received_at`: いま
   - `request_report`: `完了報告が却下されたため handoff を再発行した: <reason>`
     （`reason` が空なら理由部分を落とす）
7. `tx.Commit()` の**後で**、現在 `answerDecision` が出しているイベント 3 つを同じ順で出す。
   `s.notify.publish(decisionID)` / `s.notify.publishAll()` /
   `s.notify.publishEvent(Event{Name: "decision.rejected", Data: d})`。
   `d` は commit 後の `s.GetDecision` で取る
8. **`answerDecision` の既存の呼び出し元（`AnswerDecision`）の挙動を変えるな。**
   `RejectCompletion` だけを分岐させる

## 3. テスト（`internal/store/goal_handoff_test.go` に足す）

既存の書き方に合わせる（`internal/store/goal_handoff_test.go` の 1-130 行にヘルパーがある）。

**新しくできるようになること（肯定・1 件）**

1. subcommander が handoff を受領 → `CompleteGoalWithReport` → `CompleteGoalHandoffForGoal`
   → `RejectCompletion` の後、**そのゴールに開いた handoff が 1 つあり、その `received_by`
   が元の受領者と一致する**こと。ID が `<元>-reopen-<decisionID>` であることも確認する

**壊れてはいけないこと（否定・3 件）**

2. **既に開いた handoff があるとき、新しい行が増えない。**却下前に別の handoff が
   開いている状態を作り、`RejectCompletion` 後の `ListGoalHandoffs` の件数が変わらないこと
3. **報告者が handoff を持たないとき、handoff が開かない。**handoff 無しで
   `CompleteGoalWithReport` を出して却下し、開いた handoff が 0 件であること
4. **承認では handoff が開かない。**同じ前提から `ApproveCompletion` を通し、
   開いた handoff が 0 件であること

**さらに、既存の `TestGoalHandoffCompleteByGoal` と
`TestGoalHandoffAllowsSecondHandoffForSameGoal` が通り続けること。**

## 4. 読む対象

これ以外を読む必要が出たら、続行せず `atct-183-subcommander` へ差し戻せ。

- `doc/specs/2026-08-28-reissuing-the-goal-handoff-on-rejection.md`（全部）
- `internal/store/goal.go` の 380-470 行（`CompleteGoalWithReport` と完了周り）、
  470-520 行（`ApproveCompletion` / `RejectCompletion`）、575-640 行（`RejectGoal` = 手本）
- `internal/store/goal_handoff.go`（全部・438 行）
- `internal/store/decision.go` の 255-300 行（`answerDecision`）
- `internal/store/queries/task.sql` の 200-245 行、
  `internal/store/queries/decision.sql` の 1-20 行
- `schema.sql` の 79-100 行（`decisions`）と 128-150 行（`goal_handoffs`）
- `internal/store/goal_handoff_test.go`（テストの書き方とヘルパー）

## 5. 触らないもの

- `internal/store/migrations/` — **スキーマは変えない**
- `internal/store/sqlcgen/` — **生成物は手で直さない。`sqlc generate` も走らせない**
- `internal/e2e/`・`internal/daemon/`・`skills/` — **次の依頼で扱う。今回は触るな**
- `web/` 全部
- 一覧に無いものを勝手に足すな・削るな。指定外のファイルに触る必要が出たら、
  実装せずに `atct-183-subcommander` へ聞け

## 6. 検証

そのまま実行し、**出力を報告に含めろ。**

```sh
go build ./...
go test ./internal/store/ -run 'GoalHandoff|RejectCompletion|Completion' -v 2>&1 | tail -60
go test ./internal/store/ 2>&1 | tail -20
go vet ./internal/store/
```

**あってはいけないものの確認**（結果を報告に貼る）。

```sh
git status --porcelain -uall            # migrations/ と sqlcgen/ が出ないこと
git diff --stat                          # 変更が internal/store/goal.go と *_test.go だけであること
```

## 7. 禁止

- **コミットするな。**`git add` も `git commit` も `git push` もするな
- **ATCT のツールを呼ぶな**（handoff の枠を壊す）
- **pane を作るな。再委譲するな。サブエージェントを起こすな**
- `git checkout` / `git restore` / `git stash` / ファイル削除をするな
- 権限昇格を独断でするな。失敗したら報告して返せ

## 8. 報告

`herdr agent prompt atct-183-subcommander` で `atct-183-subcommander` へ送れ。
**冒頭に発信元と用件を書く。40 行以内。**含める値:

- 変更したファイルと、追加・変更した関数名
- 上の 6 のコマンドの実出力（テストは末尾の `ok` / `FAIL` 行と、失敗があればその内容）
- 設計どおりに実装できなかった箇所があれば、その理由
