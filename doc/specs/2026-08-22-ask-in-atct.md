# 判断をチャットで仰げないようにする

2026-08-22。人間の要求「atct を使ってる間は AI チャット上で判断を仰がず、atct 上で
判断を仰ぐことをルールにしたい」（ゴール `fa888894`）に対する設計。

## なぜ文書に書かないのか

`ai-config` の配置判定を通した結果、**この規則は文書に書いてはいけない**という結論になった。

判定の二問:

1. **守られなかったとき実害が出るか → Yes。**チャットで聞いた質問はダッシュボードに
   届かず、記録が残らず、既定値を持たず、答えが来るまで他の作業を止める。
   実測: 2026-08-21 に commander が `AskUserQuestion` で 4 回判断を仰いだ
   （リリース 2 回、回答待ちの表示、ボタンの対比）
2. 判定表の指示は「Yes なら強制層に置く。**文書には書かない**」

**すでに文書には書いてあった。**atct スキルの "Ask here, not in conversation" に
「`atct_decision_ask` is the only place a question belongs. Saying "let me know how you
want to proceed" in the conversation and then waiting is not asking — it is stopping.」
とある。**読んだうえで 4 回破った。**書き足しても同じことが起きる。

補強する事実（公式ドキュメント）: 指示ファイルは context であって強制設定ではない。
"To block an action regardless of what Claude decides, use a PreToolUse hook instead."

## なぜ atct に置くのか

当初 commander は「このルールは Claude Code の `AskUserQuestion` を前提にするので、
atct のリポジトリには置けない（公開物になる）」と書いた。**これは誤りだった。**

置き場の判定基準は「公開物か、この環境の設定か」である
（`doc/specs/2026-08-20-delegation-guard.md` で確定した基準）。

- `AskUserQuestion` は **Claude Code の組み込みツール**であって、この環境固有の何かでは
  ない。atct を使う誰の環境にも存在する
- 「atct がこのリポジトリを管理しているか」は **atct 自身が判定できる**
- **herdr に依存しない。**削除した `claim-before-delegate` が atct から出ていったのは、
  あれが `herdr agent prompt` を見ていたからである。この規則は herdr を見ない

したがって atct の plugin が配る。既に `session-start` と `stop` を配っている場所に
`PreToolUse` を足すだけである。

## 決定事項

### 1. 機構

`plugin/hooks/hooks.json` に `PreToolUse` を足し、`matcher` を `AskUserQuestion` にする。
スクリプトは `plugin/hooks/pre-ask`。既存 2 本と同じ書き方（`${CLAUDE_PLUGIN_ROOT}`、
`shell: bash`、`async: false`）にそろえる。

### 2. 出力の形（公式ドキュメントで確認済み）

**exit 2 ではなく、exit 0 と JSON を使う。**理由を構造化して渡せる。

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "..."
  }
}
```

入力は stdin の JSON で、`tool_name` と `cwd` が入っている。

### 3. 止める条件

**atct がこの作業ディレクトリを管理しているときだけ止める。**判定は `atct context` の
出力が空でないこと。

`atct context` は DB を直接開き（daemon を必要としない）、プロジェクトが未登録なら
空の結果を返して exit 0 する（`cmd/atct/context.go` の `ErrProjectNotFound` の枝）。
`stop` フックが `atct pending` で使っているのと同じ形である。

**止めてはいけない場合（すべて exit 0 で素通し）**:

- `atct` の実体が見つからない
- `atct context` が失敗する
- `atct context` の出力が空（プロジェクトが未登録）
- `tool_name` が `AskUserQuestion` でない

### 4. 文面

**禁止で終わらせない。**代わりに何をするかを書く。最低限これを含める:

- `atct_decision_ask` を使うこと
- 選択肢を 2 つ以上、それぞれに帰結を付けること
- 取り消せる選択なら先に実行して `wait_ms=0` で記録し、止まらないこと
- 取り消せない選択なら `default_option` を付けずに待つこと

### 5. 例外を作らない

`AskUserQuestion` は atct が管理するリポジトリでは常に止める。「原則として」
「必要に応じて」は書かない（免除の判断を読み手に委ねる形になり、規則が無効化される）。

## 検証（実装で必ず両方やる）

**片側だけのテストは通り抜けを許す。**

1. **止まること**: atct が管理するディレクトリで `tool_name=AskUserQuestion` を渡すと
   `permissionDecision: deny` が出る
2. **止まらないこと（4 通りすべて）**: 未登録のディレクトリ / `atct` が無い /
   `atct context` が失敗する / `tool_name` が別のツール

`tests/` に bash のテストがある（`tests/wrapper_test.bash`）。同じ形にそろえる。

## 残る懸念

この規則は commander が `AskUserQuestion` を呼べなくする。**`AskUserQuestion` には
atct の決定に無い機能がある**（選択肢に preview を付けて見比べる形）。UI の案を 2 つ
見比べてもらう用途では劣化する。それでも例外を作らないのは、例外の判定を呼ぶ側に
委ねると規則が効かなくなるからである。必要になったら atct の決定に同じ機能を足す。
