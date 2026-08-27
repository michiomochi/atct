# executor が呼んでよい ATCT を決める

ゴール 181。2026-08-27。

## 何が起きているか

`skills/atct/SKILL.md` の `## Delegate a task` 手順 4 は、依頼書の冒頭に転記する文言として
executor へ 4 回の ATCT 呼び出しを義務づけている。dotfiles の `orchestration` スキル
`## 依頼書の型` 末尾は、禁止対象の一覧に `ATCT ツールの呼び出し`（handoff の枠を壊す）を
挙げている。**一方は義務、他方は禁止で、同じ 1 通の依頼書に両方は書けない。**

2026-08-27 に 3 件壊れた。

1. executor が衝突地点で作業を中断し、「報告方法の指示が更新されました」と述べて
   自前で handoff を作った。**依頼書を無視する形が正解になってしまっている。**
2. subcommander が `atct_task_claim` を先に呼び、続く `atct_handoff_request` が
   `task handoff already open` で落ちた。
3. **ゴール 144 で executor が subcommander の goal handoff を閉じ、subcommander の役割が
   executor に落ちた。**commander が再発行するまでコミットできず、その間ダッシュボードは
   「ゴール 144 は完了」と表示し、実体はコミット 0・タスク 4 件すべて todo だった。

## 決定

### D1. 完了報告は ATCT handoff に統一する。pane への完了報告は書かない

executor の完了報告は `atct_handoff_complete` で行う。`orchestration` の pane 報告は
完了報告の経路から外す。

理由:

- **pane 報告に統一する案は、2026-08-27 の人間の指示と正面から衝突する。**
  「報告のみでも送ってくるな。報告されるだけでも commander のトークンが無駄になるため」
  （ゴール 178）。pane 報告は受け手のコンテキストに全文が載る。
- **ATCT handoff は起こし方に依存しない。**`skills/atct/SKILL.md` の `## Delegate a task`
  冒頭が「keep the contract independent of how that worker is started」と書いているとおりで、
  pane で起きた executor もサブエージェントの executor も同じ経路を持つ。
- **完了がダッシュボードに残る。**pane 報告だけだと、`## Close a task the moment it is
  finished` が警告している「誰も閉じていないタスクは未着手に見える」がそのまま起きる。
  実測 3 がその形である。

**pane 経路そのものを廃止するわけではない。**ブロック・差し戻し・依頼への質問は、依頼を出した
相手へ返す。これは完了報告ではないので `atct_handoff_complete` に載らない。
**その経路をどう通すかはゴール 180 の担当であり、ここでは決めない。**この決定は「何を呼ばせるか」
だけを決めているので、180 が経路を herdr から別の形に寄せても壊れない。


### D1 の裏付け: Codex の executor は shell 経由で報告できない

**ゴール 180 が報告し、私が Codex の executor（task 716）で 2 回測り直した。**

    codex-cli 0.148.0-alpha.21 / herdr 0.8.2
    ~/.codex/config.toml   sandbox_mode = "workspace-write" / approval_policy = "on-request"
    HERDR_SOCKET_PATH      ~/.config/herdr/herdr.sock

**1 回目**は `herdr agent get` と `herdr agent prompt` が exit 1 で落ち、stderr に
aqua の `timestamp.txt を open: operation not permitted` が出ていた。
**そこから「aqua のシムが弾かれている」と読んだ。これは誤りだった。**

**2 回目**にシム起動と socket 接続を分けて測った。

| コマンド | exit | 何に触るか |
|---|---|---|
| `herdr --version` | **0**（`herdr 0.8.2` を返す） | シムのみ。socket に触らない |
| `herdr --help` | **0** | 同上 |
| `herdr agent get <name>` | **1** | socket |
| `herdr agent prompt <name> '...'` | **1** | socket |
| MCP: `atct_session_identify` / `atct_handoff_receive` / `atct_role` | 成功 | MCP。sandbox を通らない |
| 対照: `printf ... > /tmp/...` | 0 | workspace 外の書き込み |

**aqua の警告は致命的ではない。**`--version` と `--help` は同じ警告を出して exit 0 で返る。
`Error: Os { ... }` は `agent` 系にだけ現れる。

**止めているのは socket である。**`HERDR_SOCKET_PATH` は `~/.config/herdr/herdr.sock` で、
`workspace-write` はホーム配下への書き込みを許さない。**Unix socket への connect は
socket ファイルへの書き込み権限を要求するので、`herdr agent` はここで落ちる。**

**結論は変わらないが、原因は変わる。**

- **shell 経由の報告コマンドは Codex の executor から実行できない。**これは 2 回とも同じ
- **原因は aqua のシムではなく herdr の socket の位置である。**
  したがって `herdr` を `"$HERDR_BIN_PATH"` に置き換える回避策は効かない。
  シムは元から通っていた
- **MCP は sandbox を通らないので通る**

**pane 報告に統一する案は、Codex の executor に対して実行不能である。**
禁止した側を選ぶと、その executor は報告手段を 1 つも持たない。

**ただし D1 はこの実測だけに依存していない。**仮に shell 経由が通ったとしても、
D1 の理由 1（178 の指示との衝突）と理由 3（ダッシュボードに残る）は成立する。
**この実測は D1 を決めたのではなく、pane 案が技術的にも不可能であることを足しただけである。**

### D2. 禁止は一括ではなく、名指しの許可リストと名指しの禁止リストにする

**`ATCT ツールの呼び出し` という一括禁止が事故 3 の原因である。**粒度を持たない禁止は、
覆されるときも粒度を持たずに覆る。executor は「ATCT を呼んでよい」と解釈した瞬間、
goal スコープの `atct_goal_handoff_complete` にも手が届いた。

executor が呼んでよいのは次の 5 つだけである。すべて自分の `task_id` の範囲に閉じている。

| ツール | いつ |
|---|---|
| `atct_session_identify` | 最初。他の atct 呼び出しより先 |
| `atct_handoff_receive` | 着手前。`task_id` だけ渡す |
| `atct_role` | `expected_role="executor"` |
| `atct_task_update` | 作業が landed した時点で `done` |
| `atct_handoff_complete` | 完了時。`task_id` と `complete_report` |

executor が呼んではならないものを名指しで書く。**「上記以外」では足りない。**
実測 3 では、executor は禁止されていると認識していなかった。

- goal スコープ: `atct_goal_handoff_complete` / `atct_goal_handoff_receive` /
  `atct_goal_handoff_request` / `atct_goal_claim` / `atct_goal_release` /
  `atct_goal_complete` / `atct_goal_update_content`
- project スコープ: `atct_project_claim` / `atct_project_release`
- 委譲と設計: `atct_handoff_request`（再委譲）/ `atct_task_declare`（設計）/
  `atct_decision_ask`（人間への決定発行は subcommander の役割）
- `atct_task_claim`: 委譲されたタスクは誰も claim しない（`## Delegate a task` 手順 1）

### D2b. executor が取り消せない操作に当たったら委譲元へ返す

**`atct_decision_ask` を禁止リストに入れると、executor に行き先が無くなる。**
`## Act on reversible choices, ask about irreversible ones` は `atct_decision_ask` を呼ぶ側に
向けて書かれているので、それを禁じられた executor には適用できない。

**executor は実行せず、依頼を出した相手へ返す。**判断を持ち帰るのではなく、そこで止める。
`atct_decision_ask` は委譲元が呼ぶ。設計判断も同じ経路である
（役割表の `does not: make design decisions` と揃う）。

**これは commander の指摘で見つかった。**禁止リストを名指しにしたことで初めて
「では executor はどうするのか」が問える形になった。**一括禁止のままでは問いすら立たなかった。**

### D3. なぜ役割が落ちるかを `## Recover when your role comes back wrong` に書く

同節は復旧手順を持っているが、なぜ起きるかを書いていない。原因を書かないと、
禁止リストが「なぜ禁止なのか分からない規則」になり、実測 1 のように状況が変わったと判断した
executor に覆される。

書く内容: 役割は「受領済みで未完了の goal handoff を持つか」から導かれる。executor が
`atct_goal_handoff_complete` を呼ぶと subcommander の goal handoff が閉じ、subcommander の
役割は executor に落ちる。落ちたことは subcommander が次に `atct_role` を呼ぶまで分からず、
その間ダッシュボードはゴールを完了として表示する。

### D4. claim と handoff_request の順序を両文書で一致させる

`skills/atct/SKILL.md` は `## Delegate a task` 手順 1 で「委譲元はタスクを claim しない。
claim が open handoff を書くので、後続の handoff request は必ず拒否される」と書いている。
`orchestration` は順序に触れていない。**触れていないので実測 2 が起きた。**

`orchestration` に 1 行足して `atct` スキルを正典として指す。**daemon 側の挙動そのもの
（`atct_task_claim` が自己 handoff を書くこと）はゴール 177 の担当であり、ここでは触らない。**
177 が挙動を変えても、「順序は `atct` スキルに従う」という指し方は壊れない。

### D5. 完了報告を先に、タスクを閉じるのは後に

**`atct_task_update(status="done")` は open な task handoff を閉じ、`complete_report` を
`作業ロックを手放した（報告者なし）` で上書きする。**

    internal/store/task.go:462-466        終端化のとき CompleteTaskHandoff(..., taskHandoffReleasedReport)
    internal/store/task_handoff.go:25     taskHandoffReleasedReport = "作業ロックを手放した（報告者なし）"

**実測（2026-08-27・このゴール）**: 依頼書で「`atct_task_update(done)` →
`atct_handoff_complete`」の順を指示した結果、Claude の executor（task 713）と
Codex の executor（task 716）の**2 台とも完了報告を失った。**
2 台とも `task handoff not found: task <id>` で落ち、記録には「報告者なし」だけが残った。
**2 台とも指示どおりに動いた。**

引用ブロックの順序は **`atct_handoff_complete` が先、`atct_task_update(done)` が後**とする。

**実装側の穴はこのゴールでは直さない。**「順序を間違えると黙って報告が消える」ことは
文書の修正では消えないので、**決定 455 として人間に出した。**

## 検査に使う固定文字列

`tests/wrapper_test.bash` が grep する文字列を、ここで確定させる。

`skills/atct/SKILL.md` の `## Delegate a task` 節に必ず現れる:

    An executor may call only these atct tools:
    `atct_session_identify`, `atct_handoff_receive`, `atct_role`, `atct_task_update`, `atct_handoff_complete`.
    An executor must not call `atct_goal_handoff_complete`

`skills/atct/SKILL.md` の `## Delegate a task` 節に必ず現れる（D5）:

    Report completion before closing the task

`skills/atct/SKILL.md` の `## Delegate a task` 節に必ず現れる（D2b）:

    An executor that reaches an irreversible operation returns it to the delegator

`skills/atct/SKILL.md` の `## Recover when your role comes back wrong` 節に必ず現れる:

    Closing a subcommander's goal handoff drops that subcommander to `executor`

`~/.claude/skills/orchestration/SKILL.md` に**現れてはならない**:

    **ATCT ツールの呼び出し**（handoff の枠を壊す）

## 実測: この矛盾の中で依頼書を書いた

**このゴールの subcommander は、この矛盾を直しながら、同時に executor へ依頼を出す側だった。**
決定を書く前に、依頼書を 3 通書く必要があった。そこで起きたことを記録する。

### 一括禁止は、具体的な依頼書を 1 通書こうとした時点で書けない

依頼書の冒頭には受け手の起動手順が要る。`atct_handoff_receive` を呼ばせないと handoff が
受領されず、`atct_role` を呼ばせないと役割が検査されない。
**「ATCT ツールの呼び出しを禁じる」と書いた次の行に「まず `atct_session_identify` を呼べ」と
書くことはできない。**

D2 を決める前に、実際に手が書いたのは**許可 5 個と禁止 13 個の 2 つのリスト**だった。
**一括禁止は、抽象的な規則としてしか存在できない。**1 通の具体的な依頼書に落とすと、
必ず許可リストの形になる。**これが D2 の根拠である。**

### 衝突での停止は起きなかった。代わりに別の壊れ方をした

executor 3 台（Claude 2 台・Codex 1 台）に、許可リストと禁止リストを冒頭に置いた依頼書を出した。
**3 台とも衝突地点で停止しなかった。**ゴール 144 の executor が「報告方法の指示が
更新されました」と述べて止まった形は再現しなかった。

**代わりに、依頼を出した側（私）が順序を間違えた。**
`atct_task_update(done)` を `atct_handoff_complete` より前に置く指示を書き、
**Claude と Codex の 2 台の完了報告が消えた。**

**壊れ方が「2 文書が食い違うので受け手が止まる」から
「委譲元が順序を間違えて報告が黙って消える」へ移った。**
前者は文書で直る。後者は文書では直らないので、決定 455 として人間に出した。

### 名指しに変えた検査が、名指しの位置を見ていなかった

**最初に書いた検査は、リストではなく節全体を見ていた。**

    for tool in atct_session_identify ... atct_handoff_complete; do
      grep -Fq -- "$tool" <<<"$(delegate_task_section)" || fail ...
    done

**節の「どこかに」名前があれば通る。**許可リストの 5 つは手順 4 の引用ブロックにも出てくるので、
**許可リストから消しても通った。**commander が壊して見つけた。

リスト側から消したとき節の他所に残るか（残る = 穴）を数えると:

    許可 5 個   5/5 が穴
    禁止 13 個  3/13 が穴   atct_goal_claim / atct_handoff_request / atct_decision_ask

**これは D2 と同じ形の緩さである。**「包括的な禁止は根拠を持たないので覆される」と書いて
名指しに変えたのに、その名指しを守る検査が**名指しの位置**を見ていなかった。
**粒度を上げた文書を、粒度の低い検査で守っていた。**

直した形は 3 点。

1. **節ではなくリストそのものを取り出す。**許可リストは見出し行の次の 1 行、
   禁止リストは `An executor must not call` から `Spell the names out` までのブロック
2. **2 つのリストが交わらないことを測る。**許可リストに禁止対象が混ざっても、
   禁止ブロックに許可対象が混ざっても落ちる
3. **抽出が空なら落とす。**見出しが動くと抽出が空になり、全部が空振りで通る。
   **常に通る検査は穴と同じである**

**壊して落ちることを測るときは、「何も壊さない」場合も測る。**片方だけでは、
常に落ちる検査と区別がつかない。

## 完了条件との対応

| 条件 | 対応 |
|---|---|
| 1 統一先と理由 | D1 |
| 2 負けた側の記述が消えている | D2（一括禁止の削除）・タスク 715 |
| 3 並べて読んで矛盾が無い | 上の固定文字列を grep |
| 4 claim と handoff_request の順序が一致 | D4 |
| 5 (2) を壊すと落ちる検査 | タスク 714 |
| 6 dotfiles 側が渡っている | タスク 715（`doc/handoffs/2026-08-27-orchestration-atct-allowlist.md`） |

**このゴールで直さないもの**

| 見つけたもの | 出した先 |
|---|---|
| `atct_task_update(done)` が完了報告を黙って消す（`internal/store/task.go:462-466`） | 決定 455。新ゴールの起票を人間に聞いている |
| `atct_task_claim` が自己 handoff を書き、委譲手順を実行不能にする | ゴール 177（既存）。触っていない |
| executor の報告経路が物理的に無い（herdr のシム） | ゴール 180（稼働中）。本ゴールは「何を呼ばせるか」だけを決め、経路の修理には触っていない |
