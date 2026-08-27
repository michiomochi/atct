# atct スキルの順序を番号で固定し、検査で崩れないようにする

ゴール 194。2026-08-28。

## 何が起きているか

`skills/atct/SKILL.md` は 22 節あるが、番号リストがあるのは `## Delegate a task` と
`## Delegate a goal` の 2 節だけである。**順序のある操作が散文で書かれている。**

2026-08-27〜28 の 1 日で守られなかったもの 7 種のうち、3 種は順序の問題だった。

    1  executor が `atct_goal_handoff_complete` を呼んだ（ゴール 144）
    2  `atct_goal_handoff_complete` を `atct_goal_complete` より先に呼んだ（ゴール 180・187）
    3  `atct_task_claim` を `atct_handoff_request` より先に呼んだ（ゴール 144・134）

**ただし番号リストだけでは守られない。**1 と 3 は、既に 6 段の番号リストである
`## Delegate a task` の中で起きた。commander が pane で正しい順序を 2 回配っても
180 と 187 は逆に呼んだ。

今日守られたものはすべて機構だった（ゴール 127・141・181）。**したがってこのゴールは
番号リストと検査を同時に置く。**

## 決定

### D1. 番号にする節を「操作が 2 つ以上あり、逆順にすると結果が変わる」で選ぶ

節の見出しが命令形かどうかでは選ばない。次の両方を満たす節だけ番号にする。

- 読み手が順に行う**操作**が 2 つ以上ある
- **逆順に行ったときの結果が違う**（落ちる、記録が失われる、ダッシュボードが嘘をつく）

理由: 基準が無いと「手順っぽい節」を全部番号にしてしまう。役割表・分類・書き方の指針を
番号にすると、番号が「順に読め」の意味を失い、本当に順序のある節と区別できなくなる。
既に番号である `## Delegate a task` / `## Delegate a goal` が効いていないのは、
番号が薄まっているからではなく逆順の結果が書かれていない段があるからで、D2 がそれを扱う。

**番号にする節（10）**

| 節 | 順序 | 逆順の結果 |
|---|---|---|
| `## Declare before you work` | declare -> 作業 | 作業がダッシュボードに出ない |
| `## Claim before you start` | claim/receive -> 作業 -> `done` | 二重着手、または未着手に見える |
| `### Two-layer delegation` | `atct_goal_claim` -> `atct_handoff_request` | 委譲できない |
| `## Delegate a task` | 既存 6 段 | 既存 |
| `## Delegate a goal` | 既存 6 段（D3 で `atct_goal_complete` を追加） | D3 |
| `## Fill in a report on a handoff that is already closed` | 閉じているのを確かめる -> amend | 正常完了の経路を壊す |
| `## Recover when your role comes back wrong` | 停止 -> `atct_session_identify` -> 層ごとの復旧 | 復旧が要らない場面で handoff を閉じ直す |
| `## Close a task the moment it is finished` | 作業着地 -> `done` -> 次を claim | 未着手として残る |
| `## Report completion in six parts` | commit -> タスクを閉じる -> 6 欄を埋めて `atct_goal_complete` | コミット 0・タスク todo のまま「完了」と表示される |
| `## Act on reversible choices, ask about irreversible ones` | 分類 -> 可逆は実行してから記録 / 不可逆は聞いてから | 取り消せない操作が先に走る |
| `## Apply what you were told` | `atct_decision_poll` -> 依存する作業を続ける | 人間の回答が届いたか分からない |
| `## Finishing` | 開いた decision を答えるか取り下げる -> `done` -> `atct_goal_complete` | `done` にできない |

（表は 12 行あるが `## Delegate a task` と `## Delegate a goal` は既存なので新規は 10 節）

**番号にしない節と理由**

| 節 | 理由 |
|---|---|
| `## Roles` | 役割表。操作が無い。派生順は daemon の内部順で読み手の操作ではない |
| `## Fix a declared task` | `todo`/`doing` のときだけ直せる、という制約であって順序ではない |
| `## One worktree per goal` ほか worktree の 3 節 | 誰がどこで作業するかの規則。操作の列ではない |
| `## Commit safely` | 2 文。逆順が無い |
| `### Session keys` | セッションキーの性質。操作は 1 つ |
| `## What the delegator answers` | 問いの分類 |
| `## Where an unsent report goes` | 対応表 |
| `## Keep going` | **`skills/start/SKILL.md` の `## The loop` が同じ手順を既に 6 段の番号で持っている。**ここで番号にすると同じ手順が 2 か所になり、片方が腐る（完了条件 9） |
| `## Ask instead of guessing` | いつ聞くかの判断基準 |
| `## Ask here, not in conversation` | 聞く場所の規則 |
| `## Write so the answer takes ten seconds` | 書き方の指針 |
| `## Name goals after the symptom, not the mechanism` | 命名の指針 |

### D2. 番号のある節には `**Out of order:**` の段落を必ず置く

番号のある各節に、次の形の段落を 1 つ置く。

    **Out of order:** <逆順に呼んだときに起きること>

理由: 番号は「何を先にやるか」しか伝えない。**1 と 3 が既に番号リストの中で起きたのは、
番号が読み飛ばせるからで、読み飛ばせるのは従わなかったときの損失が書かれていないから
である。**ゴール 181 が `## Delegate a task` で `atct_handoff_complete` ->
`atct_task_update` についてやったのと同じ形を、順序のある全節に広げる。

固定の文言にするのは検査できるようにするためでもある。散文で書くと grep で見つけられず、
新しい節が来たときに追随できない。

### D3. `## Delegate a goal` に `atct_goal_complete` を足す

現在の手順 5（`grep -c atct_goal_complete` は 0）は
「完了時は `atct_goal_handoff_complete` を呼べ」しか書いていない。

正しい順序は次である。

    1. 作業を commit する
    2. タスクをすべて閉じる
    3. `atct_goal_complete` で完了報告を出す（6 欄）
    4. `atct_goal_handoff_complete` で goal handoff を閉じる

**Out of order の内容**: `atct_goal_handoff_complete` を先に呼ぶと、goal handoff が閉じた
時点で役割が `subcommander` から `executor` に落ちる。`atct_goal_complete` は goal の
保持者しか呼べない（ゴール 127 の門番）ので、**完了報告が出せなくなる。**
ゴール 180 と 187 がこれで詰まった。復旧には commander による goal handoff の再発行が要る。

### D4. 検査は「自動で追随する部分」と「登録が要る部分」に分ける

`tests/wrapper_test.bash` に置く。前例は `test_role_contract_matches_implementation`。

**自動で追随する検査（新しい節を足しても手を入れなくてよい）**

- **A1 番号の連続性**: `skills/atct/SKILL.md` と `skills/start/SKILL.md` の全節を走査し、
  行頭 `^[0-9]+\. ` の項目を集める。番号が 1 から連続していない節（抜け・重複・1 始まりでない）
  があれば落ちる。
- **A2 番号は 2 項目以上**: 番号リストが 1 項目しかない節があれば落ちる。項目が 1 つなら
  順序ではない。
- **A3 番号のある節は `**Out of order:**` を持つ**: 番号リストのある節に D2 の段落が
  無ければ落ちる。

**登録が要る検査（節を足したら手を入れる）**

- **B1 順序のある節が番号になっている**: `ORDERED_SECTIONS` に列挙した見出しに番号リストが
  無ければ落ちる。**「番号にすべきなのに番号が無い」は本文からは判定できない**ので、
  ここだけは列挙する。

追随のさせ方は `CONTRIBUTING.md` に書く（完了条件 7）。

### D5. 検査そのものを両側から検査する

検査ロジックを `numbering_violations <file>` と `out_of_order_violations <file>` の
ヘルパーに切り出し、次の 2 通りで呼ぶ。

- **肯定側**: 実物の `SKILL.md` に対して違反 0 を要求する（完了条件 6）
- **否定側**: 番号を崩した一時ファイル（`1. 2. 4.` / `1. 1. 2.` / `2. 3.` / `**Out of order:**` を
  抜いたもの）に対して違反が出ることを要求する（完了条件 5）

理由: 否定側だけ書くと「常に落ちる検査」を見逃す。肯定側だけ書くと「何も検出しない検査」を
見逃す。ゴール 181 の検査は肯定側だけだったので、崩したときに落ちることを人手で確かめる
必要があった。ここでは両側を恒久的にリポジトリへ残す。

## このゴールで扱わないもの

7 種のうち 4・5・6 は「終わったかの確認」の問題で、番号リストでは解けない。

    4  完了報告に存在しないコミット SHA を書いた（ゴール 181）  -> 未起票
    5  完了報告を出したがコミット 0・タスク全 todo（ゴール 144） -> ゴール 192 が一部を扱う
    6  タスクを done にしたが成果物が未コミット（ゴール 172）    -> 未起票

D3 の手順（commit -> タスクを閉じる -> 完了報告）は 5 と 6 を**文章で**言うだけで、
機構ではない。機構は別ゴールである。
