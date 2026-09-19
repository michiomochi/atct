# Goal 282: legacy completion / no-session compatibility 調査

調査日: 2026-09-17、更新日: 2026-09-19

基準: `main` / `v0.63.11` (`5c726f9`) に rebase 済み。

## 結論

`KindCompletion` は named goal-review flow と独立した、まだ実行可能な goal 完了経路である。作成 API だけを消しても、既存の open `completion` decision は HTTP の approve で goal を `done` にできる。そのため、実装は (1) 新規 legacy request の fail-closed diagnostic、(2) open legacy decision の forward migration、(3) kind 固有の Store/HTTP/CLI/UI 消費者の削除、を同じ変更に含める必要がある。

`decisions.kind` は任意の `TEXT` で、`completion` を列挙する DB constraint はない。closed history を書き換える必要はない。一方、open row は旧 HTTP endpoint で完了可能なので、`withdrawn` へ移す forward migration が必要である。migration は理由を `answer_text` に残し、利用者を `goal_review` の再依頼へ導く。

## 現在の legacy call path

| 層 | 実装 | 挙動 |
| --- | --- | --- |
| MCP | `internal/mcpshim/tools.go:1076-1087` | `atct_goal_complete` が six-part report と現在の session ID を `goal.complete` に渡す。 |
| daemon | `internal/daemon/handler.go:2042-2084` | commander を認可し、`Store.CompleteGoalWithReport` を compatibility adapter として呼ぶ。 |
| Store | `internal/store/goal.go:426-512` | `CompleteGoal` → `CompleteGoalWithReport` → goal report 更新 → taskless `KindCompletion` open decision。 |
| HTTP | `internal/httpapi/server.go:1438-1481` | `KindCompletion` を `ApproveCompletion` / `RejectCompletion` に分岐する。approve は goal を done にする。 |
| rejection recovery | `internal/store/goal.go:984-1080` | `RejectCompletion` は旧 handoff を選び、必要なら新しい goal handoff を作成・受領する。現行 same-handoff goal-review rejection と矛盾する。 |
| CLI / wakeup | `cmd/atct/pending.go:30-32`, `internal/store/wakeup.go:259-286`, `cmd/atct/watch.go:1737-1738` | 完了 report 欠落を `atct_goal_complete` で解消するよう案内し、open completion を special-case する。 |
| UI | `web/src/lib/ui.ts:99-101`, `web/src/components/GoalDetail.tsx:91-194,598-604,689-696` | `completion` decision 用の approve/reject card を表示する。 |

主要な回帰対象は `internal/store/goal_complete_test.go`、`internal/store/goal_handoff_test.go`、`internal/daemon/goal_complete_guard_test.go`、`internal/e2e/flow_test.go`、`internal/e2e/full_flow_test.go`、`internal/httpapi/server_test.go`、`internal/mcpshim/schema_test.go`、`cmd/atct/pending_test.go`、`web/src/lib/ui.test.ts`、`web/src/components/GoalDetail.test.tsx` である。`doc/specs/` と `doc/plans/` の古い記録は履歴であり、事実を改変しない。現行運用文書・tool description は更新する。

## named flow と外部互換性

正規 flow は `atct_goal_handoff_review_request` → commander review → `atct_goal_review_request` → human approval → merge → `atct_goal_review_complete` である（`doc/execution-flow.md:54-171`、`internal/daemon/handler.go:1726-1775`）。`goal.review.complete` は report を受け取らず、approved `goal_review` と commander role を Store で検査する。

MCP 名 `atct_goal_complete` と RPC 名 `goal.complete` は以前の利用者に見える public surface である。互換性のために成功させ続けることは goal の完了条件に反する。両方を登録したまま、安定した migration diagnostic（`atct_goal_review_request` と `atct_goal_review_complete` へ移る指示）を返すのが最小の非破壊 migration である。Go Store の `CompleteGoal`、`CompleteGoalWithReport`、`FinalizeGoalWithReport`、`ApproveCompletion`、`RejectCompletion` は repository 内 caller を削除して除去できる internal adapter であり、保持しない。

HTTP `/api/decisions/{id}/approve|reject` は generic endpoint なので消さない。ただし `KindCompletion` branch を除去し、migrated open row は withdrawn になって endpoint の open guard で拒否される。closed legacy decision は読み取り表示・監査用に残す。UI は新規 completion card を描かず、historical kind は generic decision action としても扱わない。

## no-session fallback

MCP shim の holder は identify 前には 0 を返す（`internal/mcpshim/tools.go:334-349,619-627`）。role-authorized named operations は `authorizeRole` が 0 を拒否する（`internal/daemon/handler.go:378-381`）ため、`goal.complete` 自体は既に fail-closed である。

残る completion-adjacent no-session fallback は daemon の `completeTaskHandoff` と `completeGoalHandoff` にある（`internal/daemon/handler.go:830-862,865-900`）。`agent_session_id == 0` の場合、handoff ID が無ければ `*ForTask` / `*ForGoal` を呼び、ID があれば ownership を伴わない `Complete*Handoff` を呼ぶ。dispatch も authorization を `p.AgentSessionID != 0` のときだけにしている（`:1628-1633`, `:1718-1722`）。これは owner/role を確定せず completion を許す fallback であり、goal の no-session 完了条件と両立しない。

`ensureAgentSessionProject` の 0 short-circuit（`:319-322`）は上記 authorization を通る API に対して権限を与えない。task create の no-session scope test など、completion 以外の historical compatibility はこの goal の対象外とする。task/goal handoff completion の 0 input は、`agent_session_id is required; identify the session and use the named handoff review flow` と明示拒否し、Store adapter を呼ばない。

## データ migration

`internal/store/migrations/0043_runtime_heartbeat_lease.sql` が現在の末尾。新しい embedded migration `0044_retire_legacy_completion.sql` は次を transaction 内で行う。

```sql
UPDATE decisions
SET status = 'withdrawn',
    answer_text = 'legacy completion retired; request a named goal review',
    answered_at = COALESCE(answered_at, CURRENT_TIMESTAMP)
WHERE kind = 'completion' AND status = 'open';
```

`kind` に CHECK constraint はなく（`internal/store/migrations.go:461-485`）、列削除・table rebuild は不要である。fresh schema は data-only migration のため変更しない。過去の `completion` rows は audit history として保持する。migration test は legacy open/answered/applied rows を作り、open row だけ withdrawn となり goal status/report が変わらないことを検証する。completion-only query を削除するため、別途 sqlc output は再生成する。

## 推奨する実装境界

1. Store/domain/daemon と live Go consumer: legacy kind と adapters、session-0 completion fallback、HTTP/CLI/wakeup/E2E の legacy branches を一つの compile unit として削除する。RPC/MCP の旧 entry point は stable diagnostic を返す。
2. Migration: `0044` で open legacy row を withdraw し、fresh `schema.sql` は変更しない。
3. UI/docs: completion UI action と stale guidance を除き、named flow と migration behavior を記載する。
