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
| スキル | `skills/start/SKILL.md` |
| 図 | `doc/execution-flow.md` |

## 据え置いた層と、その理由

**据え置きは手抜きではなく、改名の便益がエージェントに届かない一方で費用が実在する層である。**

- **DB 列 `tasks.declare_key`**：改名には移行が要る。ゴール 145 が移行 0023 を使用中で、
  同じ時期に `tasks` を作り直す移行を重ねる価値はない。この列は冪等キーの派生値であり、
  エージェントにも人間にも露出しない（web の task 詳細に出るラベルだけは残る）
- **daemon の RPC メソッド `task.declare`**：`atct` の CLI とデーモンの間だけの名前。
  改名すると、バイナリを更新したがデーモンを再起動していない環境でタスク作成が壊れる。
  エージェントから見える利得はゼロなので、リスクだけが残る
- **検知種別 `detection.undeclared_goal`**：`decisions.kind` に保存済みの値であり、
  改名には移行が要る。さらに分類表 `cmd/atct/watch_scope.go` はゴール 192 が保持している

## 非推奨エイリアス

`atct_task_declare` は同じハンドラに登録したまま残し、説明を
"Deprecated alias for atct_task_create. Call atct_task_create instead." にした。

理由は `skills/atct/SKILL.md` がゴール 146・192 に保持されていて触れないことである。
このスキルは旧名で呼べと書いており、稼働中のセッションもそれに従っている。エイリアスが
なければ、SKILL.md が更新されるまでの間、全エージェントのタスク作成が壊れる。

**外してよい条件：**

1. `skills/atct/SKILL.md` が `atct_task_create` を指すようになる
2. 同 SKILL.md の executor 禁止リストが `atct_task_declare` ではなく `atct_task_create`
   を名指しする（`tests/wrapper_test.bash` の許可リスト検査もそれに追随する）
3. 旧名で走り続けているセッションが残っていない

この 3 つが揃うまでは、tools/list に 1 件分（約 40 トークン）を払う。壊れたセッションを
復旧させる費用より安い。
