# SessionStart で execution-flow を確認する（Goal 278・2026-09-19）

## 目的

ATCT の作業開始時に、Codex と Claude Code の両方へ role、handoff、plan review の正本で
ある `doc/execution-flow.md` を先に確認するよう明示する。開始時の指示だけを追加し、既存の
handoff 実装や文書そのものは変更しない。

## 調査結果

2026-09-19 時点の `main` は v0.63.11（`5c726f9`）であり、handoff が示した `9f519a8`
以降の変更はバージョン更新だけだった。対象コードは次の共有経路になっている。

- Codex: `.codex-plugin/plugin.json` → `hooks/codex-hooks.json` →
  `atct session-key --hook-input`。
- Claude Code: `.claude-plugin/plugin.json` → `hooks/claude-hooks.json` →
  `hooks/session-start` → `atct session-key --hook-input`。
- 両方の agent 向け文は `cmd/atct/session_key.go` の
  `sessionKeyMessageWithMonitorToken` が生成する。

したがって、共有 formatter を一度変更すれば両ハーネスへ同じ指示が届く。前回案の検証は
Codex 経路と formatter に偏っていたため、Claude の `hooks/session-start` を実行する契約テストも
受け入れ条件に含める。

## 検討した方式

1. **共有 formatter に固定文を追加する（採用）** — 既存の両経路を再利用し、文言の重複と
   harness ごとの差異を増やさない。
2. Codex と Claude の各 hook 定義へ個別に文言を追加する — 変更箇所と将来の不一致が増え、
   既存の共有 CLI 境界を弱める。
3. hook が `doc/execution-flow.md` を読み、内容や存在を検査する — 起動時 I/O と失敗条件を
   増やすが、今回必要なのは agent への確認指示だけである。

## 決定

`sessionKeyMessageWithMonitorToken` の既存の session key / monitor token 指示の末尾に、次の
固定文を必ず追加する。

> Before any ATCT work, read `doc/execution-flow.md` and follow its procedure.

monitor token の有無による二つの formatter 分岐は維持し、固定文は一つの共有定数から付加する。
Codex は `hooks/codex-hooks.json` の既存 command、Claude Code は `hooks/session-start` の既存
command を通り、どちらも同じ SessionStart 出力を得る。hook は文書を読まず、存在検査もしない。

## 受け入れ条件

- monitor token がある場合とない場合の両方で、出力末尾に上記 exact directive が一度だけ現れる。
- Codex の SessionStart command が共有 `session-key --hook-input` を呼び、directive を返す。
- Claude の `hooks/session-start` が共有 `session-key --hook-input` を呼び、directive を返す。
- CLI 未導入・旧版時の install/upgrade guidance、Claude hook の context 経路、既存 session key / monitor
  token 文言は変わらない。
- `go test ./cmd/atct`、`bash tests/wrapper_test.bash`、`git diff --check` が通る。

## 範囲外

- `doc/execution-flow.md`、dotfiles、daemon、MCP schema、role authorization の変更。
- hook によるファイル読取り・存在検査・文書内容の埋め込み。
- MCP instructions や role-specific skill の文言変更。必要になった場合は別 Goal のレビュー対象にする。
