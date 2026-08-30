# Codex / Claude plugin の hook 分離

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

Claude Code 専用の hook 定義を `hooks/claude-hooks.json` へ改名する。
`.claude-plugin/plugin.json` に
`"hooks": "./hooks/claude-hooks.json"` を明示し、Claude plugin がこの名前の
hook 定義だけを登録するようにする。

`.codex-plugin/plugin.json` から
`"hooks": "./hooks/hooks.json"` プロパティを削除する。Codex 用の hook は現在ないため、
空の `hooks/codex-hooks.json` は作らない。Codex 固有の hook が必要になった時点でだけ、
そのファイルを追加して Codex manifest から登録する。

3 つの実行可能 hook script は変更しない。Claude Code は改名後の hook 定義を読み、
次を維持する。

- `hooks/session-start` による `startup|clear|compact` の `SessionStart`
- `hooks/pre-ask` による `AskUserQuestion` の `PreToolUse`
- `hooks/stop` による `Stop`

Codex manifest に hook 登録がなければ、Codex が `${CLAUDE_PLUGIN_ROOT}` を含む
command を実行する経路はない。MCP、skills、metadata、version の登録は現状のままとする。

## 検討した案

1. **hook file を Claude 用として改名し、Codex manifest から hook 登録を外す
   （採用）。** Claude 専用であることをファイル名でも表し、Claude manifest が明示的に
   登録する。Codex には hook を登録しないため、Claude 専用 command は一切到達しない。
2. hook command に Codex 用の環境変数 fallback または wrapper を足す。Codex に
   Claude Code 用の lifecycle 挙動を実行させることになり、別の runtime contract を
   増やす。また SessionStart と PreToolUse の誤った登録も残る。
3. 共有 hook 定義から `Stop` を分離または削除する。観測済みの exit 127 だけは避けられるが、
   SessionStart／PreToolUse の誤った登録が残るか、Claude Code の Stop handoff 報告を壊す。

## 実装とテスト

実装では `hooks/hooks.json` を `hooks/claude-hooks.json` へ git rename し、
`.claude-plugin/plugin.json` にこの相対パスの `hooks` 登録を追加する。
`.codex-plugin/plugin.json` からは `hooks` 登録を削除する。`hooks/` 配下の script は
編集しない。

既存の plugin 静的契約テストである `tests/wrapper_test.bash` を拡張し、
`.codex-plugin/plugin.json` を JSON として読み、`hooks` プロパティがないことを
確認する。`.claude-plugin/plugin.json` は `./hooks/claude-hooks.json` を登録していることを
確認する。改名先の file に対する、report-only の Claude `Stop` command が正確に登録されて
いること、SessionStart と PreToolUse section があることの検査を移す。実行するコマンドは
次のとおり。

```sh
bash tests/wrapper_test.bash
```

テスト境界は宣言的である。このテスト群は Codex の plugin host 自体を再現しないが、
manifest が Claude hook file への経路を Codex に与えなくなったこと、Claude manifest が
改名先を登録すること、Claude の hook 定義が残ることは証明できる。

## 受け入れ条件

- `hooks/claude-hooks.json` が Claude 専用 hook 定義である。
- `.claude-plugin/plugin.json` が `hooks/claude-hooks.json` を登録する。
- `.codex-plugin/plugin.json` が `hooks` を宣言しない。
- `hooks/claude-hooks.json` が既存の Claude Code 用 SessionStart、PreToolUse、Stop entry と
  command を維持する。
- `bash tests/wrapper_test.bash` が両方の境界を検証する。
- ユーザー設定、dotfiles、ホーム／プロファイル、リリース状態、publish 操作を変更しない。
