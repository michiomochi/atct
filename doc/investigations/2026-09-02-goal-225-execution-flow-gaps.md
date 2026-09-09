# Goal 225: execution-flow gaps investigation

## 結論

`doc/execution-flow.md` は目標状態であり、現在の実装はレビュー付きの
`goal` / `plan` / `task` フローを持っていない。確認できた主な未実装は
差分 0, 1, 2, 3, 4, 5, 7, 9, 10 である。差分 6 はイベント型と SSE の
一部が既にあるが、withdraw の publish 条件はこの調査の停止後に再確認が
必要である。差分 8 は MCP 名だけは実装済みで、内部 RPC 名は互換目的で
`task.declare` のままである。

### plan / review API の判定

**plan と review の handoff API は「未公開」ではなく、現時点では end-to-end
で未実装である。**既存の store 型・DB 表・daemon RPC route・MCP tool/schema
のいずれにも `plan_handoffs` または review 遷移の実装は確認できない。

- `internal/store/task_handoff.go` と `internal/store/goal_handoff.go` には
  request / receive / complete / report-amend だけがあり、review の列・型・
  メソッドはない。
- `internal/store/queries/task.sql` は同じ 4 系統の SQL だけで、plan 用 SQL
  や review request / receive / reject はない。`plan_handoffs` の migration
  もない。
- `internal/daemon/handler.go` は `handoff.*` と `goal.handoff.*` の route
  だけを持ち、`plan.*` / `*.review.*` route はない。
- `internal/mcpshim/tools.go` は `atct_handoff_*` と
  `atct_goal_handoff_*` を登録するが、`atct_plan_handoff_*`、review 系、
  `atct_goal_review_*` は登録しない。

人間向け `atct_goal_review_*` は MCP に公開しない設計（`Register` のコメント
どおり Web UI 所管）なので、MCP 非公開だけをもって欠落とは判定しない。
ただし、既存の decisions / HTTP API がその役割を代替しているかは未検証で、
別表を作るかどうかは目標文書自身の未解決事項である。

## 確認できた差分

### 0. レビュー状態・plan handoff・人間レビュー

**確認済みの gap:** store / SQL / daemon / MCP に対象 API と状態がない。
既存の `TaskHandoff` / `GoalHandoff` は 3 timestamp と request/complete
report だけで、作業側が完了報告を書いて handoff を閉じる形である。

読むべき実装とテスト:

- DB/model: `internal/store/migrations/0009_task_handoffs.sql`,
  `0013_goal_handoffs.sql`, `0014_handoff_reports.sql`,
  `0015_handoff_exclusivity.sql`, `0019_integer_agent_session_ids.sql`,
  `internal/store/task_handoff.go` の request/receive/complete メソッド、
  `internal/store/goal_handoff.go` の同名メソッド、
  `internal/store/queries/task.sql` の handoff SQL。
- API: `internal/daemon/handler.go` の `handoff.*` / `goal.handoff.*` case、
  `internal/mcpshim/tools.go` の handoff tool 登録。
- 通知: `internal/store/wakeup.go` の event 定数と event payload、
  `internal/httpapi/server.go` の `eventMatchesGoalID` / `eventProjectID`、
  `cmd/atct/watch.go` の `formatWatchDecision` と配送重複排除。
- テスト: `internal/store/task_handoff_test.go` の
  `TestTaskHandoffRequestReceiveAndComplete` / `TestTaskHandoffStatesRemainDistinct`、
  `internal/store/goal_handoff_test.go` の
  `TestGoalHandoffRequestReceiveAndComplete`、`internal/daemon/task_handoff_test.go`、
  `internal/daemon/goal_handoff_test.go`、`internal/daemon/web_test.go` の
  `TestHTTPHandlerMCPTaskHandoffRoutes` / `TestHTTPHandlerMCPGoalHandoffRoutes`、
  `internal/mcpshim/schema_test.go` の `TestHandoffToolsInjectAgentSessionID`、
  `internal/httpapi/server_test.go` の SSE filter tests。

推奨 work unit: まず migration / store state machine / payload の設計を決めて
実装する単位、次に daemon + MCP surface とその contract tests、最後に SSE /
watch 配送を独立単位にする。`goals.status=review` と人間 review の保存先は
実装前に subcommander が決める設計判断であり、この調査では決めない。

### 1. handoff 状態から task status を更新

**確認済みの gap:** `CompleteTaskHandoff` は handoff の completion timestamp
と report を書くだけで、`tasks.status` は `UpdateTask` / `task.update` が別に
書く。`internal/domain/status.go` には `todo`, `doing`, `done`, `dropped` しか
なく `review` がない。`internal/store/queries/task.sql` の handoff SQL と
`UpdateTaskStatus` も独立している。

読むべき実装とテスト:

- `internal/store/task_handoff.go` の `RequestTaskHandoff`,
  `ReceiveTaskHandoff`, `CompleteTaskHandoff`, `AmendTaskHandoffReport`。
- `internal/store/task.go` の `UpdateTask`, `updateTask`, `ClaimTask`。
- `internal/daemon/handler.go` の `task.update`, `handoff.request`,
  `handoff.receive`, `handoff.complete` case。
- `internal/domain/status.go` の `TaskStatus` / `ParseTaskStatus`。
- `internal/store/task_handoff_test.go` の request/receive/complete/state tests、
  `internal/store/task_test.go` の `UpdateTask` status/authorization tests、
  `internal/daemon/task_handoff_test.go` の RPC tests。

推奨 work unit: 5 遷移の transaction と status 更新を store 層で一つにし、
各遷移の正方向と既存 `task.update` 互換を同じテスト単位で検証する。review
status の名前と handoff の review state は差分 0 の設計結果に依存する。

### 2. receive で session key を確定

**確認済みの gap:** `ReceiveTaskHandoff` / `ReceiveGoalHandoff` は
`received_by` と `received_at` を更新するだけで、session key を確定しない。
MCP 側は `agentSessionIDHolder` の `sessionID.Get()` を各 tool から渡し、現行
skill は先に `atct_session_identify` を要求している。

読むべき実装とテスト:

- `internal/store/task_handoff.go:225` の `ReceiveTaskHandoff` と goal 側の
  `ReceiveGoalHandoff`、`internal/store/agent_session.go` の session 登録/照合。
- `internal/daemon/handler.go` の handoff receive route、
  `internal/mcpshim/tools.go` の `SessionIdentifyIn`、session holder、receive
  tool handler。
- `internal/store/agent_session_test.go`、`internal/mcpshim/schema_test.go` の
  `TestHandoffToolsInjectAgentSessionID`、`internal/daemon/goal_handoff_test.go`
  / `task_handoff_test.go` の receive tests。

推奨 work unit: 呼び手の key の供給源、identify の存廃、receive の transaction
境界を決めて store/daemon/MCP/tests を一緒に変更する。key を process metadata
から取るか request で渡すかは未解決の仮説であり、executor は選ばない。

### 3. receive が role を返す

**確認済みの gap:** receive route は `TaskHandoff` / `GoalHandoff` をそのまま
marshal し、role を含む response wrapper を作らない。現行手順は receive 後に
別の `atct_role` を呼ぶ。

読むべき実装とテスト:

- `internal/daemon/handler.go` の `handoff.receive` / `goal.handoff.receive`、
  `roleBoundaries` と role response 型。
- `internal/mcpshim/tools.go` の receive tool output schema と
  `callWithUnappliedDecisions`。
- `internal/store/task_handoff.go` / `goal_handoff.go` の return 型、
  `internal/mcpshim/schema_test.go` の handoff schema/response tests、
  `internal/daemon/task_handoff_test.go` / `goal_handoff_test.go`。

推奨 work unit: receive 成功 response の role/claim payload と MCP schema の
変更だけを独立単位にする。`atct_role` は診断用途で残すという目標文書の決定を
前提にする。

### 4. goal completion report の書き手を commander に移す

**確認済みの gap:** `internal/mcpshim/tools.go` は agent-facing
`atct_goal_complete` を登録し、handler には `goal.complete` route がある。
現行 skill/orchestration の completion path も subcommander が goal complete
を呼ぶ前提で、commander の review report と goals の 6 部 report が分離されて
いない。

読むべき実装とテスト:

- `internal/daemon/handler.go` の `goal.complete` と
  `authorizeGoalCompletion`、`internal/store/goal.go` の
  `CompleteGoalWithReport`。
- `internal/mcpshim/tools.go` の `GoalCompleteIn` / `atct_goal_complete`。
- `internal/daemon/goal_complete_guard_test.go:64-252`、
  `internal/store/goal_complete_test.go`、`internal/mcpshim/schema_test.go` の
  goal-complete schema cases。
- `skills/atct/SKILL.md` の goal delegation/completion、
  `/Users/masayoshi.michikawa@kanmu.co.jp/.agents/skills/orchestration/SKILL.md`
  の delegation/completion rules。

推奨 work unit: goal handoff review の受理者と `goal.complete` の認可を一つの
単位で変更し、別単位で human approval/merge の保存経路を検証する。どの role
がどの report を書くかは target 文書の決定を使い、executor は再設計しない。

### 5. 依頼書に隣接 goal を列挙しない

**確認済みの gap:** 現行 `skills/atct/SKILL.md` の goal delegation は、同じ
file を触る adjacent goal と所有境界を依頼書に書くよう要求している。target
文書は worktree 分離を根拠にこの要求を削除する。

読むべき実装・手順・テスト:

- `skills/atct/SKILL.md` の goal delegation request instructions、
  `/Users/masayoshi.michikawa@kanmu.co.jp/.agents/skills/orchestration/SKILL.md`
  の「依頼を書く前に影響範囲を洗う」および依頼書の 7 項目。
- `skills/start/SKILL.md` の worker launch flow。
- `tests/wrapper_test.bash` の goal/task handoff contract tests（`atct_*handoff*`
  と adjacent-goal 文言の検査）。

推奨 work unit: 手順文書と wrapper tests だけを変更する単位にし、worktree の
merge-main 手順を同じ変更に含めるかは設計側で決める。

### 6. withdraw 通知

**部分確認:** `internal/store/wakeup.go` には `EventGoalWithdrawn` と
`GoalWithdrawnEvent` があり、`internal/httpapi/server.go` の
`eventMatchesGoalID` / `eventProjectID` はその型を扱う。したがって target
文書が要求する新 review event の通知処理は別途必要である。一方、文書が指す
`WithdrawActiveGoal` の「open decision があるときだけ publish」という条件は、
この調査では関数本体を再読せず、**現状 gap としては未確定**とする。

読むべき実装とテスト:

- `internal/store/goal.go:636` の `WithdrawActiveGoal` と publish transaction、
  `internal/store/goal_withdraw_test.go` の withdrawal event tests。
- `internal/httpapi/server.go:1462` の `eventMatchesGoalID`、`:1485` の
  `eventProjectID`。
- `internal/httpapi/server_test.go` の
  `TestSSEGoalScopedStreamDeliversGoalWithdrawn` と
  `TestSSEProjectScopedStreamFiltersOtherProjectsWithdrawal`。

推奨 work unit: withdrawal transaction/event delivery と goal/project SSE filter
の回帰テストを一つにする。review/plan event の scope/filter/format は差分 0 の
配送 work unit に分ける。

### 7. commander の goal-scoped decision

**確認済みの gap:** `0001_baseline.sql:79` と
`0019_integer_agent_session_ids.sql:62` の CHECK は、open/answered の
`kind='decision'` に task id を要求する。`DecisionAskIn` は goal id と optional
task id を持つため、task を持たない commander の decision 経路がこの制約に
阻まれる。一方 completion/goal_approval はこの CHECK の対象外である。

読むべき実装とテスト:

- `internal/store/migrations/0001_baseline.sql:79`、
  `0019_integer_agent_session_ids.sql:62`、`internal/store/decision.go` の
  decision create/answer/apply 処理。
- `internal/daemon/handler.go` の `decision.ask`、
  `internal/mcpshim/tools.go` の `DecisionAskIn` / `atct_decision_ask`。
- `internal/store/decision_constraint_migration_test.go`、
  `internal/daemon/pending_response_test.go` の decision ask/update cases、
  `internal/mcpshim/schema_test.go` の decision schema cases。

推奨 work unit: constraint を緩める migration と commander authorization を
独立単位にする。baseline が task-only にした理由を確認してから、constraint
変更か goal-scoped 別経路かを subcommander が決める。

### 8. `atct_task_create` 名

**確認済み:** `internal/mcpshim/tools.go:584` は MCP 名を
`atct_task_create` として公開し、schema tests も同名を検査する。ただし実装
handler は互換性のため `task.declare`（`tools.go:597`, `handler.go:948`）を
呼ぶ。target 文書が MCP tool 名を指す限り、この差分は完了扱いでよい。

確認対象は `internal/mcpshim/schema_test.go` の `atct_task_create` cases と
`internal/daemon/handler_test.go` の `task.declare` idempotency tests。内部 RPC
名まで変更する要求が追加されるなら、後方互換を含む別 work unit にする。

### 9. task handoff tool の粒度付き命名

**確認済みの gap:** MCP は `atct_handoff_request/receive/complete/report_amend`
（`internal/mcpshim/tools.go:621-668`）、daemon RPC も `handoff.*`
（`internal/daemon/handler.go:1080-1150`）で、task を名前に含めない。store 型
は task-specific だが外部契約に反映されていない。

読むべき実装とテスト:

- `internal/mcpshim/tools.go` の `Handoff*In` と 4 tool registrations、
  `internal/daemon/handler.go` の generic handoff routes、
  `internal/store/task_handoff.go` / `internal/store/queries/task.sql`。
- `internal/mcpshim/schema_test.go` の generic handoff schema cases、
  `internal/daemon/task_handoff_test.go`、`internal/store/task_handoff_test.go`。
- `cmd/atct/main.go:801` の CLI `runHandoff` とその tests（CLI 名も影響するかを
  実装前に確認）。

推奨 work unit: MCP 名/RPC 名と deprecation 方針を一つの API rename 単位で
変更し、goal handoff と substring を誤検出しない schema/wrapper tests を追加
する。store の内部メソッド名を変える必要はない可能性がある。

### 10. 全 task を executor に渡す

**確認済みの gap:** `internal/store/task.go:522` の `ClaimTask` は自己 claim
用 handoff を作り、`internal/store/task_handoff.go` の
`requestTaskHandoffForClaim` と `internal/daemon/handler.go` の
`task.claim` が role による executor-only 制限を持たない。したがって
subcommander の自己実装を許す経路が残っている。文書にある直近 120 task の
26/12 件という DB 集計値は、この調査では再実行していない。

読むべき実装とテスト:

- `internal/store/task.go` の `DeclareTasks`, `ClaimTask`, `UpdateTask`、
  `internal/store/task_handoff.go` の `requestTaskHandoffForClaim` と
  `requireGoalHandoffForTask`。
- `internal/daemon/handler.go` の role boundaries と `task.claim`、
  `internal/mcpshim/tools.go` の task create/claim tools。
- `internal/store/task_test.go:913` の
  `TestClaimTaskUsesSelfHandoffWithoutWritingClaimedBy`、`:975` の
  `TestClaimGoalUsesSelfHandoffWithoutWritingClaimedBy`、
  `internal/store/task_claim_test.go`、`internal/store/task_handoff_test.go`、
  `cmd/atct/context.go` / `cmd/atct/delegated_task_display_test.go`。

推奨 work unit: self-claim と delegated handoff の ownership contract を整理し、
role guard と既存 task queue/display tests を一つの単位で変更する。実装を
executor に限定する具体的な認可方式は設計判断として subcommander に返す。

## 証拠コマンドと結果

以下はこの調査中に実行した検索の実結果である。レビュー後の追加探索は行って
いない。

1. `rg -l --hidden --glob '!.git' --glob '!doc/**' --glob '!*.md' 'atct_(plan_handoff|task_handoff|goal_handoff_review|goal_review)|atct_task_create|atct_goal_complete' .`
   - `internal/mcpshim/tools.go`, `internal/daemon/handler.go`,
     `internal/store/task_handoff.go`, `internal/store/goal_handoff.go`、既存の
     handoff test 群などを出力した。
   - **`internal/store/plan_handoff.go`、plan migration、review API の実装 path
     は出力されなかった。**出力された `plan` / `review` の多くは
     `doc/execution-flow.md`、skill、wrapper/test の契約文だった。
2. `rg -n '^(type (Handoff|Goal|Task|Plan)|func .*Handoff|func .*Plan|func .*Review)|Name: *"atct_|Method: *"(task|goal|plan|handoff)|handoff\.(request|receive|complete|report\.amend)|review' internal/mcpshim/tools.go internal/mcpshim/*.go internal/daemon/handler.go internal/daemon/*.go internal/store/*.go internal/httpapi/server.go`
   - MCP の登録は `atct_handoff_*`（`tools.go:621-668`）と
     `atct_goal_handoff_*`（`:670-717`）、daemon route は `handoff.*` と
     `goal.handoff.*`（`handler.go:1080-1240`）だった。plan/review の登録・route
     は見つからなかった。
3. `rg -n 'CREATE TABLE (task_handoffs|goal_handoffs|plan_handoffs)|task_handoffs|goal_handoffs|TaskStatus|type TaskStatus|status.*review|CHECK.*status|task\.declare|task\.update|goal\.complete|goal\.review|handoff\.review|plan' internal/store/migrations internal/store/queries internal/store/schema.go internal/domain internal/daemon internal/mcpshim --glob '*.sql' --glob '*.go'`
   - `internal/domain/status.go:17-20` は todo/doing/done/dropped のみ。
   - `internal/store/migrations/0019_integer_agent_session_ids.sql:62` は
     task_id 必須の decision CHECK。
   - `internal/store/queries/task.sql:193-254` は request/receive/complete/amend
     の handoff SQL のみで、plan_handoffs/review SQL はなかった。
4. `rg -n 'func \(s \*Store\) (UpdateTask|DeclareTasks|ClaimTask)|UpdateTask\(' internal/store/task.go internal/store/task_test.go internal/daemon/handler_test.go internal/mcpshim/schema_test.go`
   - `internal/store/task.go` の `DeclareTasks:46`, `UpdateTask:294`,
     `ClaimTask:522` と、既存の status/claim tests を出力した。handoff complete
     と task status 更新が別関数であることを確認した。

## 未検証・実装前に読むべきファイル

停止指示により、次は存在と参照先までに留め、関数本体・テスト結果の再検証を
していない。

- `internal/store/goal.go:636` (`WithdrawActiveGoal`) と
  `internal/store/goal_withdraw_test.go` — withdraw publish gate。
- `internal/store/agent_session.go` / `agent_session_test.go` — receive 時の key
  導出元。
- `internal/store/decision.go` — goal-scoped decision の既存認可と保存経路。
- `internal/store/migrations.go`、sqlc generated files — plan table migration が
  schema parity/generation に与える影響。
- `cmd/atct/watch_scope.go` と `cmd/atct/watch.go` — review event の scope と
  text formatter の追加位置。
- `internal/httpapi/server.go` / `internal/httpapi/server_test.go` の human
  approval endpoint — `atct_goal_review_*` を decisions で代替できるか。
- `internal/daemon/goal_complete_guard_test.go`、
  `internal/store/goal_complete_test.go`、`internal/mcpshim/schema_test.go` —
  commander-only completion に変えたときの既存契約。

## 検証境界

依頼の制約に従い、テスト・build・sqlite 集計は実行していない。実行を許可した
検証は検索と `git diff --check` のみであり、DB 実データの件数、未読の関数本体、
HTTP の human review 経路は「未検証」として扱う。
