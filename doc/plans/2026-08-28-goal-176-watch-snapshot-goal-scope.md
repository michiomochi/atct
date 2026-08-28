# 依頼: `atct watch -goal N` の起動時スナップショットを goal で絞る（ゴール 176 / タスク 752）

あなたは executor です。あなた自身の名前が `atct-176-executor` です。この依頼はあなた宛てです。
発信元: `atct-176-subcommander`（pane `w64:p1`）。

作業ディレクトリは `/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/176`
（ブランチ `wt/goal-176`）。ここから出るな。主チェックアウトを触るな。

## 0. まず設計をレビューせよ

下の「決定事項」を読み、実装上の制約で成り立たないと判断したら、**実装せずに
`atct-176-subcommander` へ差し戻せ**。成り立つならそのまま実装に進め。判断は依頼を出した側が行う。

## 1. 症状（調査は済んでいる。再調査は不要）

`atct watch -goal 127` を張った直後に、ゴール 92 の decision 341 の通知が届いた。poll すると
`decision belongs to another goal` で拒まれた。**届くのに読めない。**

原因は `cmd/atct/watch.go` の `watchLoopWithEnsureAndProjectIDAndGoal`。起動時スナップショット
（`/api/inbox` の `unapplied_decisions`）を回すループが、**プロジェクトでは絞っているのに
goal では絞っていない**。

- `filterProjectID` による skip: 実装あり（`decision.ProjectID != "" && != filterProjectID`）
- `scopeFilter.delivers("decision.answered", decision)`: これは**粒度フィルタであって goal
  フィルタではない**。`cmd/atct/watch_scope.go` の `delivers` は先頭で
  `if f.passThrough || f.goalID != "" { return true }` と書いてあり、**`-goal` 付きでは全部通す**
- SSE 側（`consumeWatchEventsWithStateAndGoal`）はサーバの `internal/httpapi/server.go`
  `eventMatchesGoalID` が正しく絞っている。**漏れているのは CLI の起動時スナップショットだけ**

## 2. 決定事項（変更不可）

1. **修正は CLI 側に置く。**`/api/inbox` に `goal_id` クエリを足すことはしない。
   理由: `/api/inbox` はダッシュボードの全ペイロード（goals / tasks / projects / open decisions）を
   返す共有エンドポイントで、CLI 1 呼び出し元のために共有面を変えると影響範囲が広がる。
   プロジェクト絞り込みが既に同じ CLI ループに居るので、2 つのスコープを 1 か所に揃える。

2. **`cmd/atct/watch_scope.go` に `deliversSnapshotDecision(decision watchDecision) bool` を
   新規追加する。**`delivers` 本体は変えない。
   理由: `delivers` は SSE 経路でも呼ばれる。そこには keepalive や `wakeup.evaluate_failed` の
   ように **goal を持たないまま正しく通るべきイベント**があり（サーバの `eventMatchesGoalID` が
   明示的に true を返している）、`delivers` に goal 一致を足すと `-goal` 付き watch で
   keepalive が消える。スナップショットは `decision.answered` しか emit しないので、
   専用の入口を作るのが正しい。

3. **一致規則はサーバの `eventMatchesGoalID` と同じにする。**すなわち
   `f.goalID != ""` のときは `decision.GoalID == f.goalID` だけを通し、**goal を持たない
   decision（`goal_id` 欠落や `0`）は落とす**。
   理由: サーバ側 SSE が `data.GoalID != 0 && data.GoalID == goalID` で同じ判定をしている。
   受信箱と権限を食い違わせないことがこのゴールの目的なので、2 経路の規則を揃える。

4. **比較は文字列の完全一致でよい。**ID は `internal/store/id.go` の `resolveID` が
   数値のみ受け付ける（UUID は migration 0020 で廃止済み）。CLI 側の `decodeEntityID` も
   `json.Number` の `String()` を返すので、両側とも正規化済みの 10 進文字列である。
   `strconv` での再解釈は不要。

5. **`-goal` 未指定（`goalID == ""`）のときは今までどおり全件通す。**commander の受信箱を
   壊してはならない。`newWatchPassThroughFilter()` も `goalID == ""` なので同じ経路で通る。

6. **プロジェクト絞り込みは直さない。**`cmd/atct/watch.go` のスナップショットループに実装済みで、
   `TestWatchFiltersOtherProjectFromSnapshot`（`cmd/atct/watch_test.go`）が固定している。
   セマンティクスもサーバの `eventPasses` と等価（project が空の decision は通す＝サーバは
   `project_id` クエリを付けないので同じ）。**この既存テストを走らせて出力を報告に貼るだけでよい。**

## 3. 実装項目

1. `cmd/atct/watch_scope.go` に `deliversSnapshotDecision` を追加する。中身は
   「`f.goalID != "" && decision.GoalID != f.goalID` なら false、それ以外は
   `f.delivers("decision.answered", decision)` に委ねる」。
   **なぜスナップショットだけ別入口なのかをコメントで残せ**（決定事項 2 の理由。/api/inbox は
   daemon 側で絞られない、という事実を書く）。

2. `cmd/atct/watch.go` の `watchLoopWithEnsureAndProjectIDAndGoal` 内、スナップショットの
   `for _, decision := range decisions` ループで、
   `scopeFilter.delivers("decision.answered", decision)` の呼び出しを
   `scopeFilter.deliversSnapshotDecision(decision)` に差し替える。**それ以外は変えない。**

3. `cmd/atct/watch_scope_test.go` にユニットテストを足す。既存の
   `TestWatchScopeProjectStops...` の書き方に合わせる。少なくとも次を分けて書く。
   - `-goal` 付きで、他ゴールの `decision.answered` が通らないこと
   - `-goal` 付きで、自ゴールの `decision.answered` が通ること
   - `-goal` 付きで、`goal_id` 空／`"0"` の decision が通らないこと
   - `-goal` 無し（`newWatchScopeFilter("")`）で、どのゴールの decision も通ること

4. `cmd/atct/watch_test.go` に結合テストを 2 本足す。既存の
   `TestWatchFiltersOtherProjectFromSnapshot`（638 行付近）が雛形。`cancelOnOutput`（749 行付近）と
   `watchWithURLsAndProjectAndGoal`（`cmd/atct/watch.go` 216 行）を使う。
   - `TestWatchFiltersOtherGoalFromSnapshot`: `/api/inbox` が 3 件返す
     （`goal_id` が他ゴール / 自ゴール / 欠落）状態で `-goal <自ゴール>` を渡し、
     **自ゴールの 1 件だけが出ることを完全一致で検査する**。
     `goal_id` は**実際のペイロードどおり数値 JSON で書け**（`"goal_id":92`）。
     `decodeEntityID` が数値を文字列にする経路ごと通すため。
   - `TestWatchKeepsEveryGoalInSnapshotWithoutGoalFlag`: 同じ inbox で `goalID=""` を渡し、
     **3 件すべてが出ること**を検査する（決定事項 5 の回帰）。

5. **項目 2 の差し替えを元に戻すと、項目 3 と 4 の新規テストが落ちること**を実際に確認せよ
   （手で戻して `go test` を走らせ、戻す）。落ちないならテストが効いていない。
   確認したら差し替えを戻し忘れるな。

## 4. 読む対象（ここだけ読め。他は読むな）

| ファイル | 箇所 | 行数 |
|---|---|---|
| `cmd/atct/watch_scope.go` | 全部 | 68 |
| `cmd/atct/watch.go` | 30-60（`watchDecision`）、200-330（ループと helper） | 873 のうち約 160 |
| `cmd/atct/watch_test.go` | 464-530（helper）、638-720（雛形）、749-780（`cancelOnOutput`） | 1086 のうち約 140 |
| `cmd/atct/watch_scope_test.go` | 1-60 | 217 のうち 60 |
| `internal/httpapi/server.go` | 1436-1452（`eventMatchesGoalID`）だけ | 17 |

`internal/store/`・`internal/domain/`・`web/`・他の `cmd/atct/*.go` は読む必要がない
（必要な事実は決定事項に書き写した）。

## 5. 触らないもの

- `internal/httpapi/server.go`（読むだけ。**1 文字も変えるな**）
- `internal/store/`、`internal/domain/`、`web/`、`skills/`、`doc/`
- `cmd/atct/watch.go` の項目 2 以外の箇所。とくに `delivers` の本体、
  `consumeWatchEventsWithStateAndGoal`、`watchEventsURLWithGoal`
- `TestWatchFiltersOtherProjectFromSnapshot` を含む既存テストの本文（走らせるだけ）
- **一覧に無いものを勝手に足すな・削るな。**指定外のファイルに触る必要が出たら、実装せずに
  `atct-176-executor` ではなく `atct-176-subcommander` に聞け。

**他ゴール（184 / 185）が別 worktree で同じ `cmd/atct/watch.go` を編集している。**
このディレクトリ（`.worktrees/176`）の外に出るな。`git rebase` / `git merge` / `git pull` を実行するな。

## 6. 検証（このコマンドをそのまま実行し、出力を報告に貼れ）

```sh
gofmt -l cmd/atct
go build ./...
go vet ./cmd/atct
go test ./cmd/atct -run 'TestWatchScope|TestWatchFiltersOtherGoalFromSnapshot|TestWatchKeepsEveryGoalInSnapshotWithoutGoalFlag|TestWatchFiltersOtherProjectFromSnapshot' -count=1 -v
go test ./cmd/atct -count=1
```

**新しくできるようになること**（4 本）

1. `-goal N` で他ゴールの decision がスナップショットに出ない
2. `-goal N` で自ゴールの decision はスナップショットに出る
3. `-goal N` で goal を持たない decision が出ない
4. 項目 2 の絞り込みを外すと 1〜3 が落ちる

**壊れてはいけないこと**（4 本。既存の利用者が今できていること）

1. `-goal` 無しで全ゴールの decision が出る（`TestWatchKeepsEveryGoalInSnapshotWithoutGoalFlag`）
2. `-goal` 無しの粒度フィルタが従来どおり（既存 `TestWatchScope*` 全部が緑）
3. プロジェクト絞り込みが従来どおり（`TestWatchFiltersOtherProjectFromSnapshot` が緑）
4. `-goal` 付きで keepalive と SSE 経路が黙らない（`TestWatchKeepsQuietWhileKeepalivesArriveAndReportsAfterTheyStop`
   と `TestWatchPassesGoalIDToEvents` が緑。`go test ./cmd/atct -count=1` に含まれる）

## 7. 報告

- 報告先は `herdr agent prompt atct-176-subcommander`。冒頭に発信元と用件を書く
- **30 行以内。**含める値: 変更したファイルと追加した関数名・テスト名、
  上記コマンドの実出力（`ok`/`FAIL` の行と、`-v` の新規テストの `--- PASS` 行）、
  項目 5（絞り込みを外すと落ちる）の確認結果として**落ちたテスト名と `FAIL` 行**
- 長い出力は一時ファイルに書いてパスを渡せ

## 8. 禁止

- **コミットするな**（`git commit` / `git add` / `git push`）。コミットは subcommander が行う
- `git add -A` / `git add .` を実行するな
- `git restore` / `git checkout -- ` / `git stash` / `rm` を実行するな（取り消せない）
- **ATCT の MCP ツールは、この依頼の冒頭で指示された 3 つ**
  （`atct_session_identify` / `atct_handoff_receive` / `atct_handoff_complete`）**だけを呼べ。**
  それ以外は呼ぶな。とくに `atct_task_update` を呼ぶな——**タスクを done にするのは
  subcommander である**（コミット SHA を一緒に記録するため）。`atct_handoff_complete` で止めろ
- **pane を作るな。`herdr pane split` / `herdr agent start` を実行するな。再委譲するな。**
  使ってよい herdr コマンドは `herdr agent prompt atct-176-subcommander` だけ
- サブエージェントを起動するな
- 権限昇格を独断でするな。失敗したら報告して返せ
