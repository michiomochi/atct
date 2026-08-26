# worktree はチェックアウトの中に置く

人間の判断（2026-08-22）:「今後 worktree は atct ディレクトリ内に作りたい」
置き場所は `.worktrees/<goal8>`。

## なぜ隣をやめたか

隣（`../atct-wt-<goal8>`）に置いていたので、片付け忘れが溜まった。2026-08-22 の時点で
`atct-wt1` / `atct-wt2` / `atct-wt3` / `atct-wt-goal 108` の 4 つが並び、
**中身が主に取り込み済みかを 1 ファイルずつ突き合わせてから消した。**
ブランチ 2 本（`wt/executor-1` / `wt/executor-2`）は worktree より長く生き残っていた。

## 標準は無い。流儀が 2 つある

**git 自身は場所を決めていない。**`git worktree add` はパスを必須引数にしており、
既定の場所を決める設定も無い（git 2.50.1 の `worktree.*` は `guessRemote` と
`useRelativePaths` の 2 つだけ）。man の例は `../temp` / `../hotfix`。

    兄弟ディレクトリ        ../myapp-feature     git の man の例。記事類では「最も一般的」
    リポジトリ内の無視配下  .worktrees/<name>    少数派だが確立

**この 2 つのエージェントは、どちらも後者を採っている。**

    Claude Code   .claude/worktrees/<name>   公式ドキュメントが「.gitignore に入れよ」と明記
    Codex         .codex/worktrees/<name>    設計。手元の codex-cli 0.148.0-alpha.15 は
                                             --help に worktree 関連が 0 件で未対応
                                             （CLI フラグは openai/codex#12862 の提案段階）

## 実測: リポジトリ内に置いても壊れない（2026-08-22）

作業ツリーを 1 つ作った状態で測った。

    git status --short      空（/.worktrees/ が効いている）
    go build ./...          exit 0
    go list ./...           12 パッケージ（基準と同じ）。worktrees を含むもの 0 件
    go test -count=1 ./...  9 パッケージ ok
    pnpm typecheck          0 errors / 0 warnings / 13 hints
    pnpm test               13 ファイル・192 テスト通過

**理由はドットではない。**「ドット始まりだから Go が無視する」と考えて検算したら、
**ドットなしの `worktrees-probe` でも 0 件だった。**本当の理由は**入れ子の `go.mod`** で、
Go の `./...` が別モジュールに降りないこと。ドットの利点は人間側の道具
（ripgrep・fd・エディタの既定の走査）だけである。

## `.claude/worktrees/` を選ばなかった理由

**掃除ではない。**Claude Code の掃除の対象は「Claude が **subagent と background session の
ために作った** worktree」であり、`--worktree` で作ったものは決して消さない。
**`git worktree add` をスクリプトで叩いたものは追跡されていないので、掃除もされない。**
つまり消される危険も無いし、掃除の恩恵も無い。

**理由は `EnterWorktree` の承認である。**

> When Claude enters a path **outside** the repository's `.claude/worktrees/` directory,
> Claude Code asks for your approval first… Once in a worktree, Claude can switch
> directly to another one **under `.claude/worktrees/`** by calling EnterWorktree.

`.claude/worktrees/` に置くと、**Claude セッションが承認なしに他の worktree へ移れる。**
3 層では commander が subcommander の作業ツリーへ黙って入れることになる。
外に置けば承認が挟まり、それが防波堤になる。

`.atct/worktrees/` も生態系のパターンには合うが、**`~/.atct/` が既に DB・ソケット・
バイナリの置き場**なので、リポジトリ内の `.atct/` は取り違えを招く。

## パスの所有者はスクリプト 1 つだけ

`~/.claude/hooks/worktree-before-space.sh`（208 行）は**パスを持っていない。**

    152 行  setup_path="$target/script/worktree-setup.sh"
    199 行  # worktree-setup.sh prints two lines: "worktree: <path>" then a "next: ..." hint.

スクリプトが印字する 2 行を読むだけである。

    script/worktree-setup.sh
      printf 'worktree: %s\nnext: cd %s && go test ./...\n' "$worktree" "$worktree"

**この形を崩さないこと。**場所を変えるのにフックを触る必要が出たら、設計が間違っている。
実際、この変更（`192dbdf`）はフックを 1 文字も変えずに通った。

## 入れ子を拒む

`repo` はスクリプト自身の位置から決まるので、**作業ツリーの中のスクリプトを叩くと
`.worktrees/a/.worktrees/b` ができる。**判別して拒む。

```bash
git_dir="$(git -C "$repo" rev-parse --absolute-git-dir)"
git_common_dir="$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir)"
if [[ "$git_dir" != "$git_common_dir" ]]; then
  echo "作業ツリーの中では実行できない。主チェックアウトで実行しろ" >&2
  exit 2
fi
```

    主チェックアウト  --absolute-git-dir = .../atct/.git                       一致
    作業ツリー        --absolute-git-dir = .../atct/.git/worktrees/<name>      不一致
                      --git-common-dir   = .../atct/.git

**`--path-format=absolute` を付けること。**付けないと `--git-common-dir` が相対パスで返り、
比較が成り立たない。

## 片付けの手順

**この順序で行う。**

```bash
# 1. node_modules のリンクを先に外す。主の 434M を指しているので、たどって消すと主が壊れる
python3 -c "import os; p='.worktrees/<goal8>/web/node_modules'; os.path.islink(p) and os.unlink(p)"

# 2. 作業ツリーを消す
git worktree remove --force .worktrees/<goal8>

# 3. ブランチも消す。ここを忘れる
git branch -D wt/goal-<goal8>

# 4. .worktrees が空なら消す
python3 -c "import os; os.path.isdir('.worktrees') and not os.listdir('.worktrees') and os.rmdir('.worktrees')"
```

**検算する。**

```bash
git worktree list            # 1 個（主だけ）
git branch --list 'wt/*'     # 0 本
git status --short           # 空
du -sh web/node_modules      # 434M（主が無事）
```

### `rm` を使わない

**`rm` は `rm -i` に別名定義されている。**非対話で使うと確認待ちになり、
**`&&` で繋いだ次のコマンドが走るので成功したように見える。**
実測（2026-08-22）: `rm ../wt/web/node_modules && echo "外した"` が 4 回とも
「外した」と印字したのに、リンクは 4 つとも残っていた。`python3` の `os.unlink` を使う。

### ブランチを忘れる

2026-08-22 に作業ツリーを 4 つ消したあと、ブランチが 4 本残っていた
（`wt/executor-1` / `-2` / `-3` / `wt/goal-goal 108`）。
**先端が main に含まれることを確認してから消す。**

```bash
git merge-base --is-ancestor wt/goal-<goal8> main && git branch -d wt/goal-<goal8>
```

## フックの誤検知（既知）

フックは入力全体を文字列で見ているので、**`herdr workspace create --help` も止まる。**
`worktree` について書いた文書を書き込むコマンドも止まる（heredoc の中身が発火条件に一致する）。
回避するには本文をファイルへ書いてから結合する。詳細は
`doc/specs/2026-08-22-worktree-automation.md`。
