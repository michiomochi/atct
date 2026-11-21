# execution-flow B1〜B4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** B1〜B4 の決定を `doc/execution-flow.md` の本文、図、現状との差に矛盾なく反映する。

**Architecture:** 実行フローの順序は保持する。B1/B4 は人間却下後の commander 再 handoff と状態なし待機を明文化し、B3 は25の handoff 要約と28の6部 report の責務を分離し、B2 は最大性の評価語を削除する。

**Tech Stack:** Markdown、Mermaid、ripgrep。

## Global Constraints

- 手順1〜29の順序と連番を変えない。
- `doc/execution-flow.md` だけを実装変更対象にする。
- Goal 216、launcher/shim/monitor、ユーザー設定、dotfiles、ホーム、README、Claude/Codex manifests・skills を変更しない。
- 既存の未コミット `doc/investigations/2026-08-31-execution-flow-b1-b4.md` は変更しない。

---

### Task 1: B1〜B4 を execution-flow へ反映する

**Files:**
- Modify: `doc/execution-flow.md:8-27,39-51,53-119,253-310,345-375,467-489,607-676`
- Test: `doc/execution-flow.md`

**Interfaces:**
- Consumes: `doc/specs/2026-09-01-execution-flow-b1-b4.md`
- Produces: B1〜B4 の決定と理由を本文・図・差分表で同じ意味にする文書。

- [ ] **Step 1: 設計をレビューする**

`doc/specs/2026-09-01-execution-flow-b1-b4.md` と、この plan を読む。B1=A、B2=最大性削除、B3=A、B4=A と矛盾する実装上の制約がないか確認する。制約があれば編集せず subcommander に差し戻す。

- [ ] **Step 2: B1/B4 の本文と図を更新する**

原則2に人間却下が外部判断による例外であること、claim 表に25後の claimなし、図・ゴール手順・人間レビュー節に commander の新 handoff と subcommander の手順5再受領を残す。25〜29は新状態を作らず、subcommander が再 handoff 待機以外をしないことを明記する。

- [ ] **Step 3: B3 の報告の責務を更新する**

25を「handoff受理・closureの要約報告」、28を「goals に保存する唯一の6部完成報告」とラベル付けする。両方の書き手が commander であり、二重入力ではないことを、人間レビュー節と完了報告理由にも反映する。

- [ ] **Step 4: B2 を更新する**

冒頭の「現状との最大の差」を削除する。差分0の「他のすべての前提」は review 基盤の依存関係を指し、責務移管との優劣ではないことを明確にする。

- [ ] **Step 5: 文書検証を実行する**

Run: `rg -n '最大の差|手順 25|手順 28|再 handoff|再発行|待機|6 部|6部' doc/execution-flow.md`

Expected: 「最大の差」がなく、25/28・再 handoff・待機・6部 report の説明が決定と一致する。

Run: `awk '/^[[:space:]]*[0-9]+\./ {sub(/^[[:space:]]*/, ""); sub(/\..*/, ""); print}' doc/execution-flow.md | sort -n | uniq -c`

Expected: 手順1〜29に欠番・重複がないことを確認できる。

Run: `git diff --check -- doc/execution-flow.md && git diff -- doc/execution-flow.md`

Expected: whitespace error がなく、変更が B1〜B4 に限定される。

- [ ] **Step 6: handoff を完了する**

`atct_handoff_complete` に変更内容、上記3検証の実出力、未検証事項、変更 path を記録し、その後 `atct_task_update(status="done")` を実行する。commit はしない。
