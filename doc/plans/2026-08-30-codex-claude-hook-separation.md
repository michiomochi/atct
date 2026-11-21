# Codex / Claude Hook Separation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Codex が Claude 専用 Stop hook を実行して exit 127 になる経路をなくし、Claude の既存 3 hook を維持する。

**Architecture:** Claude 専用の hook 定義は `hooks/claude-hooks.json` という名前にし、`.claude-plugin/plugin.json` の `hooks` フィールドで明示的に登録する。Codex manifest は hook を登録しない。既存の Bash テストを契約テストとして拡張し、Claude の custom path と Codex の非登録を同時に検証する。

**Tech Stack:** JSON plugin manifests、Bash、Python 3 標準ライブラリの `json`、既存の Bash テスト harness。

## Global Constraints

- 実装対象は repository 内の plugin 定義、hook 定義、既存静的テストだけとする。
- `.claude-plugin/plugin.json` は `"hooks": "./hooks/claude-hooks.json"` を明示する。
- `.codex-plugin/plugin.json` は `hooks` プロパティを持たない。
- `hooks/claude-hooks.json` は現在の SessionStart、PreToolUse、Stop command を内容変更なしで保持する。
- `hooks/session-start`、`hooks/pre-ask`、`hooks/stop` は変更しない。
- ユーザーの Codex / Claude 設定、dotfiles、ホーム／プロファイル、release、publish は変更しない。
- 履歴を記述した既存の `doc/specs/` は当時のファイル名を記録しているため変更しない。

---

### Task 1: Plugin hook registration and static contracts

**Files:**
- Create: `hooks/claude-hooks.json`（現在の `hooks/hooks.json` と byte-for-byte 同一の JSON）
- Delete: `hooks/hooks.json`
- Modify: `.claude-plugin/plugin.json`
- Modify: `.codex-plugin/plugin.json`
- Modify: `tests/wrapper_test.bash`
- Modify: `tests/pre_ask_test.bash`

**Interfaces:**
- Consumes: Claude Code plugin manifest の `hooks` string path、Codex plugin manifest の component 登録、既存 hook JSON の `hooks` object。
- Produces: Claude だけが `./hooks/claude-hooks.json` をロードし、Codex は hook file をロードしない plugin contract。

- [ ] **Step 1: Write the failing static-contract tests**

`tests/wrapper_test.bash` の `test_static_contract` に、両 manifest を Python 3 の JSON parser で検査する block を追加する。以下と同じ値で失敗させる。

```bash
if ! python3 - "$REPO_ROOT/.claude-plugin/plugin.json" "$REPO_ROOT/.codex-plugin/plugin.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    claude = json.load(stream)
with open(sys.argv[2], encoding="utf-8") as stream:
    codex = json.load(stream)

if claude.get("hooks") != "./hooks/claude-hooks.json":
    raise SystemExit(f"Claude hooks path = {claude.get('hooks')!r}")
if "hooks" in codex:
    raise SystemExit(f"Codex must not register hooks: {codex['hooks']!r}")
PY
then
  fail 'plugin manifests must register Claude hooks only'
fi
```

`tests/wrapper_test.bash` の `test_stop_hook_only_reports` と
`test_hooks_json_keeps_session_start_and_pre_tool_use_sections` が読む path を、どちらも
`$REPO_ROOT/hooks/claude-hooks.json` に置き換える。失敗メッセージも `claude-hooks.json`
を指すものに更新する。

`tests/pre_ask_test.bash` の `test_managed_ask_is_denied` にある 2 つの
`$REPO_ROOT/hooks/hooks.json` 参照を、`$REPO_ROOT/hooks/claude-hooks.json` に置き換える。

- [ ] **Step 2: Run the changed tests to verify they fail**

Run: `bash tests/wrapper_test.bash`

Expected: FAIL。Claude manifest に `hooks` が無く、`hooks/claude-hooks.json` がまだ無いため、
新しい manifest contract または hook JSON の file open が失敗する。

Run: `bash tests/pre_ask_test.bash`

Expected: FAIL。`hooks/claude-hooks.json` がまだ無いため、AskUserQuestion 用 PreToolUse entry の
静的検査が失敗する。

- [ ] **Step 3: Add the Claude-specific hook registration and remove the Codex registration**

`hooks/hooks.json` の JSON content を変更せず `hooks/claude-hooks.json` として作成し、
旧 path を削除する。

`.claude-plugin/plugin.json` の `version` の直後に次の property を追加する。

```json
"hooks": "./hooks/claude-hooks.json",
```

`.codex-plugin/plugin.json` から次の 1 property を削除する。

```json
"hooks": "./hooks/hooks.json",
```

この task で `hooks/session-start`、`hooks/pre-ask`、`hooks/stop`、`.mcp.json`、
ユーザー設定、dotfiles を編集しない。Codex 用 hook は不要なので空の JSON file を追加しない。

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `bash tests/wrapper_test.bash`

Expected: PASS。Claude manifest の custom path、Codex manifest の hooks 非登録、改名後の
SessionStart / PreToolUse / report-only Stop definition が検証される。

Run: `bash tests/pre_ask_test.bash`

Expected: PASS。`AskUserQuestion` に対する PreToolUse entry と既存の deny behavior が検証される。

- [ ] **Step 5: Commit the implementation**

```bash
git add .claude-plugin/plugin.json .codex-plugin/plugin.json hooks/claude-hooks.json hooks/hooks.json tests/wrapper_test.bash tests/pre_ask_test.bash
git commit -m "fix: separate Claude plugin hooks from Codex" -- .claude-plugin/plugin.json .codex-plugin/plugin.json hooks/claude-hooks.json hooks/hooks.json tests/wrapper_test.bash tests/pre_ask_test.bash
```

### Task 2: Final boundary review

**Files:**
- Review: `.claude-plugin/plugin.json`
- Review: `.codex-plugin/plugin.json`
- Review: `hooks/claude-hooks.json`
- Review: `tests/wrapper_test.bash`
- Review: `tests/pre_ask_test.bash`

**Interfaces:**
- Consumes: Task 1 の commit と 2 つの focused test result。
- Produces: Codex が Claude hook を実行できず、Claude hook の 3 event が維持されることの検収記録。

- [ ] **Step 1: Inspect the final plugin boundary**

Run:

```bash
python3 - <<'PY'
import json

with open('.claude-plugin/plugin.json', encoding='utf-8') as stream:
    claude = json.load(stream)
with open('.codex-plugin/plugin.json', encoding='utf-8') as stream:
    codex = json.load(stream)
with open('hooks/claude-hooks.json', encoding='utf-8') as stream:
    hooks = json.load(stream)['hooks']

assert claude['hooks'] == './hooks/claude-hooks.json'
assert 'hooks' not in codex
assert set(hooks) == {'SessionStart', 'PreToolUse', 'Stop'}
PY
```

Expected: exit 0。Claude だけが custom hook path を持ち、Codex に hook 登録はなく、Claude hook
JSON は 3 event を保持する。

- [ ] **Step 2: Re-run the complete focused verification set**

Run:

```bash
bash tests/wrapper_test.bash
bash tests/pre_ask_test.bash
```

Expected: 両方 exit 0。

- [ ] **Step 3: Record completion with the implementation commit**

`git log -1 --format=%H` の実出力を task と goal の complete report に記録する。report には
Claude hook custom path、Codex hooks 非登録、実行した 2 test command、変更しなかった
ユーザー環境・dotfiles・release・publish を含める。
