# 1 space 1 ゴール（ゴール 137・2026-08-28）

**実装は 0 行。**変更は `skills/atct/SKILL.md` と `tests/wrapper_test.bash` の 2 ファイル。

## 決定の要旨

**space はゴール 1 つに属する。承認で閉じる。閉じたあと使い回さない。**
2 つ目のゴールには新しい space を作る。**例外は commander 自身の space だけで、他に無い。**

## なぜ使い回していたか、なぜその理由が消えたか

**理由は直列化だった。**同じファイルを触るゴールを 1 台に寄せれば、主チェックアウトを
共有していても衝突しない。**この理由は `## One worktree per goal` が入った時点で消えた。**
いまはゴールごとに `.worktrees/<goal>` があり、別 space が同じファイルを同時に編集しても
主チェックアウトでは衝突しない。

残るのはマージ時の衝突だけで、**それは commander の仕事**である
（`## Roles` の commander 行に `resolve conflicts` がある）。

## 導入順序の判断

**待たずに入れる。**ゴール 137 の本文は「`65e6e113`（ゴールごとに worktree）が入れば解ける。
入っていない状態でこの規則だけ入れると衝突が増える」と書いているが、**`65e6e113` は既に
入っている。**根拠は 3 つ:

    skills/atct/SKILL.md          `## One worktree per goal` の節が存在する
    script/worktree-setup.sh      存在し、worktree 内では exit 2 で止まる
    .worktrees/137                このゴール自身がその worktree で作業している

**したがって前提は満たされており、規則を待たせる理由が無い。**

## 使い回しの害（2026-08-26 の実測）

1. **space の名前がゴールを指さなくなる。**`atct-b01a92b8` が 5 ゴールを保持していた。
   名前から中身が引けない
2. **`atct_goal_sessions` が解けなくなる。**あれは `goal_handoffs.received_by` から
   セッション鍵を引く。1 つの鍵が 5 ゴールに紐づくと、どのゴールの space かが決まらない
3. **コンテキストが積み上がる。**ゴールが違えば無関係の依頼である
4. **閉じる契機が消える。**承認の時点で次のゴールを渡すと、その space は永久に閉じない。
   同日 commander は 15 space を手で閉じた

## `b1793296` の設計と 1 本の手順になるか

`b1793296` は「承認をトリガーに閉じる」を決めた。**このゴールはその後ろに
「閉じたあと使い回さない」を足すだけで、契機を動かさない。**合わせると手順は 1 本になる:

    ゴールを渡す      -> commander が space を作り、subcommander を立てる
    完了報告          -> subcommander が atct_goal_handoff_complete。**まだ閉じない**
    差し戻し          -> 同じゴールなので同じ space で続ける
    承認              -> commander が space を閉じる
    次のゴール        -> **新しい space を作る**

**閉じる契機を「完了報告」ではなく「承認」に置くのは `b1793296` の決定であり、
このゴールは変えない。**差し戻しは同じゴールの続きなので、報告で閉じると作業場を
作り直すことになる。

## 例外は 1 つだけ

**`commander` 自身の space。**プロジェクトを持つのであってゴールを持たないので、
どのゴールよりも長く生きる。ゴールとゴールのあいだで閉じない。

**他に例外は無い。**例外に見えて例外でないものが 3 つあるので、節に明示する:

| 見え方 | 実際 |
|---|---|
| 差し戻された完了 | 同じゴールの続き。2 つ目のゴールではないので space は開いたまま |
| `derived_from` で派生したゴール | **新しいゴール**なので新しい space |
| 同じファイルを触る 2 ゴール | それでも 1 ゴール 1 space。直列化は commander が「いつ渡すか」で決め、衝突はマージで解く |

## 触る場所

**新設節 `## One space per goal` を 1 つ足す。既存の節の本文は書き換えない。**
位置は `## One worktree per goal` の直後、`## Commit safely` の直前。space（誰がどこに
立つか）と worktree（ファイルがどこにあるか）は対になるので隣に置く。

**手順側の受け皿は `## One worktree per goal` の末尾に 1 行だけ足す。**ゴール 178 の spec が
「表は読まれても実行されない」を実測しているので、**規則は節だけでなく commander が歩く
手順にも要る。**当初は `## Delegate a goal` に step 7 を足す形で実装したが、
**commander の占有実測（2026-08-28）で `## Delegate a goal` はゴール 192 と 194 が
両方触っており最も混むと分かった**ため、step 7 は取り消した。

**代わりに `## One worktree per goal` を使う。**この節は空いており、しかも
「commander はゴールを渡す前に worktree を用意する」という**同じ瞬間の手順**を既に
書いている。space の用意と worktree の用意は同じ 1 手なので、1 行で繋がる。

    The commander prepares the goal's space at the same time as its worktree, and
    closes that space when the goal is approved; see `## One space per goal`.

## 検査（`tests/wrapper_test.bash` が grep する文字列）

節が消えても例外が消えても落ちるように、次を検査する。

| 検査 | 固定する文字列 |
|---|---|
| 節が存在し順番が正しい | `## One space per goal` が `## One worktree per goal` の後、`## Commit safely` の前 |
| 1 space 1 ゴール | `A space belongs to one goal` |
| 承認で閉じる | `approving the completion decision` |
| 使い回さない | `Do not hand it a second goal` / `A closed space is not reopened` |
| 例外が 1 つで他に無い | `there is no other` と、`commander` の space である旨 |
| 見え方だけの例外 | `A rejected completion is the same goal` |
| 手順にも入っている | `## One worktree per goal` の節に `closes that space when the goal is approved` が現れる |
| 混む節に入っていない | `## Delegate a goal` の節に `space` が現れない |
