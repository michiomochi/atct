# Codex plugin の hook 分離

日付: 2026-08-30
状態: 提案

## 問題

Codex plugin の manifest は現在 `hooks/hooks.json` を登録している。このファイルは
Claude Code 用の hook 定義であり、3 つすべての command が
`${CLAUDE_PLUGIN_ROOT}` を使う。Codex はこの変数を設定しないため、`Stop` command
の実行先は `/hooks/stop` となり、exit 127 で終わる。同じ登録により、Claude 専用の
SessionStart と PreToolUse command も Codex の hook 対象に入る。

この登録を所有するのはリポジトリである。ユーザー単位の Codex 設定・hook、dotfiles、
ホーム／プロファイル、リリース、publish はこの変更の対象外とする。

## 決定

`.codex-plugin/plugin.json` から
`"hooks": "./hooks/hooks.json"` プロパティだけを削除する。

`hooks/hooks.json` と 3 つの実行可能 hook script は変更しない。Claude Code は既存の
hook 定義を読み続け、次を維持する。

- `hooks/session-start` による `startup|clear|compact` の `SessionStart`
- `hooks/pre-ask` による `AskUserQuestion` の `PreToolUse`
- `hooks/stop` による `Stop`

Codex manifest に hook 登録がなければ、Codex が `${CLAUDE_PLUGIN_ROOT}` を含む
command を実行する経路はない。MCP、skills、metadata、version の登録は現状のままとする。

## 検討した案

1. **Codex manifest から hook 登録を外す（推奨）。** 1 プロパティだけを境界として
   変更するため、Claude 専用 command のすべてを Codex から外しつつ、共有の Claude
   hook file を保てる。
2. hook command に Codex 用の環境変数 fallback または wrapper を足す。Codex に
   Claude Code 用の lifecycle 挙動を実行させることになり、別の runtime contract を
   増やす。また SessionStart と PreToolUse の誤った登録も残る。
3. 共有 hook 定義から `Stop` を分離または削除する。観測済みの exit 127 だけは避けられるが、
   SessionStart／PreToolUse の誤った登録が残るか、Claude Code の Stop handoff 報告を壊す。

## 実装とテスト

実装で変更するのは `.codex-plugin/plugin.json` だけとする。`hooks/hooks.json` と
`hooks/` 配下の script は編集しない。

既存の plugin 静的契約テストである `tests/wrapper_test.bash` を拡張し、
`.codex-plugin/plugin.json` を JSON として読み、`hooks` プロパティがないことを
確認する。既存の `hooks/hooks.json` に対する、report-only の Claude `Stop` command が
正確に登録されていること、SessionStart と PreToolUse section があることの検査は維持する。
実行するコマンドは次のとおり。

```sh
bash tests/wrapper_test.bash
```

テスト境界は宣言的である。このテスト群は Codex の plugin host 自体を再現しないが、
manifest が Claude hook file への経路を Codex に与えなくなったことと、Claude の
hook 定義が残ることは証明できる。

## 受け入れ条件

- `.codex-plugin/plugin.json` が `hooks` を宣言しない。
- `hooks/hooks.json` が既存の Claude Code 用 SessionStart、PreToolUse、Stop entry と
  command を維持する。
- `bash tests/wrapper_test.bash` が両方の境界を検証する。
- ユーザー設定、dotfiles、ホーム／プロファイル、リリース状態、publish 操作を変更しない。
