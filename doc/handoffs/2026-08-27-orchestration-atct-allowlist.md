# 依頼: `orchestration` スキルの ATCT 一括禁止を許可リストへ差し替える

宛先: dotfiles（`~/ghq/github.com/michiomochi/dotfiles`）を担当する作業単位。
発信元: atct のゴール 181（`atct-181-subcommander`）。2026-08-27。

**この依頼は `ai-config` スキルを通して実行すること。**対象がスキル本体なので、
`superpowers:writing-skills` にも従う（`reference/` は作らない）。

対象ファイルは 1 つだけ。

    ~/.claude/skills/orchestration/SKILL.md      （chezmoi 管理下の実体）

**atct 側（`skills/atct/SKILL.md` と `tests/wrapper_test.bash`）は既に直っている。**
この依頼はその対向側である。**片方だけでは矛盾が消えない。**

## なぜ

`skills/atct/SKILL.md` の `## Delegate a task` 手順 4 は、依頼書の冒頭に転記する文言として
executor へ ATCT 呼び出しを義務づけている。`orchestration` の `## 依頼書の型` 末尾は
禁止対象の一覧に `ATCT ツールの呼び出し` を挙げている。**一方は義務、他方は禁止で、
同じ 1 通の依頼書に両方は書けない。**

2026-08-27 に 3 件壊れた。

1. executor が衝突地点で作業を中断し、「報告方法の指示が更新されました」と述べて
   自前で handoff を作った。**依頼書を無視する形が正解になってしまっていた。**
2. subcommander が `atct_task_claim` を先に呼び、続く `atct_handoff_request` が
   `task handoff already open` で落ちた。`orchestration` が順序に触れていなかった。
3. **executor が subcommander の goal handoff を閉じ、subcommander の役割が executor に落ちた。**
   commander が再発行するまでコミットできず、その間ダッシュボードは「ゴール 144 は完了」と
   表示し、実体はコミット 0・タスク 4 件すべて todo だった。

**3 の原因は一括禁止そのものである。**粒度を持たない禁止は、覆されるときも粒度を持たずに覆る。
executor が「ATCT を呼んでよい」と解釈した瞬間、goal スコープの
`atct_goal_handoff_complete` にも手が届いた。

設計の全文は atct 側の `doc/specs/2026-08-27-what-an-executor-may-call.md` にある。

## 変更 1: `## 依頼書の型` 末尾の一括禁止から ATCT を外す

現在の末尾の段落（引用）:

> `chezmoi apply`・コミット・push・サブエージェント起動・**pane の作成と再委譲**・
> `git add -A`・**ATCT ツールの呼び出し**（handoff の枠を壊す）・**独断の権限昇格**（失敗したら
> 報告して返す）を禁じるなら、依頼書に明記する。**書かないと実行される。**

`**ATCT ツールの呼び出し**（handoff の枠を壊す）・` を**削る。**残りはそのまま。

削った直後に、次の内容の小節を新設する（見出しは `### executor の ATCT は一括禁止にしない`）。

- **一括で禁じるな。一括で義務づけるな。名指しの許可リストと名指しの禁止リストを依頼書に書く。**
- executor が呼んでよいのは次の 5 つだけ。**すべて自分の `task_id` の範囲に閉じている。**

  | ツール | いつ |
  |---|---|
  | `atct_session_identify` | 最初。他の atct 呼び出しより先 |
  | `atct_handoff_receive` | 着手前。`task_id` だけ渡す |
  | `atct_role` | `expected_role="executor"` |
  | `atct_task_update` | 作業が landed した時点で `done` |
  | `atct_handoff_complete` | 完了時。`task_id` と `complete_report` |

- 呼んではならないものを**名指しで**書く。**「上記以外」では足りない。**
  実測 3 の executor は、禁止されていると認識していなかった。

      goal スコープ    atct_goal_handoff_complete / atct_goal_handoff_receive /
                       atct_goal_handoff_request / atct_goal_claim / atct_goal_release /
                       atct_goal_complete / atct_goal_update_content
      project スコープ atct_project_claim / atct_project_release
      委譲と設計       atct_handoff_request（再委譲）/ atct_task_declare（設計）/
                       atct_decision_ask（人間への決定発行は subcommander の役割）
      claim            atct_task_claim（委譲されたタスクは誰も claim しない）

- **`atct_goal_handoff_complete` を呼ばれると、依頼を出した側の goal handoff が閉じ、
  その役割が executor に落ちる。**落ちたことは次に `atct_role` を呼ぶまで分からず、
  その間ダッシュボードはゴールを完了として表示する。**だから名指しで禁じる。**
- **executor が取り消せない操作に当たったら、実行せず依頼を出した相手へ返す。**
  `atct_decision_ask` を禁じた以上、executor には行き先が無い。**行き先を書かないと、
  禁止だけが残って executor が勝手に実行する。**取り消せない操作とは、履歴の書き換え /
  未コミット変更の破棄 / ファイル・ディレクトリの削除 / このマシンの外への publish。
  **設計判断も同じ経路である。**
- 手順の正典は `skills/atct/SKILL.md` の `## Delegate a task` である。
  **依頼書の冒頭にそこの引用ブロックを転記する。**

## 変更 2: `## 依頼と報告の作法` の報告経路を、完了とそれ以外に分ける

現在の行（引用）:

> - executor は完了時・判断を仰ぎたい時・ブロックされた時に、相手が読みに来るのを待たず
>   自分から依頼を出した相手へ報告する

これを 2 行に分ける。

- **完了報告は `atct_handoff_complete` の `complete_report` に書く。pane へ二重に送らない。**
- **ブロックされた時・判断を仰ぎたい時・依頼を差し戻す時は、依頼を出した相手へ返す**
  （相手が読みに来るのを待たない）。

**pane 経路そのものを廃止するわけではない。**完了報告だけを ATCT に寄せる。

完了報告を ATCT に寄せる理由は 3 つ。

1. **pane 報告に統一する案は、2026-08-27 の人間の指示と正面から衝突する。**
   「報告のみでも送ってくるな。報告されるだけでも commander のトークンが無駄になるため」。
   pane 報告は受け手のコンテキストに全文が載る。
2. **ATCT handoff は起こし方に依存しない。**`skills/atct/SKILL.md` の `## Delegate a task`
   冒頭が `keep the contract independent of how that worker is started` と書いているとおりで、
   pane で起きた executor もサブエージェントの executor も同じ経路を持つ。
   **そして Codex の executor は shell 経由で報告できない**（下の実測）。
3. **完了がダッシュボードに残る。**pane 報告だけだと、誰も閉じていないタスクが未着手に見える。
   実測 3 がその形である。

**`executor が使ってよい herdr コマンドは `herdr agent prompt <依頼を出した相手>` だけ` の行は
そのまま残す。**ブロック・質問・差し戻しの経路として要る。

### 実測: Codex の executor は shell 経由で報告できない（2026-08-27）

    codex-cli 0.148.0-alpha.21 / herdr 0.8.2
    ~/.codex/config.toml   sandbox_mode = "workspace-write" / approval_policy = "on-request"
    HERDR_SOCKET_PATH      ~/.config/herdr/herdr.sock

同じ pane で、シムの起動と socket 接続を分けて測った。

| コマンド | exit | 何に触るか |
|---|---|---|
| `herdr --version` | **0**（`herdr 0.8.2`） | シムのみ |
| `herdr --help` | **0** | シムのみ |
| `herdr agent get <name>` | **1** | socket |
| `herdr agent prompt <name> '...'` | **1** | socket |
| MCP: `atct_session_identify` / `atct_handoff_receive` / `atct_role` | 成功 | MCP |
| 対照: `printf ... > /tmp/...` | 0 | workspace 外 |

**aqua の `timestamp.txt` の警告は致命的ではない。**`--version` も `--help` も同じ警告を出して
exit 0 で返る。`Error: Os { ... }` は `agent` 系にだけ現れる。

**止めているのは socket である。**`HERDR_SOCKET_PATH` はホーム配下にあり、
`workspace-write` はそこへの書き込みを許さない。Unix socket への connect は
socket ファイルへの書き込み権限を要求するため、`herdr agent` はここで落ちる。

**`herdr` を `"$HERDR_BIN_PATH"` に置き換える回避策は効かない。**シムは元から通っている。

**したがって完了報告を pane に統一する案は、Codex の executor に対して実行不能である。**
禁止した側を選ぶと、その executor は報告手段を 1 つも持たない。

### 変更 2b: 完了報告と task を閉じる順序を書く

**`atct_handoff_complete` を先に呼び、`atct_task_update(status="done")` は後に呼ばせる。**
順序を逆にすると、`done` にした時点で open な task handoff が閉じ、`complete_report` が
`作業ロックを手放した（報告者なし）` で上書きされる（`internal/store/task.go:462-466`）。
executor はその後 `task handoff not found` で落ちる。

**実測（2026-08-27）**: 逆順を指示した結果、Claude の executor と Codex の executor の
2 台とも完了報告を失った。2 台とも指示どおりに動いた。
**これは atct 側の決定 455 として人間に出してある。**

## 変更 3: claim と handoff_request の順序を 1 行足す

`## 依頼書の型` の `### 依頼を書く前に影響範囲を洗う` の前に、次の内容を 1 行で入れる。

- **タスクを委譲するとき、`atct_task_claim` を先に呼ばない。**claim が open handoff を書くので、
  後続の `atct_handoff_request` が必ず落ちる。順序は `atct` スキルの `## Delegate a task` に従う。

**daemon 側の挙動そのもの（`atct_task_claim` が自己 handoff を書くこと）は atct のゴール 177 が
扱っている。ここでは触るな。**「順序は `atct` スキルに従う」と指す形にしておけば、177 が
挙動を変えても壊れない。

## 触らないもの

- `~/.claude/skills/orchestration/SKILL.md` の上記 3 箇所以外の節
- `~/.claude/skills/atct/`・`~/.agents/skills/` 配下（atct リポジトリが正典）
- atct リポジトリのファイルすべて

## 検証

```sh
f=~/.claude/skills/orchestration/SKILL.md

# 消えていること（0 件であること）
grep -c 'ATCT ツールの呼び出し' "$f"

# 入っていること
grep -n 'atct_session_identify' "$f"
grep -n 'atct_goal_handoff_complete' "$f"
grep -n 'atct_handoff_complete' "$f"
grep -n 'atct_task_claim' "$f"

# 順序が書いてあること
grep -n 'atct_handoff_complete' "$f" | head -1

# 残っていること（消してはいけない）
grep -n 'herdr agent prompt <依頼を出した相手>' "$f"
```

`chezmoi diff` を見てから `chezmoi apply` すること。

**新しくできるようになること**
1. 依頼書を書く側が、executor に許す atct 呼び出しを名前で得る
2. 依頼書を書く側が、禁じる呼び出しを名前で得る
3. 委譲時の claim と handoff_request の順序が `orchestration` からも引ける
4. 完了報告と task を閉じる順序が `orchestration` からも引ける
5. executor が取り消せない操作に当たったときの差し戻し先が書いてある

**壊れてはいけないこと**
1. `herdr agent prompt <依頼を出した相手>` が executor の使ってよいコマンドとして残っている
2. `## 依頼書の型` の禁止一覧から、ATCT 以外の項目（`chezmoi apply`・コミット・push・
   サブエージェント起動・pane の作成と再委譲・`git add -A`・独断の権限昇格）が消えていない
3. `## 役割の割り当て（変更するのはここだけ）` に手が入っていない
