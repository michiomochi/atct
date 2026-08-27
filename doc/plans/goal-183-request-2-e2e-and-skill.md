# 依頼 2: 通しの検査・役割復帰の検査・SKILL.md の修正

あなたは executor です。あなた自身の名前が `atct-183-executor` です。この依頼はあなた宛てです。
報告先は `atct-183-subcommander`（`herdr agent prompt atct-183-subcommander`）。

**依頼 1（`doc/plans/goal-183-request-1-implementation.md`）の続きである。**
実装はもう入っている前提で、その外側を検査する。

## 0. まず設計をレビューせよ

依頼 1 で読んだ `doc/specs/2026-08-28-reissuing-the-goal-handoff-on-rejection.md` の
「完了条件との対応」の表が、これから書く検査の根拠である。成り立たないと判断したら、
実装せずに `atct-183-subcommander` へ差し戻せ。

## 1. 決定事項（変更不可）

1. **役割復帰は `internal/daemon` の層で検査する。**store の行を見るだけでは
   「commander を経由せず再開できる」を示せない。`deriveSessionRole` が
   `subcommander` を返すところまで見る
2. **通しは `internal/e2e` に置く。**却下 → 再開 → 再提出 → 承認 を 1 本のテストで通す
3. **否定側を肯定側と同じ数だけ書く。**「あるべきものがあるか」だけでは実データで壊れる

## 2. 実装項目

### 2-1. `internal/daemon` に役割復帰の検査を足す

`internal/daemon/goal_handoff_test.go` に足す（既存のテストの書き方に合わせる）。

1. **肯定**: goal handoff を受領したセッションが完了報告を出し、`goal handoff` を閉じ、
   人間が `RejectCompletion` した直後に `deriveSessionRole` がそのセッションへ
   `subcommander` と当該 `goal_id` を返すこと。
   **これが仕様書の完了条件 5「(1) を壊すと落ちる検査」である**
2. **否定**: 同じ前提で `ApproveCompletion` を通したときは `executor` のままであること

### 2-2. `internal/e2e` に通しを足す

`internal/e2e/full_flow_test.go` に足す（`TestFullFlowThroughDaemonAndHTTP` が手本）。

3. **通し**: ゴール作成 → goal handoff の request/receive → 完了報告
   → `atct_goal_handoff_complete` 相当 → **却下** → 役割が `subcommander` に戻る
   → 再度の完了報告 → **承認** → ゴールが `done` になる。
   **途中で commander 役のセッションが handoff を再発行しないこと**が要点である

### 2-3. `skills/atct/SKILL.md` を直す

`## Recover when your role comes back wrong`（394 行付近）の中の

> A subcommander cannot restore its own goal; ask the commander to issue the goal
> handoff again because reissuing it requires a project claim. This is a procedure,
> not a repair; it becomes unnecessary once the issue is fixed.

を次の趣旨に書き換える。**英語のまま・同じ調子で書く。**

**役割が落ちる引き金は 3 つあり、復旧手段が違う。それを分けて書くのが目的である。**
2026-08-27 から 08-28 にかけて 8 件の実測がある。

1. **handoff は開いたまま、セッション記録だけが作り直された**（daemon の再起動。
   **版が上がったかどうかとは無関係**に起きる） -> `atct_session_identify` を同じ
   `session_key` で呼び直せば戻る。**これが 1 手目で、既にこの節に書いてある**
2. **完了報告が却下されて handoff が閉じたまま** -> **自動で元の受領者へ再発行される。**
   `atct_role` を呼び直せば `subcommander` に戻っている。**commander に頼む必要は無い**
3. **それ以外で handoff が閉じた**（`atct_goal_handoff_complete` を完了報告より先に
   呼んだ、executor が誤って呼んだ） -> 再発行に project claim が要るので commander に頼む

**3 つとも「`atct_role` を呼ぶまで気づけない」点は共通である。**役割が要る操作の前に
`atct_role` を呼べ、と一言添える。

- 参照先に `doc/specs/2026-08-28-reissuing-the-goal-handoff-on-rejection.md` を足す
  （既存の `doc/specs/2026-08-25-session-id-swap.md` の行は残す）

**同じ節の 405-411 行の箇条書き（project / goal / task の復旧手順）は残す。**

## 3. 読む対象

これ以外を読む必要が出たら、続行せず `atct-183-subcommander` へ差し戻せ。

- `internal/daemon/goal_handoff_test.go`（全部・304 行）
- `internal/daemon/handler.go` の `deriveSessionRole`（156-190 行付近）
- `internal/daemon/goal_complete_guard_test.go`（全部。完了報告まわりのテストの組み方）
- `internal/e2e/full_flow_test.go` の 1-120 行（ヘルパー）と 351-505 行
  （`TestFullFlowThroughDaemonAndHTTP`）
- `skills/atct/SKILL.md` の 390-415 行
- 依頼 1 で自分が書いた `internal/store/goal.go` の `RejectCompletion` 周辺

## 4. 触らないもの

- `internal/store/goal.go` — **依頼 1 で完成している。テストを通すために書き換えたく
  なったら、書き換えずに `atct-183-subcommander` へ差し戻せ**
- `internal/store/migrations/`・`internal/store/sqlcgen/`・`web/`
- `skills/atct/SKILL.md` の指定した節以外
- 一覧に無いものを勝手に足すな・削るな

## 5. 検証

そのまま実行し、**出力を報告に含めろ。**

```sh
go build ./...
go test ./internal/daemon/ -run 'Role|GoalHandoff|Completion' -v 2>&1 | tail -60
go test ./internal/e2e/ -v 2>&1 | tail -60
go test ./... 2>&1 | tail -30
go vet ./...
```

**あってはいけないものの確認**（結果を報告に貼る）。

1. **再発行を外すと 2-1 の肯定テストが落ちること**を実際に見せる。
   `internal/store/goal.go` の再発行部分を一時的に無効化して
   `go test ./internal/daemon/ -run Role` を走らせ、**FAIL の出力を貼ってから元に戻す。**
   元に戻したあと `git diff --stat` を貼り、`internal/store/goal.go` に差分が
   残っていないことを示す
2. `git status --porcelain -uall` — `sqlcgen/` と `migrations/` が出ないこと

## 6. 禁止

- **コミットするな。**`git add` も `git commit` も `git push` もするな
- **ATCT のツールを呼ぶな**
- **pane を作るな。再委譲するな。サブエージェントを起こすな**
- `git checkout` / `git restore` / `git stash` / ファイル削除をするな

## 7. 報告

`herdr agent prompt atct-183-subcommander` へ。**冒頭に発信元と用件。40 行以内。**含める値:

- 追加したテスト関数名（ファイルごと）
- 5 のコマンドの実出力（末尾の `ok` / `FAIL` 行）
- 検証 1 の FAIL 出力と、元に戻した後の `git diff --stat`
- `skills/atct/SKILL.md` の書き換えた段落の全文
