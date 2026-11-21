# subcommander が commander へ何を送るか（ゴール 178・2026-08-27）

**実装は 0 行。**変更は `skills/atct/SKILL.md` と `tests/wrapper_test.bash` の 2 ファイル。

## 決定の要旨

**subcommander から commander への連絡は `atct_goal_handoff_complete` の 1 通だけにする。**
途中経過・受領確認・設計の共有・発見の共有・設計の相談は、どれも送らない。
送っていた内容は ATCT の記録に落とす。commander の関与は、handoff が閉じた後の
最終レビューだけにする。

## なぜ規則ではなく手順に書くか

2026-08-26 のゴール 154 が「規則を書いても届かない」を実測している。
`## Roles` の表には既に「commander は goal の設計をしない」と書いてあるが、
2026-08-27 に commander は設計に踏み込んで 2 回誤った。

    「tools.go を触らないと MCP から呼べない」   -> 誤り。ゴール 134 が訂正
    「wakeup.go が files を読む」                 -> 誤り。ゴール 139 が訂正

**表は読まれても実行されない。**だから今回の変更は、`## Delegate a goal` の
**番号付き手順**と、**依頼書に貼り付ける引用ブロックの本文**に入れる。
どちらも commander が委譲のたびに通る場所である。

## 触る場所と、ゴール 181 との切り分け

ゴール 181 は「executor が ATCT を呼ぶか呼ばないか」（`skills/atct` と
`orchestration` の矛盾）を扱う。同じ `skills/atct/SKILL.md` に入るが、
**触る節を分ければ、どちらが先に入っても壊れない。**

| ゴール | 触ってよい場所 |
|---|---|
| 181 | `## Delegate a task` の step 4 引用ブロック（executor 向けの前文） |
| 178 | `## Delegate a goal` の step 3・step 4 引用ブロック・新しい step 6、および新設する 2 節 |

**178 は `## Delegate a task` に 1 文字も触らない。**この切り分けを固定するために、
`## Delegate a task` の節に 178 の文言が現れないことを検査する
（`test_task_delegation_preamble_is_untouched_by_upward_silence`）。
181 が先に入っても、178 が触る行と重ならないため衝突しない。逆も同じ。

**181 は今日 3 件の事故を起こしており、うち 1 件は executor が subcommander の
goal handoff を閉じて役割が落ちたものである。**178 は goal handoff の前文を
「送るな」の方向にしか変えないため、executor 側の呼び出し規約には影響しない。

## 完了条件ごとの決定

### 1. 依頼書の定型に「ゴール内部の判断を commander に聞くな」が入る

`## Delegate a goal` の step 4 引用ブロックに、`atct watch` の段落と
「When the work is complete」の段落の**あいだ**に次を挿入する。

> Decide this goal's design yourself. Do not bring the delegator a design
> question, a progress note, a receipt acknowledgement, a discovery, or a
> reading of this goal's code. Send the delegator nothing until the completion
> report. What you would have said goes into the record instead: a task for
> work in flight, `surprises` and `needs_review` for what you found,
> `next_steps` for what you left, and `atct_decision_ask` for anything that
> needs the human.
>
> A fact that spans another goal is not an exception. Raise it with
> `atct_decision_ask`; the answer reaches you through your own watch, without
> passing through the delegator.

順序は `role` < `watch` < この段落 < `completion` とする。既存の
`test_goal_handoff_watch_contract_has_required_order` と同じ形で検査する。

### 2. commander が答えてよいものの列挙と、同数の否定形

新節 `## What the delegator answers` を `### Session keys` の直後に置く。
**4 個と 4 個**にする。数を揃えるのは、片方だけが伸びて「答えてよいもの」が
既定になるのを防ぐためである。検査は箇条書きの個数を数え、両者が等しく 4 であることを見る。

答えてよい（**委譲した側にしか見えないもの**）

- 2 つのゴールが取り合う作業がどちらの担当か
- このゴールが触るファイルを、いま別のどのゴールが編集しているか
- ある変更が既に main に入っているか
- リリースの時期

答えてはいけない（**コードを読んだ側にしかわからないもの**）

- 2 つの設計のどちらを採るか
- このゴールのコードのある関数が実際に何をしているか
- このゴールの作業をどう commit に割るか、どの順で入れるか
- このゴールの作業を executor へどう分けるか

後者に答えると必ず推測になる。**2026-08-27 の 2 件の誤りはどちらも後者である。**
節の本文にこの実測を残す。

### 3. commander の関与が最終レビューに限られることを手順にする

`## Delegate a goal` に step 6 を足す。

> 6. Stay out until the completion report. After waking the subcommander, the
>    delegator sends it nothing and answers nothing about the goal's design.
>    What the delegator reads instead are this project's ATCT detections, which
>    arrive from `atct watch` rather than from the subcommander: a goal with no
>    commits, a goal with no declared tasks, a claim nobody delegated, a handoff
>    nobody received. Those are what a stalled subcommander looks like from
>    outside, and they arrive whether or not it speaks. Review the goal when
>    `atct_goal_handoff_complete` lands; that report is the entry point.

### 4. 連絡はゴール完了時の 1 通だけ

(1) の「Send the delegator nothing until the completion report.」がこれを名指しする。
禁じるものを列挙するのは、「相談は駄目だが報告はよい」と読まれるのを防ぐためである。
**人間の指示は「報告のみでも送ってくるな」であり、理由は commander のトークンである。**

### 5. それでも失われない経路

新節 `## Where an unsent report goes` を (2) の節の直後に置き、対応表を書く。

| いま口頭で送っているもの | 落とし先 |
|---|---|
| 受領確認 | `atct_goal_handoff_receive` の記録そのもの |
| 途中経過 | タスク（`atct_task_declare` と `done`） |
| 設計とその理由 | ゴールの作業と一緒にコミットする spec と `work_done` |
| 自ゴール内の発見 | `surprises` / `needs_review` |
| 別ゴールになる発見 | `atct_decision_ask`（人間宛て） |
| やり残し | `next_steps` |
| 完了 | `atct_goal_handoff_complete`——これが唯一の 1 通 |

**表だけでは足りない穴が 1 つある。止まった subcommander は何も送らない。**
2026-08-27 の実測 2 件がこれである。

    ゴール 172  タスク 3 件 todo・未コミット 8 件・両 pane が idle で停止
    ゴール 144  handoff が閉じているのにコミット 0・タスク 4 件すべて todo

**どちらも検知は鳴っていた。**足りなかったのは検知ではなく、
「commander が読む」ことが手順になっていなかった点である。だから (3) の step 6 で
**commander が読むものを検知だと名指しする。**検知は subcommander の発話に依存しないので、
黙らせても消えない。節の末尾にこの 2 件を実測として残す。

### 6. 他ゴールにまたがる事実を subcommander が自分で引ける形を作るか

**決定: 作らない。**専用の読み取りツールは追加せず、「他ゴールを見るな」の禁止も緩めない。
代わりに、**境界を書くのを commander の手順にする**（step 3 に追記）。

> Name in the request every adjacent goal that touches the same files and say
> which side owns what. The delegator is the only party that can see both
> goals, and a boundary left unstated becomes a question the subcommander
> cannot answer for itself.

理由は 3 つ。

1. **境界は commander が分割した時点で持っている情報である。**依頼書に書けば
   質問そのものが消える。今日の 6 件のうち唯一 commander の仕事だった
   「D9-4 の掃除ループは 92 の担当か」は、依頼書に境界が書かれていなかったから起きた。
   **事実、このゴール 178 の依頼書には隣接ゴール 181 との境界が書かれており、
   その 1 件は発生していない。**手順で消せることが同じ日に実証されている
2. **残る問いは事実照会ではなく分割の判断である。**「掃除ループはどちらの担当か」は
   読み取りツールがあっても答えが出ない。**決めるのは人間であり、
   `atct_decision_ask` が既にその経路である。**新しいツールは経路を増やすだけで、
   答えを増やさない
3. **ツールを作ると禁止の根拠が壊れる。**「他ゴールを見るな」の理由は、
   他ゴールの内容で subcommander のコンテキストを埋めないことである。
   読み取り口を開ければ、禁止の実効範囲がツールの返す量で決まるようになり、
   禁止は名目だけになる

**ファイル衝突の事実については、聞かずに済ませる。**衝突の解消は commander の
`## Roles` に既にある仕事（resolve conflicts）であり、**マージ時に解ける。**
subcommander が事前に聞いて防ぐ必要はない。防ごうとすると、そのために他ゴールを
読むことになり、禁止と正面から衝突する。

### 7. (1) と (4) を壊すと落ちる検査

`tests/wrapper_test.bash` に、既存の `delegate_goal_section` 系と同じ形で足す。
節の抽出関数を 2 つ追加する。

```bash
delegator_answers_section() {
  sed -n '/^## What the delegator answers$/,/^## Where an unsent report goes$/p' \
    "$REPO_ROOT/skills/atct/SKILL.md"
}
unsent_report_section() {
  sed -n '/^## Where an unsent report goes$/,/^## Fill in a report on a handoff that is already closed$/p' \
    "$REPO_ROOT/skills/atct/SKILL.md"
}
```

足す検査は次の 12 個。

| 検査 | 何が壊れると落ちるか |
|---|---|
| `test_goal_handoff_forbids_upward_design_questions` | (1) の 1 段落目 |
| `test_goal_handoff_names_the_single_upward_message` | (4) の「Send the delegator nothing until the completion report.」 |
| `test_goal_handoff_routes_cross_goal_facts_to_the_human` | (1) の 2 段落目 |
| `test_goal_handoff_silence_has_required_order` | watch と completion のあいだにあること |
| `test_goal_handoff_preamble_does_not_invite_upward_reports` | 「報告してよい」と読める語が入ること |
| `test_goal_delegation_requires_the_adjacent_goal_boundary` | (6) の step 3 追記 |
| `test_goal_delegation_keeps_the_delegator_out_until_completion` | (3) の step 6 |
| `test_delegator_answers_are_balanced` | 4 対 4 が崩れること |
| `test_delegator_answers_names_the_wrong_answers_measurement` | 2026-08-27 の 2 件の実測が消えること |
| `test_unsent_report_table_covers_every_spoken_kind` | (5) の表の行が欠けること |
| `test_unsent_report_names_the_stall_detection` | 172 と 144 の実測が消えること |
| `test_task_delegation_preamble_is_untouched_by_upward_silence` | 181 との切り分けが崩れること |

## 引き受けなかったもの

- **`orchestration` スキル（`~/.agents/skills/orchestration/SKILL.md`）は触らない。**
  このリポジトリの外にあり、このブランチではコミットできない。あちらの「依頼書の型」は
  executor 向けであって subcommander 向けではないため、今回の契約は
  `skills/atct/SKILL.md` だけで閉じる
- **ゴール 173 の取り下げは行わない。**他ゴールの管理は subcommander の役割外である。
  `atct_decision_ask` で人間に出す
