# Goal 286: 空の goal space で初回 executor pane が作れない Implementation Plan

> **For ATCT workers:** この plan は recorded task handoff から実行する。worker は実装後に task review を依頼し、subcommander がレビューして task を閉じる。`skills/atct/SKILL.md`、`tests/wrapper_test.bash`、`internal/mcpshim/instructions.go` は変更しない。

**Goal:** executor が 0 台の goal space でも、subcommander が初回 pane を用意して task handoff を受領可能にする。

**Architecture:** 実行場所の選択を「既存 idle executor の再利用」と「空の space の初回 bootstrap」に分ける文面を、subcommander skill と execution-flow の正典へ追加する。daemon や MCP の状態機械は変更せず、pane の具体的な配置は orchestration skill に委ねる。

**Tech Stack:** Markdown、既存の Bash skill-contract test、Git。

## Global Constraints

- 設計の基準は current main `9f519a8`。現在の worktree が古いことを理由に main へ merge/rebase しない。
- Goal 284 が所有する `skills/atct/SKILL.md` と `tests/wrapper_test.bash` は変更しない。
- Goal 290 が所有する `internal/mcpshim/instructions.go` と token-compression の変更には触れない。
- Goal 292 が所有する unreceived-handoff runtime guard を重複実装しない。
- pane/space の具体的な CLI は orchestration skill に従い、ATCT の handoff order は `request → monitored worker 起動 → receive` を維持する。
- worker は `atct codex monitor -- --model gpt-5.6-luna -c model_reasoning_effort=max` で起動される。

---

### Task 1: 初回 executor bootstrap の guidance を正典へ追加

**Files:**
- Modify: `skills/subcommander/SKILL.md`（役割概要の直後に bootstrap 規則を追加）
- Modify: `doc/execution-flow.md`（subcommander の task delegation 手順に同じ分岐を追加）
- Test: 既存の `tests/wrapper_test.bash` は読み取り専用で実行する。Goal 284 所有のため変更しない。

**Interfaces:**
- Consumes: orchestration skill の同一 space 内 pane 配置、既存の task handoff request/receive 契約。
- Produces: 「executor 0 台なら初回 pane を作る」「idle executor があれば再利用」「追加 pane は既存の4条件のみ」という同一の operator contract。

- [ ] **Step 1: current main の文面と stale worktree の差分を確認する**

```bash
git show 9f519a8:skills/subcommander/SKILL.md | grep -n -E 'reuse an idle executor|Create a new one only|## Delegate a task'
git show 9f519a8:doc/execution-flow.md | sed -n '149,170p'
git status --short
```

Expected: current main に再利用・追加 pane の記述があり、空の space の初回 bootstrap の明記がなく、作業ツリーはこの task の変更以外は clean である。

- [ ] **Step 2: subcommander skill に初回 bootstrap の優先規則を追加する**

役割概要の3つの箇条書きの直後に、次の意味を持つ短い節を追加する。

```markdown
## Executor workspace bootstrap

Before applying the reuse rule in `## Delegate a task`, classify the goal space:

- If the space has no executor pane, create the first executor pane in that same space. This is the required bootstrap path, not an exception to the additional-pane rule.
- If an idle executor exists, reuse it for the next unassigned task.
- Only after an executor exists may an additional pane be created, and only for parallel work, worktree isolation, context exhaustion, or a topic change.

After the pane is prepared, request the task handoff before starting the monitored worker. The worker must receive the recorded handoff before it starts implementation.
```

The existing detailed delegation contract remains intact; this section only makes its empty-space precedence explicit.

- [ ] **Step 3: execution-flow の subcommander 手順を同じ状態分岐に揃える**

In the `### subcommander` section, extend step 5 so it states all of the following in this order: prepare the first pane in the same space when none exists; reuse an idle executor when one exists; limit additional panes to the four existing conditions; then record the task handoff and start the monitored worker. Keep the existing step that says the worker receives before implementation.

The resulting paragraph must contain these exact phrases so the state distinction cannot be read as a blanket ban:

```text
executor pane が無ければ、同じ space に最初の 1 台を用意する
初回 pane は追加 pane の例外条件には含めない
idle executor があれば再利用する
task handoff を記録してから executor を起動する
```

- [ ] **Step 4: run the focused documentation checks**

Run the direct bootstrap assertions:

```bash
skill=skills/subcommander/SKILL.md
flow=doc/execution-flow.md
grep -Fq 'If the space has no executor pane, create the first executor pane in that same space.' "$skill"
grep -Fq 'If an idle executor exists, reuse it for the next unassigned task.' "$skill"
grep -Fq 'Only after an executor exists may an additional pane be created' "$skill"
grep -Fq 'executor pane が無ければ、同じ space に最初の 1 台を用意する' "$flow"
grep -Fq 'task handoff を記録してから executor を起動する' "$flow"
git diff --check
```

Run the existing in-repository contract test while skipping only its unrelated external dotfiles check:

```bash
ORCHESTRATION_SKILL_PATH=/private/tmp/atct-no-orchestration-skill bash tests/wrapper_test.bash
```

Expected: the direct assertions and `git diff --check` exit 0; the wrapper test reports its external orchestration check as skipped and exits 0. A plain `bash tests/wrapper_test.bash` is known to fail before this task because the local external `.claude/skills/orchestration/SKILL.md` is missing the unrelated `atct_session_identify` contract.

- [ ] **Step 5: inspect the final diff and leave it for subcommander review**

```bash
git diff -- skills/subcommander/SKILL.md doc/execution-flow.md
git diff --check
git status --short
```

The diff must contain only the two instruction files. Do not commit from the executor role; after the task handoff review is accepted, the subcommander stages these paths explicitly with the spec and plan and commits the goal's work.

## Plan self-review

- Spec coverage: the single task covers the empty-space branch, worker launch order, existing reuse conditions, scope boundaries, and focused verification.
- Placeholder scan: no unresolved placeholders or deferred implementation steps are present.
- Type/wording consistency: the skill and execution-flow use the same three-way state split and retain the same four additional-pane conditions.
