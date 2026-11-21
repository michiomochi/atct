# atct スキルを役割で分けるかを決める

ゴール 194 の却下（decision 494）への回答。2026-08-28。

## 問い

人間が完了報告を却下し、理由にこう書いた。

> atct skill が長くなってきたから atct-commander, atct-subcommander, atct-executor skill に
> わけるのはどう？

## 実測

### 分量

    $ wc -l skills/atct/SKILL.md
    679 行（22 節。ゴール 194 の番号リスト化を含んだ後の値）

commander が pane で配った 582 行は**ゴール 194 の変更前の値**である。

### 各役割が実際に要る分量

`## Delegate a task` は executor が呼んでよい ATCT ツールを 5 つ、呼んではいけない
ツールを 13 個、名指しで列挙している（ゴール 181）。**その一覧を使って、節ごとに
「executor が呼べるツールしか出てこない節」と「呼べないツールが出てくる節」を数えた。**

| 役割 | 実際に要る節 | 行数 |
|---|---|---|
| `executor` | `## Roles` 15 / `## Close a task the moment it is finished` 19 / `## Recover when your role comes back wrong` 29 / 依頼書へ転記される契約ブロック 51 | **114 行 / 679 行（17%）** |
| `subcommander` | ほぼ全体 | 約 600 行 |
| `commander` | ほぼ全体 | 約 600 行 |

### commander の「役割に依存しない 14 節」は誤りである

commander は節ごとの役割名（`commander` / `subcommander` / `executor` の語）の出現数を
数え、**14 節が 0/0/0 なので役割に依存しないと結論した。**

**役割名を数える方法では見えない。**その 14 節の大半は、**executor が呼んではいけない
ツールを扱う節**である。

    ## Declare before you work          atct_task_declare        <- executor 禁止
    ## Keep going                       atct_goal_list           <- 自走の節
    ## Ask instead of guessing          atct_decision_ask        <- executor 禁止
    ## Ask here, not in conversation    atct_decision_ask        <- executor 禁止
    ## Report completion in six parts   atct_goal_complete       <- executor 禁止
    ## Apply what you were told         atct_decision_poll       <- executor 禁止
    ## Finishing                        atct_goal_complete       <- executor 禁止
    ## Commit safely                    （executor は commit しない）

**これらは役割非依存ではなく delegator 専用である。**役割名が本文に出てこないのは、
禁止が `## Delegate a task` に集約されているからにすぎない。

## 決定

### D1. 分割は妥当だが、3 分割ではなく 2 分割である

**切れ目は executor / delegator の 1 本しか無い。**

commander と subcommander が要る節はほぼ同じで、違いは
「project claim を持つか」「他ゴールを見てよいか」だけである。3 分割すると
**2 つがほぼ同じ内容になり、複製 1 組が生まれる。**

    atct            delegator（commander + subcommander）。役割差は今どおり本文に併記
    atct-executor   worker の契約。約 114 行

### D2. いま実施しない。5 つのゴールが同じファイルに同時に入っている

2026-08-28 時点で `skills/atct/SKILL.md` を編集しているゴール:

    192  ## Delegate a goal / ## Where an unsent report goes
    183  ## Recover when your role comes back wrong
    146  ## Report completion in six parts
    137  ## One worktree per goal + 新設 ## One space per goal
    191  ## One worktree per goal
    194  上記 11 節（着地済み）

**ファイルを 2 つに割ると、この 5 件すべてのマージが壊れる。**
分割は 5 件が main に着地したあとに行う。

### D3. executor スキルには固有の設計問題がある。それが分割の主要な作業である

**executor の契約は今、スキルではなく依頼書に載っている。**
`## Delegate a task` 手順 4 は「Put these exact instructions at the very beginning of
the request」と書き、delegator が契約を丸ごと転記する形になっている（ゴール 181 の設計）。

**`atct-executor` スキルを作ると、契約の出所が 2 つになる。**
このリポジトリが繰り返し書いている「同じことが 2 か所にあると片方が腐る」に当たる。

**分割ゴールが決めるべきこと**:

1. **どちらを正とするか。**スキルを正にすると依頼書は参照だけになるが、
   **スキルを読まない worker（Codex executor、sub-agent）に契約が届かない**
2. 依頼書を正のままにするなら、`atct-executor` スキルは何を持つのか。
   `## Roles` と `## Recover when your role comes back wrong` だけになり、
   **分割で減る分量は 114 行のうち 44 行にとどまる**
3. 検査をどう保つか。`tests/wrapper_test.bash` は現在 40 本以上が
   `skills/atct/SKILL.md` を単一ファイルとして grep している

### D4. 分量を減らしても、機構が無ければ守られない

**2026-08-28 17:56 に 5 単位（137 / 156 / 164 / 176 / 185）が同時に
`atct_goal_handoff_complete` を先に呼び、`has_report=0, open_approval=0, open_handoff=0`
に落ちた。**正しい順序は commander が pane で 4 回以上配っていた。

今日の通算で goal handoff の再発行は 13 回。引き金の内訳:

    順序の誤り（handoff_complete が先）  8 件  <- 最多
    daemon 再起動                        3 件
    executor の誤呼び出し                1 件
    transport の再接続                   1 件

**8 件すべてが、正しい順序を配られたあとに起きている。**

同じことが 2026-08-28 のコミット紐づけでも起きた。**11 単位中 8 単位が
`task_commits` を空のまま done にした。**原因は分量ではなく、
**`commits` 引数が 2 つのスキルのどこにも書かれていなかったこと**である
（`grep '`commits`'` が 0 件）。ゴール 194 がこれを直した。

**したがって分割の位置づけは「守らせる手段」ではなく「読む負荷を下げる手段」である。**
守らせるのは機構の仕事で、ゴール 192 が扱う。

## ゴール 194 との関係

**却下理由への回答はこの spec である。**ゴール 194 の完了条件 9 項目はすべて満たしている
（順序のある節の番号リスト化、`## Delegate a goal` への `atct_goal_complete` 追加、
連続性と `**Out of order:**` の検査、崩したときに落ちることの実測、英語、
`skills/start/SKILL.md` との非重複）。**分割はその 9 項目のどれでもない。**

**分割を 194 に取り込まない理由は範囲ではなく D2 である。**
5 件が同じファイルに入っている状態で割ると、5 件のマージが壊れる。
