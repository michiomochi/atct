# worktree を主チェックアウトの node_modules から切り離す

ゴール 191。`script/worktree-setup.sh` が `web/node_modules` を主チェックアウトへの
symlink として張るので、**worker（Codex の `workspace-write` sandbox）が pnpm を実行できない。**
worktree の外へ書けない相手に、worktree の外を指す `node_modules` を渡していた。

人間が decision 475 で設計を確定している。

> package.json 等の node_modules をいじらないと行けないのみ worktree 側の symlink を削除し、
> 親には影響しない形で worktree 側で作業できるようにするのはどうか

**全 worktree を実体にするのではなく、必要になった worktree だけを切り離す。**

## 何を作るか

    script/worktree-node-modules.sh status [<goal-id>]
    script/worktree-node-modules.sh detach [<goal-id>]
    script/worktree-node-modules.sh attach [<goal-id>] --yes

`internal/worktreescript/worktree_node_modules_test.go` が回帰を守る。
使う側の手順は `skills/atct/SKILL.md` の `## One worktree per goal` 内、
`### Detach node_modules before running pnpm` に置いた。

## 決めたことと理由

### 1. `worktree-setup.sh` を変えず、別スクリプトを足す

`worktree-setup.sh` は worktree の中では実行できない（`git_dir != git_common_dir` で
exit 2 する。`git worktree add` が主でしか意味を持たないため）。
**一方、切り離しが必要だと分かるのは worktree の中に居るときである。**
setup にフラグを足す形にすると、この非対称のせいで「必要だと分かった場所から実行できない」。

副産物として、同じ `worktree-setup.sh` を編集しているゴール 188（`web/dist` のコピー）と
競合しない。

対象 worktree の解決は `git rev-parse --path-format=absolute --git-common-dir` の親を主と
みなす形にした。**`BASH_SOURCE` からの相対では駄目である**——worktree 内のスクリプトを
実行したとき、worktree 自身を主と誤認して自分を自分に張る。

### 2. detach は「symlink を外す」が先、「pnpm install」が後

    1. nm が symlink        -> rm nm（symlink 1 個の unlink。指し先の中身は消さない）
    2. nm が実体のディレクトリ -> 既に切り離し済み。何もしない（冪等）
    3. ( cd <worktree>/web && pnpm install --frozen-lockfile )

**この順序がこのゴールの中心である。**逆にすると pnpm が symlink を辿り、
**主チェックアウトの node_modules を書き換える。**それが元の設計が恐れていた唯一の事故であり、
`worktree-setup.sh` のコメントが警告していたものである。

    node_modules is shared with the primary checkout. Running pnpm install in a
    worktree changes the primary checkout's dependencies too.

主の node_modules を `cp -R` で複製する案は採らない。pnpm の `node_modules` は
`.pnpm` 以下の実体と、その上に張られた symlink の農場である。`cp -R` は symlink の解決規則を
自前で再現することになるが、`pnpm install` なら pnpm 自身が整合を保証する。
**そして `pnpm install` は主へ 1 バイトも書かない**（下の実測）。

`--frozen-lockfile` を付ける。detach は「実体を作る」準備手順であって、
`pnpm-lock.yaml` を書き換える操作ではない。依存を変えるのはこの後の作業である。

**主が変わらないことを実測した**（2026-08-28、`.worktrees/191` を本当に切り離した前後）。
これが元の設計の懸念そのものであり、否定側の中心である。

    測るもの                        detach 前                    detach 後
    inode                           108725214                    108725214
    mtime                           1787848410                   1787848410
    nlink                           24                           24
    直下のエントリ一覧の sha256      d49a856ddbd5cb2c8e1...       d49a856ddbd5cb2c8e1...
    .pnpm 直下の件数                528                          528

    $ git status --porcelain -- web/
    （空。web/package.json と web/pnpm-lock.yaml は変わっていない）

測り方:

    P=<主>/web/node_modules
    stat -f 'inode=%i mtime=%m nlink=%l' "$P"
    ls -A "$P" | sort | shasum -a 256
    ls -A "$P/.pnpm" | wc -l

### 3. attach（復帰）は削除を伴うので 3 つの門を通す

`--yes` が無いときは何を消すかを出して exit 2 で止まる。`--yes` があっても、

    (b) 対象 worktree の realpath が <主>/.worktrees/ で始まること
    (c) nm の中に .pnpm が在る、または nm が空であること

を満たさないと exit 1 で拒否する。引数を間違えたときに `.worktrees` の外を消さないため。
**`rm -rf` を持つスクリプトに「呼び方を間違えても壊れない」性質を持たせるほうが、
呼び方を注意して回るより安い。**

### 4. 切り離しは既定にしない

`worktree-setup.sh` は今までどおり symlink を張る。**理由はディスクではなく時間である**
（6 節で測った）。`detach` は 1 本あたり 33 秒かかり、worktree の多数は pnpm を
一度も走らせない。`TestSetupLeavesSymlink` がこれを回帰から守る。

### 5. 切り離しの条件は「依存を変えるとき」ではない。「pnpm を走らせるとき」である

**測った**（2026-08-28、`.worktrees/191` の executor。`workspace-write` sandbox。
測り方と全出力は `doc/investigations/2026-08-28-worktree-node-modules-sandbox.md`）。

    symlink 越しの read              通る    ls / cat astro/package.json /
                                             node -e require.resolve すべて exit 0
    symlink 越しの write             拒まれる touch と mkdir -p が Operation not permitted
    worktree の中への write（対照）  通る

    pnpm test   exit 1   EPERM open   web/node_modules/.vite-temp/vitest.config.ts.timestamp-*.mjs
    pnpm build  exit 1   EPERM unlink web/node_modules/.vite/deps/@astrojs_react_client__js.js

**読めるのに動かない。**vitest は設定ファイルを束ねる中間物を `node_modules/.vite-temp` へ書き、
vite は依存の最適化結果を `node_modules/.vite` へ書く。どちらも symlink 越しなので拒まれる。

したがって切り離しの条件は次のようになる。

    package.json / pnpm-lock.yaml を変える    切り離しが要る
    pnpm build / pnpm test を走らせるだけ      **切り離しが要る**
    node_modules を読むだけのコード検索        symlink のままでよい

**ゴール 188 の「読むだけなのに動かない」がこれで説明される。**
188 の executor は依存を 1 つも変えていないのに `pnpm build` で止まった。
「依存を変える作業だけ切り離す」という条件では 188 は救われない。**pnpm を走らせるかで切る。**

decision 475 の「node_modules をいじらないと行けないのみ」という条件そのものは変えていない。
**変わったのは「いじる必要がある」の範囲である。**vite と vitest が `node_modules` の中を
作業領域として使うので、ビルドとテストも「いじる」側に入る。

### 6. 切り離しは元に戻せる。既定にしない理由はディスクではなく時間だった

`attach --yes` が symlink を張り直す。実体の `node_modules` は捨てて、
必要になればまた `detach` で作り直せる。**実測で往復させた**（2026-08-28、`.worktrees/191`）。

    attached -> detach -> detached -> attach --yes -> attached -> detach -> detached

**ディスクは理由にならなかった。**`du` の値と実消費が 30 倍違う。

    du -sm .worktrees/191/web/node_modules        454 MB
    df -m / の増減（detach の前後）                 15 MB 消費
    df -m / の増減（attach の前後）                  0 MB 解放

**`du` が嘘をつくのではなく、数え方が APFS の copy-on-write clone を拾えない。**
pnpm は macOS では既定でストアから clone する。**ハードリンクではない**——実測で、主と
worktree の同じファイルは inode が別で `nlink=1` だった。

    $ du -smc <主の node_modules> <worktree の node_modules> | tail -1
    910    total          <- 個別の du の単純和。共有を拾えていない

clone はブロックを共有するので、`du` が 454 MB と言うものを消しても 0 MB しか空かない。

**したがって「全 worktree を実体にするとディスクを食う」という元の懸念は、この機械では
桁が違っていた。**7 本を全部切り離しても実消費は 100 MB 程度である。
それでも既定にしないのは**時間**である。`detach` は 1 本あたり `pnpm install` に 33 秒かかり、
**worktree の多数は pnpm を一度も走らせない。**

（機械は他の単位も並行して動かしていたので `df` の 15 MB には雑音が混じる。桁が主張である。）

### 7. 切り離すのは委譲する側である。worker は自分で切り離せない

**測った**（2026-08-28、切り離し済みの `.worktrees/191` で executor が実行）。

    pnpm test                        exit 0    15 files / 231 tests
    pnpm build                       exit 0    astro check 0 errors、3 ページ生成
    pnpm install --frozen-lockfile   exit 1    ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY

**切り離した後は worker が pnpm を通せる**（完了条件 4）。
一方 `pnpm install` は通らない。理由は sandbox の拒否ではなく、pnpm が
`node_modules` の purge を確認しようとして TTY が無いことである。
`CI=true` か `confirmModulesPurge=false` を与えれば進むが、**それは purge を承諾することであり、
委譲側が 33 秒かけて作った実体を worker が消して作り直す。**

したがって規約はこうなる。**`detach` は委譲する側が、依頼を出す前に実行する。**
worker は `detach` も `pnpm install` も実行しない。
`doc/specs/2026-08-27-the-workers-sandbox-is-not-the-delegators.md` の決定 2
（通らない検証は delegator が肩代わりする）と同じ形である。

`git status --porcelain -- web/` は空のままだった（`web/dist/*` は `.gitignore` 済み）。

## 隣接ゴール

    180  executor が構造的に止まる 2 件（socket connect / httptest の bind）。これが 3 件目
    188  リリースが web を作り直さない。同じ worktree-setup.sh が web/dist のコピーもやっている
