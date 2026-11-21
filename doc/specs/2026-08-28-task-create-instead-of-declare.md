# `atct_task_declare` を `atct_task_create` にする

ゴール 200。「`atct_task_declare` という概念がわかりにくい」という指摘への答え。

## なぜ declare だったのか

`atct_task_declare` は設計 spec (`doc/specs/2026-08-15-atct-design.md`) と同じコミット
`1bdf2d8` で入った。spec はこのツールを「registers the decomposition of a goal」と説明する
だけで、**declare という語を選んだ理由はどこにも書かれていない。**

語の意図は spec ではなく運用側の文言に残っている。`skills/atct/SKILL.md` の見出し
"Declare before you work" が示すとおり、declare は「作業を始める前に、何をするかを人間に
見える形で表明する」という**約束の宣言**を指していた。

その意図自体は今も生きている。生きていないのは語のほうである。

- MCP のツール名は、他のツール（`atct_goal_complete`、`atct_task_claim`）と同じく
  **その呼び出しが DB に何をするか**で読まれる。この呼び出しがすることは行の作成であり、
  declare は動作を隠す
- 「先に表明する」という規範は、ツール名ではなく SKILL.md の見出しと本文が担っている。
  名前がその重荷を負う必要はない
- `declared: false`（既存タスクは更新されない）という応答フィールドは、declare を
  「宣言した／していない」と読むと意味が取れない。「作成された／既にあった」なら読める

よって改名する。**規範を消すのではなく、規範を SKILL.md に残したまま名前を動作に合わせる。**

## 改名した層（エージェントと人間に見える面）

| 場所 | 変更 |
|---|---|
| MCP ツール | `atct_task_declare` → `atct_task_create` |
| ツール説明 | "Declare tasks decomposed from a Goal" → "Create the tasks a Goal decomposes into" |
| 応答フィールド | `declared` → `created`（`domain.Task.Declared` → `Created`） |
| MCP サーバー指示 | `internal/mcpshim/instructions.go` |
| 次ツール提示 | `cmd/atct/context.go`、`cmd/atct/pending.go` |
| スキル | `skills/atct/SKILL.md`（3 箇所）、`skills/start/SKILL.md` |
| executor 禁止リスト | `skills/atct/SKILL.md` と `tests/wrapper_test.bash` |
| プラグイン説明 | `.claude-plugin/*.json`、`.codex-plugin/plugin.json` |
| 図 | `doc/execution-flow.md` |

## 旧名は残さない

初版では `atct_task_declare` を非推奨エイリアスとして残した。人間がそれを却下した
（decision 528: 「atct_task_declare は残さないで」）ので削除した。

削除にあたって、**壊れる範囲を推測ではなく測定した。**

### 測定（2026-08-28）

| 測ったもの | 結果 |
|---|---|
| MCP の経路 | プラグインの `.mcp.json` は `type: http` / `http://127.0.0.1:8787/mcp` |
| tools/list を出しているプロセス | `~/.atct/bin/atct-0.59.0 daemon`（1 プロセス、02:00 起動） |
| 実行中の `atct-mcp` プロセス | **0 個** |
| `~/.atct/bin/` にある `atct-mcp` バイナリ | 0.48.0 が最後。0.49.0 以降は 1 つも無い |
| 直近 2 時間に登録された agent session | 583（5 プロジェクト・active goal 18 本） |
| 現在エージェントに見えているツール名 | `atct_task_declare` のみ（この作業をしたセッション自身で確認） |

**ここから出る結論は、初版で書いた想定と違う。**

初版は「プラグインは版ごとのディレクトリに `skills/` と `bin/` を一緒に持ち、`bin/_resolve`
が `VERSION` 固定でバイナリを引くので、スキルとツール一覧は版で固定される」と読める構造を
前提にしていた。**その構造は実在するが、もう使われていない。**stdio の `atct-mcp` は起動して
おらず、ツール一覧は**全セッションで共有された 1 つのデーモン**が出している。

つまり**スキルとツール一覧は版で固定されない。**セッションが読む SKILL.md は自分のプラグイン
版のもの、見えるツール一覧は「今動いているデーモンのバイナリ」のものである。

### 残る窓と、その扱い

**壊れるのはコミット時ではなく、新しいバイナリでデーモンを再起動した瞬間である。**その時点で
旧版の SKILL.md を読んだまま走り続けているセッションは、存在しないツールを呼ぶ。

- **失敗はサイレントではない。**未知のツール名は MCP のエラーになり、エージェントは
  tools/list を見て `atct_task_create` を見つけられる。データは壊れない
- **代償は 1 セッションあたり呼び出し 1 回分**である
- **窓を閉じるのはリリース手順の仕事であり、このゴールの変更ではない。**公開は
  subcommander の役割の外なので、セッションを空にしてからリリースするかどうかは人間に返した

## 据え置いた層と、その理由

**据え置きは手抜きではなく、改名の便益がエージェントに届かない一方で費用が実在する層である。**

- **DB 列 `tasks.declare_key`**：改名には移行が要る。ゴール 145 が移行 0023 を使用中で、
  同じ時期に `tasks` を作り直す移行を重ねる価値はない。この列は冪等キーの派生値であり、
  エージェントにも人間にも露出しない（web の task 詳細に出るラベルだけは残る）
- **daemon の RPC メソッド `task.declare`**：`atct` の CLI とデーモンの間だけの名前。
  改名すると、バイナリを更新したがデーモンを再起動していない環境でタスク作成が壊れる。
  エージェントから見える利得はゼロなので、リスクだけが残る。**上の測定で「デーモンは 1 つ、
  長く生きている」ことが確かめられたので、この判断は初版より強く支持される**
- **検知種別 `detection.undeclared_goal`**：`decisions.kind` に保存済みの値であり、
  改名には移行が要る。さらに分類表 `cmd/atct/watch_scope.go` はゴール 192 が保持している

## 触らなかった `## Declare before you work`

節の見出しは残した。**規範としての declare（作業前に人間へ見える形にする）は生きており、
消したのは名前のほうだけだからである。**見出しはさらに `tests/wrapper_test.bash` が
`## Roles` からの範囲を切り出す区切りとして 4 箇所で使っており、ゴール 192 の
`doc/specs/2026-08-28-splitting-the-atct-skill-by-role.md` とゴール 194 の
`doc/specs/2026-08-28-numbering-the-ordered-steps.md` も名指ししている。**見出しを変えると
それらと衝突する。**本文の手順 1 に「Creating them is how you declare them」を足して、
規範と呼び出しの対応だけを明示した。

## このリポジトリの外に残っている旧名

`~/.claude/skills/orchestration/SKILL.md`（dotfiles 側）が executor 禁止リストで
`atct_task_declare` を名指ししている。`tests/wrapper_test.bash` はこれを検査するが、
**別リポジトリのファイルなので、このゴールでは直していない。**検査行も旧名のままにして
ある（現実と一致しているので緑）。dotfiles 側を新名にするときに、検査行も一緒に替える。
