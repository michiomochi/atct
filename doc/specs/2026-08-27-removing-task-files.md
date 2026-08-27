# tasks.files による直列化をやめる

日付: 2026-08-27
ゴール: goal 139「各ゴール毎に worktree を作成することにしたので files 行で各作業が
コンフリクトしないように制御する必要はなくなったのでこの機構を削除して」

## 何が機構か

`tasks.files` は「そのタスクが触るファイル」を宣言で固定する列で、用途は 1 つしかない。
**claim の時点で、既に他セッションが保持しているタスクと重なったら拒む**である。

    internal/store/task.go:601  ClaimTask が files を読む
    internal/store/task.go:633  rejectTaskFileConflict が重なりを見て拒む
    internal/store/task.go:679  taskFileConflictCandidates が代替タスクを並べる
    internal/store/task.go:728  firstOverlappingFile
    internal/store/task.go:19   ErrTaskFileConflict / :32 TaskConflictCandidate / :39 TaskFileConflictError

`files` を読む場所は他に無い。web は文字列として並べるだけで、判断に使っていない。

**「消したら何が鳴らなくなるか」も測った。**`internal/store/wakeup.go` の
`WakeupState` は `ListTasks` でタスクを読むが、参照するのは handoff の
`RequestedAt` / `ReceivedAt` / `CompletedReportAt` だけで、**`files` を読まない**
（`grep -n files internal/store/wakeup.go` は 0 件）。**通知の経路に影響しない。**

## なぜ不要になったか

**同じチェックアウトを全員が共有していたから必要だった。**2026-08-20 の
`doc/specs/2026-08-20-worktree-per-goal.md` が測ったとおり、当時は executor 全員が
`main` の 1 ツリーで作業し、干渉は commander の依頼書の文章で防いでいた。

いまはゴールごとに worktree が分かれる。2026-08-27 時点で 8 ゴール（92 / 91 / 107 /
127 / 134 / 136 / 139 / 141）が専用 worktree で並行している。**別ツリーの編集は
そもそも同じファイルを指さない。**重なりが起きるのは同一ゴール内の executor 同士だが、
そこは 1 タスク 1 executor を直列に積む運用で押さえている。

**この検査は元から効いていなかった、という測定もある。**
`doc/specs/2026-08-22-three-tier-orchestration.md` の「ファイル衝突の検査は 1 セッション
運用では効かない」に、同じ `GoalDetail.tsx` を持つ 2 タスクが両方 claim できた実測がある。
止めたのは人間で、atct は止めなかった。

## 決めたこと

### 1. 列ごと消す。記述的メタデータとして残さない

`decision 410`。代案は「`rejectTaskFileConflict` の呼び出しだけ外し、列・入力・表示は残す」。
**採らない。**読む主体が消えたあとの `files` は、宣言の手間だけが残る死んだフィールドになる。
`doc/specs/2026-08-26-editing-task-files.md` が測ったとおり、この値は宣言時点で決まって
実態からずれ続ける。**ずれを直す仕組みを維持する理由は、読む主体があるときにしか無い。**

再導入するときは移行をもう 1 本足す。過去に宣言された値は失われる。

### 2. 列は移行 0021 で落とす。歴史的スキーマ定義は触らない

`0016_drop_claim_columns.sql` と `0020_drop_legacy_ids.sql` に先例がある。
`internal/store/migrations.go` の `requiredV6Columns` / `requiredCurrentV6Columns` は
**過去のスキーマがどうだったかの記述**なので、`files` を残したままにする。ここを直すと
既存 DB の移行判定が壊れる。

`schema.sql` は sqlc の入力なので移行と同時に直す。ずれると sqlc が実在しない
スキーマに対して生成し、誰も気づかない。

**訂正（実装で判明）。**「`migrations.go` を触らない」は誤りだった。`validateV6Schema`
は「その DB が**いま**持っているべき列」を検査しており、0021 を適用した DB では
`files` が無いのに要求して落ちる（`e2e` / `migration_integrity` / `migrations_test` の 4 本）。

    table "tasks" is missing v6 columns: files

正しい形は 0016 と同じで、**適用済み移行に応じて期待値を落とす変換**を足すこと。

    if _, ok := state.applied["0021_drop_task_files.sql"]; ok {
            requiredColumns = withoutTaskFilesColumn(requiredColumns)
    }

`requiredV6Columns` / `requiredCurrentV6Columns` の `files` は**残したまま**である。
あれは 0021 より前の DB の姿で、変換で落とすのが正しい。歴史的スキーマの記述を
書き換えるのと、現在の期待値を導出するのは別の操作である。

### 3. sqlc は再生成しない（この作業ツリー限定の迂回）

`internal/store/sqlcgen/` はコミット済みの内容と `sqlc generate` の出力が一致していない
（別単位が発見した既知の不整合）。実際に再生成したところ、`files` と無関係な
`decision.sql.go` / `goal.sql.go` / `project.sql.go` から **589 行が消えた**
（`LegacyID`・`RequestTaskHandoff`・`ListOpenTaskHandoffsForGoal`）。

**したがって `models.go` と `task.sql.go` を手で直した。**`UpdateTaskContent` は
番号付きプレースホルダなので、`files = COALESCE(?3, files)` を消したあと `?4` → `?3`、
`?5` → `?4` の詰めが要る。**詰め忘れは実行時にしか出ない。**
不整合そのものの解消はこのゴールの範囲外である。

## 削除範囲

| 層 | 消すもの |
|---|---|
| DB | `internal/store/migrations/0021_drop_task_files.sql` を足して `tasks.files` を落とす。`schema.sql` の列 |
| クエリ | `internal/store/queries/task.sql` の `files` 列と `UpdateTaskContent` の `files`、`ListTaskAlternatives` そのもの。`go tool sqlc generate` |
| store | `ErrTaskFileConflict` / `TaskConflictCandidate` / `TaskFileConflictError` / `rejectTaskFileConflict` / `taskFileConflictCandidates` / `firstOverlappingFile` / `marshalTaskFiles` / `unmarshalTaskFiles` / `maxTaskFileConflictCandidates`、`DeclareTasks` と `UpdateTaskContent` の `files` 引数 |
| domain | `internal/domain/model.go` の `Task.Files` |
| daemon | `internal/daemon/handler.go` の `task.declare` / `task.update_content` の `files` |
| mcpshim | `internal/mcpshim/tools.go` の `TaskDeclareIn.Files` / `TaskUpdateContentIn.Files` |
| web | `web/src/lib/api.ts` の `Task.files`、`TaskDetailPage.tsx` の表示、`i18n/ja.ts` と `en.ts` の `task.detail.files` |
| skill | `skills/atct/SKILL.md` の「`files` を直す」記述 |

**残すもの。**`FilesChanged` と `task.detail.commitFiles` はコミット差分の統計で、
名前が似ているだけの別物である。`doc/plans/` の過去の計画は履歴なので直さない。

## 失効する既存 spec

- `doc/specs/2026-08-26-editing-task-files.md`（goal 134）。`files` を直す門番の設計が
  まるごと失効する。**goal 134 の完了条件 4 は `files like '%<path>%'` の SQL が
  当たることを要求しており、この削除と正面衝突する。**どちらを通すかは `decision 409`
- `doc/specs/2026-08-22-three-tier-orchestration.md` の「ファイル衝突の検査」節。
  当時の測定として残し、削除したことをこの文書が引き継ぐ

## 検査

- **あるべきものが無いことを見る。**`grep -rn 'files' --include='*.go'` で
  `FilesChanged` 以外の task 由来の `files` が非テストコードに残らないこと
- **既存 DB が移行できること。**`files` を持つ DB を作って 0021 を通し、
  タスクの読み書きが続くことを見る。列を落とす移行は `PRAGMA foreign_keys` と
  インデックスを踏むので、構造体の中身だけでは足りない
- `go build ./... && go vet ./... && go test ./... -race`、web は `pnpm test` と `pnpm build`

## 範囲外

- goal 134 の門番の実装（この削除で不要になる）
- `request_report` / `complete_report` の扱い

## main とのマージで決めたこと（2026-08-27）

ゴール 134 が先に着地し（`f605db4`）、同じ `UpdateTaskContent` に**保持者の門番**と
`agentSessionID` 引数を足した。**両者は対立しない。門番を残し、`files` だけ落とす。**

    func (s *Store) UpdateTaskContent(ctx, taskID, title, description *string, agentSessionID int64)

`authorizeTaskContentUpdate` は 134 のまま残す。**「タスクの内容を誰が直せるか」は
`files` の直列化とは独立である。**

門番の検査は `files` を書き換えて通す形だったので、**`description` を書き換える形に移した。**
検査の意図（保持者 2 経路を通し、他 space の subcommander とプロジェクト所属だけの
セッションを拒む）は変えていない。

    TestUpdateTaskContentHandoffAuthorization           files -> description
    TestTaskUpdateContentHandoffAuthorizationRPC        files -> description
    TestUpdateTaskContentRejectsDoneAndDroppedFilesOnly -> ...DescriptionOnly に改名

**`internal/mcpshim/tools.go` の説明文は自動マージで衝突しない。**134 が
`Rewrite a task's content, including the files it touches.` と書いており、
**放置すると嘘の説明文が残る。**`Rewrite a task's title or description.` に直した。
保持者を述べる 1 文は残す。
