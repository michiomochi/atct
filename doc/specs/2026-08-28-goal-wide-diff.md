# ゴール全体の差分をダッシュボードで見せる

ゴール 187。承認判断のとき、そのゴールが**結局どのファイルをどう変えたか**が 1 か所で読めない。
いまあるのはタスクごとのコミット一覧だけで、**足し合わせても実態にならない。**

## 何を作るか

    GET /api/goals/{id}/diff            -> ファイル一覧（numstat）
    GET /api/goals/{id}/diff?path=<p>   -> そのファイル 1 つ分の生 diff

ゴール詳細に「このゴールの差分」節を足す。**既定で一覧だけを開き、ファイルを選んだときに
そのファイルの hunk を取りに行く。**hunk の描画は `@git-diff-view/react` に任せる。

## 決めたことと理由

### 1. 3 点ドット `<base>...wt/goal-<N>` を使う

2 点（`<base>..`）だと、ブランチが `main` を取り込んだ後や、`main` がブランチより進んだ後に
**main 側の変更が差分へ混ざる。**

実測（2026-08-28・主チェックアウトの全 23 本の `wt/goal-*`）:

    for b in $(git for-each-ref --format='%(refname:short)' refs/heads | grep '^wt/goal-'); do
      echo "$b 3dot=$(git diff --numstat main...$b | wc -l) 2dot=$(git diff --numstat main..$b | wc -l)"
    done

    wt/goal-181  3dot=0   2dot=26
    wt/goal-141  3dot=0   2dot=73
    wt/goal-92   3dot=0   2dot=92
    wt/goal-fe404d9c 3dot=5 2dot=175

**23 本すべてで 2 点のほうが多い。**多い分は全部 main 側であって、そのゴールの成果ではない。
`wt/goal-181` は自分の変更が 0 件なのに、2 点だと 26 ファイル・+739 −1027 と出る。
**承認者はこれを 181 の成果だと読む。**

### 2. 既定ブランチを焼き付けない。`refs/remotes/origin/HEAD` から引く

登録済み 5 プロジェクト（atct / stock-data / HQ / stock-ai / dotfiles）を実測した。
**5 件とも `refs/remotes/origin/HEAD` は `origin/main` を指していた。**
一方 **HQ はローカルの `HEAD` が `refs/heads/feature/hq-skill-dual-harness`** だった。
**`git symbolic-ref HEAD` を基準にすると HQ で壊れる。**

解決の順は次で固定する。

1. `git symbolic-ref --short refs/remotes/origin/HEAD` の結果からリモート名を落として名前を得る
2. 取れなければ `main`・`master` の順で `refs/heads/<名前>` の存在を見る
3. 得た名前は**ローカル `refs/heads/<名前>` を優先**し、無ければ `refs/remotes/origin/<名前>` を使う。
   worktree のブランチはローカルの既定ブランチから生えるので、ローカルが基準である
4. どれも解決できなければ差分の節を出さない（`reason: "no_base"`）

### 3. git の実行に期限を付ける

ゴール 180 は、期限無しで git を fork して差し戻しになった。ここでも同じ形を避ける。
**`context.WithTimeout` で 5 秒。**前例は `internal/daemon/wakeup.go` の
`handoffWorktreeActivity`（3 秒）。一覧側は `--numstat` だけで `-p` を取らないので軽い。

**期限切れは 500 にしない。**`200` で `available: false` / `reason: "timeout"` を返し、
画面は「不明」と出す。**差分が読めないことでゴール詳細全体が壊れてはならない。**

### 4. 一覧と hunk を別の取得にする。分け方は `?path=` である

ゴール 139 は 28 ファイル・−684 行だった。**28 ファイル分の hunk を一度に返さない。**

分け方は `?path=` を選んだ。理由は 2 つ。

- **web 側で切る案は、一度 28 ファイル分を転送してから捨てることになる。**節約したい当のものを
  先に払う形なので成り立たない
- **サーバが `git` を 1 回多く呼ぶだけで済む。**ATCT 側にパーサが増えない。
  `?path=` 付きは `git diff <base>...<branch> -- <path>` の**生出力をそのまま文字列で返す**

一覧側だけは `--numstat` の出力を読む。**これは hunk のパースではなく、
`path / 追加 / 削除` の 3 列を読むだけである。**既存の `parseTaskCommitDiffNumstat` を再利用する。

### 5. タスク単位の表示は消さない

「誰が何をしたか」の記録として意味がある。**上に足す。**
`GoalDetail.tsx` のコミット節（`goal.commits.title` の `<section>`）と
`TaskCommitList.tsx` は触らない。

### 6. 非 ASCII のパスが化けないようにする

`core.quotePath` の既定は `true` で、日本語を含むパスが `"\346\227\245"` になる。
**`git -c core.quotepath=false` を付ける。**登録済みプロジェクトには日本語名のブランチが実在する。

## 応答の形

    // GET /api/goals/{id}/diff
    {
      "available": true,
      "reason": "",                    // "" | "not_git" | "no_base" | "no_branch" | "diff_failed" | "timeout"
      "base_ref": "main",
      "branch": "wt/goal-187",
      "files_changed": 3,
      "insertions": 286,
      "deletions": 684,
      "files": [{"path": "...", "insertions": 12, "deletions": 3, "binary": false}]
    }

    // GET /api/goals/{id}/diff?path=internal/httpapi/server.go
    {
      "available": true,
      "reason": "",
      "base_ref": "main",
      "branch": "wt/goal-187",
      "path": "internal/httpapi/server.go",
      "patch": "diff --git a/... \n@@ ...",
      "omitted_lines": 0
    }

**1 ファイルの diff が 2000 行を超えたら `patch` を空にして `omitted_lines` に総行数を入れる。**
途中で切ると unified diff として壊れ、`@git-diff-view/react` が読めない。
**切るのではなく出さない。**画面は「差分が大きいので表示しない」と出す。

`available: false` のときは `files` を空配列、`patch` を空文字にする。**エラーにしない。**

**`reason` に `"diff_failed"` を置いたのは、`git diff` 自体が失敗したときに `"no_branch"` を
返すと嘘になるからである。**ブランチはその手前で存在を確かめている。
**画面の扱いは同じ（節を出さない）でよいが、記録は区別する。**
前例は `doc/specs/2026-08-22-worktree-automation.md` の
「`worktree-setup.sh` が無い → 通す。このプロジェクトは worktree 運用をしていない」。

## 触らないと決めたもの

- **`goals.merge_commit` を持たない。**承認判断の時点でブランチは生きている。git に聞けば済む
- **`internal/daemon/wakeup.go` を触らない。**`wt/goal-<goal8>` を組み立てる処理が重複するが、
  同じリポジトリで 7 ゴールが同時に動いているので、パッケージをまたぐ整理はここでやらない。
  重複は完了報告の `needs_review` に残す
- **`parseTaskCommitDiffNumstat` を消さない。**タスク単位の表示が今も使っている
