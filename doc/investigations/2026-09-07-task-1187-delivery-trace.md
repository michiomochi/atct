# Task #1187: Goal249/Goal251 通知配送の調査

## Provenance

- evidence collection: executor session `5838` / original handoff `goal-251-trace-delivery-20260907`
- first retry report attempt: executor session `5840` / retry handoff `goal-251-trace-delivery-retry-20260907`
- corrected retry report / canonical retry receiver: executor session `5838` / retry handoff `goal-251-trace-delivery-retry-20260907`

## 調査境界と受領確認

- task: `1187`
- handoff: `goal-251-trace-delivery-20260907`
- 発信元: `atct-251-subcommander`
- evidence collection session/handoff: `5838` / `goal-251-trace-delivery-20260907`
- evidence collection handoff receive: `2026-09-06T15:02:57.065553Z`（`atct_task_handoff_receive` 成功）
- artifact の前回 retry submission: session `5840`（ATCT record の `ReviewRequestedBy=5840`）
- 今回の corrected retry report / canonical retry receiver: `5838` / `goal-251-trace-delivery-retry-20260907`、receive `2026-09-06T15:44:43.946636Z`
- role: `executor`（`atct_role(expected_role=executor)` の `matches=true`）
- 対象: Goal249 の plan-review 未通知、Decision700 の Goal251 handoff 受領後の再表示
- 対象外: Goal249 liveness prompt
- 実施内容: 読み取り調査、既存テストの読み取りと必要最小限の実行のみ
- 変更: production code、Goal249 実装、既存対象外ファイルは変更していない

## 結論

### Goal249

`goal-249-plan-review-20260906` は、最初の review request cycle では commander 会話へ届いたが、reject 後に同じ handoff ID で再 request された `2026-09-06T14:19:20.043581Z` の cycle は commander 会話に現れないことを再現した。

最有力の原因は SSE の単純な遅延ではなく、長寿命の watch 内で handoff の配送キーが `eventName + HandoffID` だけで、reject 後の再 request cycle を区別しないことだ。最初の cycle が一度配送された後、同じ ID の二度目の request は `delivered` map によって抑止される。watch の reconnect は同一 watch loop 内の map を初期化し直さない。

SSE は live-only で replay/cursor がないため、live signal の取りこぼしや reconnect は回復余地を狭める増幅要因ではある。しかし、今回の二度目の canonical request が届かなかったことを SSE だけの原因とは断定できない。canonical event の生成と最初の cycle の配送は確認でき、同じ ID の再 request を抑止するコードパスが直接一致する。

### Decision700 / Goal251

具体仮説のうち、Decision700 が applied になった後、Goal251 handoff が received になる前に project reconciliation の approval projection が繰り返し bridge queue へ積まれ、active pump 中は保留され、idle ごとに古い action turn が FIFO で開始される、という前半はコード・既存テスト・会話時系列が一致する。

canonical な handoff receive 後にも `decision 700` の同じ action line が続くが、現行 `shouldProjectAppliedGoalApproval` は received handoff が存在すると false になる。したがって、受領後の各 line を「受領後 reconciliation が新たに emit した」とは説明できない。受領前に queue へ入った古い action が、active/idle の境界で drain されたという説明が最も整合する。ただし bridge の enqueue 時刻、queue 深さ、個々の reconciliation/SSE trigger は永続ログに残っていないため、queue drain を直接観測したとは言わない。

今日の Goal251 handoff received line も、canonical `received_at` と会話 action line の間に queue-order と整合する順序差が観測される。これは「遅延」と断定する証拠ではなく、canonical state と会話配送の順序が一致していないという観測である。

仮説別の判定は次のとおり。

| 仮説 | 判定 | 根拠 |
| --- | --- | --- |
| (a) 受領前に bridge/会話配送 queue へ蓄積した旧通知 | 強く支持。ただし queue 内部は間接証拠 | pre-receive の反復 emit 条件、無条件 append、active pump return、idle FIFO pump、実際の root turn 列が一致。queue DB は調査時点では空で、履歴を保存しないため反証にならない |
| (b) 現行 project-scoped reconciliation/SSE が受領後も誤条件で emit | pre-receive の条件は支持、観測された post-receive line の新規 emit 原因としては反証 | received handoff があると `shouldProjectAppliedGoalApproval` は false。既存の received/closed subtest も PASS |
| (c) 別 session/別 monitor 経路 | 今回の反復 line については支持されない | root commander thread/monitor に全反復 line があり、Goal251 parent/executor の実 action stream に Decision700 line がない。別 monitor 自体は複数存在するため、未知の別経路が絶対にないとは言わない |

## 時刻・session・monitor の時系列

時刻は保存された UTC の event/API/rollout timestamp。会話列は Codex rollout JSONL の `response_item` かつ `role=user` の実 action line だけを抽出した。guardian の nested transcript は action source として数えていない。

| UTC | canonical/API | 会話 action / session | monitor |
| --- | --- | --- | --- |
| `2026-09-06T14:16:35.751089Z` | Goal249 plan review の最初の request、session `5825` | — | — |
| `2026-09-06T14:16:37.055Z` | — | 最初の `atct plan handoff review requested` が root commander thread `01a076ed-a3cc-7f32-b82f-95337ad0e47c` に表示 | monitor `72878` |
| `2026-09-06T14:16:45.84755Z` | 最初の review receive | — | — |
| `2026-09-06T14:17:46.8Z` 前後 | 最初の review reject | `2026-09-06T14:17:46.868Z` に reject line | monitor `72878` |
| `2026-09-06T14:19:20.043581Z` | 同じ ID `goal-249-plan-review-20260906` を再 request | root commander に二度目の request line なし | monitor `72878` |
| `2026-09-06T14:56:35Z` | Decision700 が applied | `2026-09-06T14:56:35.919Z` に最初の approval line | monitor `72878` |
| `2026-09-06T14:58:49.238904Z` | Goal251 handoff request | `2026-09-06T15:00:05.061Z` に request line | monitor `72878` |
| `2026-09-06T14:59:38.691Z` | handoff receive 前 | 二回目の Decision700 approval line | monitor `72878` |
| `2026-09-06T14:59:41.439828Z` | Goal251 handoff canonical receive、receiver session `5837` | — | — |
| `2026-09-06T14:59:42.975Z` | receive 後 | Decision700 approval line | monitor `72878` |
| `2026-09-06T14:59:46.952Z`–`15:00:08.562Z` | — | approval line が複数回、`15:00:05.061Z` には handoff request line も出現 | monitor `72878` |
| `2026-09-06T15:01:16.387Z` | — | Decision700 approval line | monitor `72878` |
| `2026-09-06T15:02:13.021Z` | — | Decision700 approval line | monitor `72878` |
| `2026-09-06T15:02:16.495Z` | — | Goal251 handoff received line | monitor `72878` |
| `2026-09-06T15:02:57.065553Z` | task #1187 handoff receive、executor session `5838` | — | — |

Decision700 後の root turn telemetry では、長い turn が `14:56:35.5926000`–`14:59:32.9544040` に active で、その後 `14:59:38.7215510`、`14:59:42.9866470`、`14:59:46.9668580`、`14:59:51.8769480`、`14:59:54.1815870`、`14:59:57.7119280`、`15:00:00.4284600`、`15:00:05.2265360`、`15:00:08.0485910`、`15:01:16.0561040`、`15:02:13.0323980` などの新しい turn が作られている。これは bridge が idle 後に FIFO item を順番に start した仮説と整合するが、Codex telemetry は bridge enqueue の直接ログではない。

## 実測 canonical state / API state

### SQLite canonical state

読み取り専用 DB `/Users/masayoshi.michikawa@kanmu.co.jp/.atct/atct.db` を `2026-09-06T15:20:13Z` 前後に確認した。以下は対象行の全出力。

```text
id   goal_id  kind           status   answer_label  answered_at           applied_at            agent_session_id  created_at
694  249      goal_approval  applied  approve       2026-09-06T14:20:38Z  2026-09-06T14:20:38Z  0                 2026-09-06T14:09:36Z
697  249      decision       applied  10分          2026-09-06T14:13:09Z  2026-09-06T14:14:57Z  5825              2026-09-06T14:12:41Z
700  251      goal_approval  applied  approve       2026-09-06T14:56:35Z  2026-09-06T14:56:35Z  0                 2026-09-06T14:55:50Z

id                                                     goal_id  requested_by  received_by  requested_at                 received_at                  completed_report_at
goal-249-subcommander-20260906                         249      1743          5825         2026-09-06T14:10:06.207424Z  2026-09-06T14:10:59.482776Z
goal-251-notification-delivery-investigation-20260906  251      1743          5837         2026-09-06T14:58:49.238904Z  2026-09-06T14:59:41.439828Z

id                             goal_id  review_requested_by  review_requested_at          review_received_by  review_received_at  review_rejected_at  completed_report_at
goal-249-plan-review-20260906  249      5825                 2026-09-06T15:00:57.113957Z

id    goal_id  title                                            status  agent                  created_at            updated_at
1187  251      Trace missing plan-review notification delivery  doing   atct-251-subcommander  2026-09-06T15:00:20Z  2026-09-06T15:02:57.065553Z
```

`plan_handoffs` は履歴表ではない。同じ ID の reject 後再 request が upsert されるため、上の `15:00:57.113957Z` は Goal249 の対象 `14:19:20.043581Z` を否定しない。その後の artifact 作成直前の read-only 再取得でも同じ行の current `review_requested_at` が `2026-09-06T15:26:13.304668Z` に更新されていた。これは canonical state が current snapshot であり、過去の request event の証跡ではないことを示す。

### API reconciliation

実行した endpoint は次の二つ。HTTP response の対象フィールドは SQLite と一致した。

```text
GET http://127.0.0.1:8787/api/events/reconcile?project_id=1&goal_id=249
取得時刻: 2026-09-06T15:18:39Z
goal 249: active, updated_at=2026-09-06T14:20:38Z
decisions: 694 goal_approval/applied/applied_at=2026-09-06T14:20:38Z; 697 decision/applied/applied_at=2026-09-06T14:14:57Z
goal handoff: goal-249-subcommander-20260906 received_at=2026-09-06T14:10:59.482776Z
plan handoff: goal-249-plan-review-20260906 review_requested_at=2026-09-06T15:00:57.113957Z, review_received_at=null, review_rejected_at=null

GET http://127.0.0.1:8787/api/events/reconcile?project_id=1&goal_id=251
取得時刻: 2026-09-06T15:18:48Z
goal 251: active, updated_at=2026-09-06T14:56:35Z
decision: 700 goal_approval/applied/answered_at=2026-09-06T14:56:35Z/applied_at=2026-09-06T14:56:35Z/agent_session_id=0
goal handoff: goal-251-notification-delivery-investigation-20260906 requested_at=2026-09-06T14:58:49.238904Z, received_at=2026-09-06T14:59:41.439828Z, received_by=5837
task 1187 handoff: requested_at=2026-09-06T15:00:29.103817Z, received_at=2026-09-06T15:02:57.065553Z, received_by=5838
```

Goal251 の現在 state では received handoff が存在するため、project scope の applied approval projection 条件は成立しない。Goal249 の current plan row は後続 cycle に上書きされており、`14:19:20` event の有無を current API だけから再構成できない。

### SSE live-only の実測

```text
$ curl -sS -i --no-buffer --max-time 2 'http://127.0.0.1:8787/api/events?project_id=1'
curl: (28) Operation timed out after 2007 milliseconds with 0 bytes received
HTTP/1.1 200 OK
Cache-Control: no-cache
Connection: keep-alive
Content-Type: text/event-stream
Date: Sun, 06 Sep 2026 15:19:52 GMT
Transfer-Encoding: chunked
```

接続は受理されたが、2 秒間に過去 event の replay はなかった。実装でも `handleEvents` は subscriber の live channel を読むだけで、stable event ID/cursor による replay はない。`watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor` の cursor 引数は未使用である。

### session / monitor state

対象 rollout/session は次のとおり。

| 用途 | session/thread | monitor | worktree |
| --- | --- | --- | --- |
| root commander の実 action stream | `01a076ed-a3cc-7f32-b82f-95337ad0e47c` | `72878` | repository root |
| Goal249 subcommander | `01a0770e-6af4-7550-90bd-aeca482d82dd` | `77110` | `.worktrees/249` |
| Goal251 subcommander | `01a0773a-e067-78f0-9a04-d8ab73e627d8` | `12328` | `.worktrees/251` |
| task #1187 executor | `01a0773d-81f7-7ca2-a0ea-954b381149dc` | `20903` | `.worktrees/251` |

monitor registry の該当レコードは以下だった。

```text
72878: supervisor_pid=72878 app_server_pid=72894 project_path=.../atct started_at=2026-09-06T13:34:39.808769Z
77110: supervisor_pid=77110 app_server_pid=77132 project_path=.../.worktrees/249 started_at=2026-09-06T14:10:25.900733Z
12328: supervisor_pid=12328 app_server_pid=12329 project_path=.../.worktrees/251 started_at=2026-09-06T14:59:01.778237Z
20903: supervisor_pid=20903 app_server_pid=21049 project_path=.../.worktrees/251 started_at=2026-09-06T15:01:54.004780Z
```

root thread の実 action stream には Decision700 の反復と Goal251 handoff request/receive がある。一方、Goal251 parent/executor の実 action streamには Decision700 の反復がない。よって (c) は今回観測された反復を説明しない。

### watch/bridge log と queue state

- `/Users/masayoshi.michikawa@kanmu.co.jp/.atct/daemon.log` の `watch|bridge|sse|event|reconcil|decision|handoff` 検索結果は、schema migration の warning（`0023_handoff_review_state.sql`、`0025_workflow_event_outbox.sql`、`0024_goal_scoped_decisions.sql`）だけで、watch/reconcile/Enqueue の実行ログはなかった。
- `codexMonitorWatchOutput.Write` は action line と watch diagnostics を破棄する。supervisor は monitor error 等を stderr に出すだけで、queue item の ID、enqueue timestamp、queue depth を保存しない。
- 調査時点で `/Users/masayoshi.michikawa@kanmu.co.jp/.codex/queue_1.sqlite` を root/対象 monitor thread で読み取った結果は空だった。これは drain 後の現在値であり、過去 queue が存在しなかったことの証拠ではない。
- したがって、観測できるのは会話 action line と Codex turn telemetry までで、個別 line の enqueue と drain の因果はコードパスと時系列からの推論である。

## コードパス

### event generation → SSE → reconciliation

1. `internal/store/goal_handoff.go:797-858` の `RequestPlanHandoffReview` は canonical `plan_handoffs` row を更新し、commit 後に `publishWorkflowEvents` で `plan.handoff.review.request` を publish する。`internal/store/goal_handoff.go:862-910` の receive も同じ流れ。
2. `internal/store/queries/task.sql:378-393` は同一 plan handoff ID の request を upsert し、reject 後の再 request で `review_requested_at/report` を上書きし、received/rejected state を clear する。event history は保持しない。
3. `internal/httpapi/server.go:1367-1423, 1442-1491` の `eventPasses`/`handleEvents` は project/goal/task filter を適用して live subscriber に SSE を送る。`server.go:1531-1537` の frame に stable ID はない。
4. `internal/httpapi/server.go:1540-1653` の reconciliation endpoint と `internal/store/workflow_events.go:47-135` の `ReconcileWorkflow` は current canonical rows を返す。`internal/store/notify.go:101-129` の subscriber channel は non-blocking publish で、slow subscriber が signal を missed し得るため reconciliation が recovery path になっている。
5. `cmd/atct/watch.go:588-670` の `consumeWatchEventsWithStateAndScopeAndSinkAndInterval` は SSE frame ごと、また 30 秒 ticker ごとに `reconcileWatchScope` を呼ぶ。SSE payload 自体は action line の source ではなく、canonical reconciliation の trigger である。EOF 時は upstream reconnect する。

### Goal249 の再 request dedup

- `cmd/atct/watch.go:332-345` の watch loop は `delivered` map を loop の開始時に一度だけ作る。
- `cmd/atct/watch.go:325-328` の `watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor` は cursor を保持せず、同一 process の reconnect でこの map を reset しない。
- `cmd/atct/watch.go:970-1079` の `emitWatchDecisionWithStateAndSink` は handoff の delivery key を `watchDetectionDeliveryKey{eventName,targetID}` とし、`targetID` に HandoffID を使う。cycle timestamp/status は key に含まれない。
- `cmd/atct/watch.go:941-964` の `watchReconciliationHandoff` は review request を正しく current row から判定し、`cmd/atct/watch.go:1126-1176` が `atct plan handoff review requested ...` を format する。event generation/filter の対象 ID は正しいが、同じ ID の再 request は既に seen と判定される。

### Goal251 の approval projection → bridge FIFO

- `cmd/atct/watch.go:1101-1124` の `shouldProjectAppliedGoalApproval` は project scope、active goal、`goal_approval/applied`、GoalHandoff の received がない場合に true。`goal handoff received` がある場合は false。
- `cmd/atct/watch.go:883-890` の `reconcileWatchScope` はこの条件を満たすと `decision.approved` を delivery map を通さず直接 write するコメント付き分岐になっている。従って、同じ reconciliation が複数回走れば同一 approval line が複数回生成される。`decision 700` の ID dedup を使う通常分岐とは別である。
- `cmd/atct/codex_monitor.go:669-680` の `codexMonitorBridge.Enqueue` は empty/disabled 以外の line を queue に無条件 append し、dedup/coalesce/expiry/current canonical state check はない。
- `cmd/atct/codex_monitor.go:683-728` の `pump` は `active`、thread 未接続、empty のいずれかなら return。active を true にして `queue[0]` を StartTurn し、成功後にだけ queue 先頭を remove する。
- `cmd/atct/codex_monitor.go:835-920` の `HandleNotification`/`pumpAfterIdle` は turn completion/status idle で active を解除して pump する。これが、active 中に append された古い line が idle ごとに FIFO で action turn になる経路である。
- `cmd/atct/codex_monitor.go:766-833` の `LineSinkWithContext` と action-line recognizer は `decision.approved` および各 handoff lifecycle line を bridge 対象として認識する。
- `cmd/atct/codex_monitor_supervisor.go:57-68,155-227` は watch output を discard し、bridge/watch を起動するが、queue lifecycle の永続 log は残さない。project commander monitor `72878` が今回の root stream である。

## Goal249 の再現

### 実測 action extraction

root rollout `.../sessions/2026/09/06/rollout-2026-09-06T22-34-40-01a076ed-a3cc-7f32-b82f-95337ad0e47c.jsonl` の実 user action line を抽出した結果、対象 handoff の plan request は次の一行だけだった。

```text
2026-09-06T14:16:37.055Z atct plan handoff review requested (goal_id: 249, handoff_id: goal-249-plan-review-20260906)
```

一方、request tool の canonical response は次のとおり。

```text
GoalID: 249
ID: goal-249-plan-review-20260906
ReviewRequestedAt: 2026-09-06T14:19:20.043581Z
ReviewRequestedBy: 5825
ReviewReceivedAt: null
ReviewRejectedAt: null
```

この二つは同じ ID の別 cycle である。first cycle は root に表示され、receive/reject 後の second cycle は canonical row/event が生成されたが root action stream に現れない。`task.sql` の reopen upsert と `watch.go` の `eventName + HandoffID` dedup が、観測された境界をそのまま再現する。

### Goal249 の既存テストが示す範囲

`cmd/atct/watch_scope_test.go` の `TestWatchFormatsHandoffReviewEvents` は plan request line の format/filter を PASS する。`internal/store/plan_handoff_test.go` の `TestPlanHandoffReviewLifecycle` は request/receive/reject/reopen の canonical lifecycle を PASS する。しかし、同じ HandoffID の reject→request を一つの長寿命 project watch で二回 emit する regression test は存在しない。

## Decision700 / Goal251 の再現

### 既存コード・テストから検証できる因果

`cmd/atct/watch_test.go` の `TestReconcileWatchScopeReprojectsAppliedGoalApprovalForCommander` は、active goal・applied approval・未受領 handoff の状態で reconciliation を二回実行し、同一 approval line が二回出ることを PASS とする。これは現在の projection bypass が意図された既存契約になっていることを示す。

`TestReconcileWatchScopeSendsAppliedApprovalToCodexMonitorBridge` はその line が bridge sink に入ることを PASS する。`cmd/atct/codex_monitor_test.go` の `TestCodexMonitorQueueDeliversFIFOAfterIdle`、`TestCodexMonitorQueueContinuesWhenCompletionRacesTurnResponse`、`TestCodexMonitorQueuesBeforeThreadIsAttached` は、無条件 queue、active/attach 待ち、idle 後 FIFO drain を PASS する。これらは dedup/coalesce/expiry があることを検証していない。

逆に `TestReconcileWatchScopeSuppressesAppliedGoalApprovalAfterReceiptOrGoalClosure` の received/done/dropped ケースは approval projection を出さないことを PASS する。現行 canonical state で Goal251 handoff received 後の新規 reconciliation が同じ approval を emit する、という (b) の post-receive 説明はこのテストとコードに反する。

### root stream の実測出力

root rollout から抽出した Decision700 と Goal251 handoff の全対象 action line は次のとおり。

```text
2026-09-06T14:56:35.919Z atct decision approved (decision_id: 700)
2026-09-06T14:59:38.691Z atct decision approved (decision_id: 700)
2026-09-06T14:59:42.975Z atct decision approved (decision_id: 700)
2026-09-06T14:59:46.952Z atct decision approved (decision_id: 700)
2026-09-06T14:59:51.866Z atct decision approved (decision_id: 700)
2026-09-06T14:59:54.168Z atct decision approved (decision_id: 700)
2026-09-06T14:59:57.696Z atct decision approved (decision_id: 700)
2026-09-06T15:00:00.418Z atct decision approved (decision_id: 700)
2026-09-06T15:00:05.061Z atct goal handoff requested (goal_id: 251, handoff_id: goal-251-notification-delivery-investigation-20260906)
2026-09-06T15:00:08.562Z atct decision approved (decision_id: 700)
2026-09-06T15:01:16.387Z atct decision approved (decision_id: 700)
2026-09-06T15:02:13.021Z atct decision approved (decision_id: 700)
2026-09-06T15:02:16.495Z atct goal handoff received (goal_id: 251, handoff_id: goal-251-notification-delivery-investigation-20260906)
```

最初の approval line は canonical applied (`14:56:35Z`) と一致する。最初の反復は canonical receive (`14:59:41.439828Z`) の前、次は約 1.5 秒後、その後も続く。receive 後の line を queue drain と読むことは、(i) pre-receive projection が queue を増やせる、(ii) bridge は active 中に pump を return する、(iii) idle で FIFO start する、(iv) current receive 後は projection false、という四つの独立した観測に基づく。enqueue timestamp/queue depth がないため、個別 line の発生元を直接証明したものではない。

## 根本原因候補の順位

1. **高: Goal249 の同一 HandoffID による cycle-blind dedup。** canonical reopen、first cycle の配送、second cycle の欠落、`delivered` key の実装が直接一致する。
2. **高: Goal251 の applied approval projection bypass と bridge FIFO の stale item 保持。** pre-receive の反復生成と、受領後に続く action line の順序を最も少ない仮定で説明する。ただし enqueue/drain の直接ログはない。
3. **中: SSE live-only / reconnect / in-memory signal loss。** replay/cursor がなく、subscriber channel も signal を落とし得るため、canonical recovery に依存する設計上の増幅要因。ただし Goal249 の same-ID dedup が単独で欠落を説明する。
4. **低: 別 session/別 monitor 経路。** 複数 monitor は実在するが、対象の反復 action は root commander stream に局在し、Goal251 parent/executor stream にはない。

## 最小の regression test 条件（設計判断ではない）

実装方法は決めず、以下の観測可能な条件だけを回帰テストに必要とする。

1. 一つの長寿命 project watch で、同じ plan HandoffID に対して `request → receive → reject → request` を実行する。二つの request cycle それぞれで `plan.handoff.review.request` が一回ずつ action line になり、二回目が HandoffID だけを理由に抑止されないこと。
2. live SSE signal を受けた場合と受けなかった場合の両方で、current canonical の未完了 plan request を必要な回数だけ recover できること。過去の event replay を前提にせず、同じ current row を無限に再配送しないこと。
3. active goal + applied Goal700 + 未受領 Goal251 handoff で reconciliation が複数回走る場合、既存の projection テストが表す現行挙動を固定したうえで、Goal handoff receive の canonical transition を挿入する。receive より前に生成された approval action が、receive 後に新しい action turn として開始されないこと。
4. handoff receive 後の reconciliation は `decision 700` approval を一件も新規生成しないこと。goal/task/plan handoff の実イベント配送と project scope filtering は維持すること。
5. bridge が active 中、同一の解消済み approval を複数回 enqueue しても、idle 通知ごとに古い approval action turn が増殖しないこと。idle 後に開始される action は canonical state で未解消のものだけであること。
6. root project monitor、Goal251 parent monitor、executor monitor を同時に用い、Decision700 と handoff action が意図した scope/session にだけ現れること。別 monitor の存在だけで root line の出所を推定しないこと。

## 検証した既存テスト

テストコードは読み取りのみ。最初の sandbox 実行では Go build cache の作成が拒否され、`TestConsumeWatchEventsPerformsPeriodicReconciliation` は `httptest.NewServer` の localhost bind で `operation not permitted` となった。これは sandbox 制約であり、fixture failure でも product behavior failure でもない。task-specific `GOCACHE` と elevated 実行に切り替え、関連テストはすべて PASS した。

### 最初の cmd/atct 実行で観測した状態

| test | 結果 | 理由 |
| --- | --- | --- |
| `TestReconcileWatchScopeReprojectsAppliedGoalApprovalForCommander` | PASS | 現行 duplicate projection を期待する既存テスト。product behavior を PASS としたもので、修正済みの意味ではない |
| `TestReconcileWatchScopeSuppressesAppliedGoalApprovalAfterReceiptOrGoalClosure` | PASS（全 subcase） | received/closed で抑止する現行条件 |
| `TestReconcileWatchScopeSendsAppliedApprovalToCodexMonitorBridge` | PASS | projection line が bridge sink に届く現行経路 |
| `TestCodexMonitorQueueDeliversFIFOAfterIdle` | PASS | bridge FIFO drain |
| `TestCodexMonitorQueueContinuesWhenCompletionRacesTurnResponse` | PASS | completion race 時の現行 queue 処理 |
| `TestCodexMonitorQueuesBeforeThreadIsAttached` | PASS | thread attach 前の保持 |
| `TestConsumeWatchEventsPerformsPeriodicReconciliation` | FAIL（初回 sandbox のみ） | `httptest` localhost bind が `operation not permitted`; fixture/product failure ではない。elevated retry は PASS |
| `TestWatchReconcilesAtStartAndReconnect` | 初回は上記 panic の影響で独立判定なし、elevated retry PASS | fixture/product failure ではない |

### 再実行の全文

```text
=== RUN   TestConsumeWatchEventsPerformsPeriodicReconciliation
--- PASS: TestConsumeWatchEventsPerformsPeriodicReconciliation (0.05s)
=== RUN   TestWatchReconcilesAtStartAndReconnect
--- PASS: TestWatchReconcilesAtStartAndReconnect (0.30s)
PASS
ok  github.com/michiomochi/atct/cmd/atct  0.352s
```

```text
=== RUN   TestSSEPublishesAllDecisionTransitionsWithExactPayloads
--- PASS: TestSSEPublishesAllDecisionTransitionsWithExactPayloads (0.00s)
=== RUN   TestSSEStreamsOnlyLiveEventsWithoutStableID
--- PASS: TestSSEStreamsOnlyLiveEventsWithoutStableID (0.00s)
=== RUN   TestWorkflowReconcileCanonicalEndpoint
--- PASS: TestWorkflowReconcileCanonicalEndpoint (0.00s)
=== RUN   TestSSEDoesNotRejectAStaleCursor
--- PASS: TestSSEDoesNotRejectAStaleCursor (0.00s)
PASS
ok  github.com/michiomochi/atct/internal/httpapi  0.378s
```

```text
=== RUN   TestPlanHandoffReviewLifecycle
--- PASS: TestPlanHandoffReviewLifecycle (0.00s)
=== RUN   TestDecisionTransitionsPublishWithoutDeliveryPersistence
--- PASS: TestDecisionTransitionsPublishWithoutDeliveryPersistence (0.00s)
=== RUN   TestScopedWorkflowReconciliationReturnsCanonicalState
--- PASS: TestScopedWorkflowReconciliationReturnsCanonicalState (0.00s)
=== RUN   TestGoalScopedWorkflowReconciliationReturnsCompletedReopenedHandoffAndAppliedDecision
--- PASS: TestGoalScopedWorkflowReconciliationReturnsCompletedReopenedHandoffAndAppliedDecision (0.00s)
PASS
ok  github.com/michiomochi/atct/internal/store  0.325s
```

```text
=== RUN   TestNamedGoalAndPlanHandoffReviewRoutesReturnRoleEvidence
--- PASS: TestNamedGoalAndPlanHandoffReviewRoutesReturnRoleEvidence (0.00s)
PASS
ok  github.com/michiomochi/atct/internal/daemon  0.513s
```

追加の format/scope testsも全て PASS だった。

```text
TestWatchScopeProjectStopsTaskHandoffReported              PASS
TestWatchScopeProjectDeliversDecisionApprovedAndRejected  PASS
TestWatchScopeProjectDeliversHumanDecisionAnswered         PASS
TestWatchFormatsHandoffReviewEvents                        PASS（task request / plan request / goal reject subcases）
```

実行した代表コマンドは以下。いずれも production code を変更しない。

```text
GOCACHE=/private/tmp/goal251-trace-cmd-cache go test ./cmd/atct -run '^(TestReconcileWatchScopeReprojectsAppliedGoalApprovalForCommander|TestReconcileWatchScopeSuppressesAppliedGoalApprovalAfterReceiptOrGoalClosure|TestReconcileWatchScopeSendsAppliedApprovalToCodexMonitorBridge|TestCodexMonitorQueueDeliversFIFOAfterIdle|TestCodexMonitorQueueContinuesWhenCompletionRacesTurnResponse|TestCodexMonitorQueuesBeforeThreadIsAttached|TestConsumeWatchEventsPerformsPeriodicReconciliation|TestWatchReconcilesAtStartAndReconnect)$' -count=1 -v
GOCACHE=/private/tmp/goal251-trace-httpapi-cache go test ./internal/httpapi -run '^(TestSSEStreamsOnlyLiveEventsWithoutStableID|TestWorkflowReconcileCanonicalEndpoint|TestSSEDoesNotRejectAStaleCursor|TestSSEPublishesAllDecisionTransitionsWithExactPayloads)$' -count=1 -v
GOCACHE=/private/tmp/goal251-trace-store-cache go test ./internal/store -run '^(TestPlanHandoffReviewLifecycle|TestDecisionTransitionsPublishWithoutDeliveryPersistence|TestScopedWorkflowReconciliationReturnsCanonicalState|TestGoalScopedWorkflowReconciliationReturnsCompletedReopenedHandoffAndAppliedDecision)$' -count=1 -v
GOCACHE=/private/tmp/goal251-trace-daemon-cache go test ./internal/daemon -run '^TestNamedGoalAndPlanHandoffReviewRoutesReturnRoleEvidence$' -count=1 -v
```

`TestDecisionTransitionsPublishWithoutDeliveryPersistence` が PASS する一方、migration `0027_drop_workflow_event_delivery.sql` は outbox、project sequence、watch cursor を drop している。従って event delivery history が DB に残らないことも確認済みである。

## 読むべき実装ファイルと関数

- `cmd/atct/watch.go`: `watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor`、`consumeWatchEventsWithStateAndScopeAndSinkAndInterval`、`reconcileWatchScope`、`emitWatchDecisionWithStateAndSink`、`shouldProjectAppliedGoalApproval`、`watchReconciliationHandoff`
- `cmd/atct/codex_monitor.go`: `codexMonitorBridge.Enqueue`、`pump`、`LineSinkWithContext`、`HandleNotification`、`pumpAfterIdle`
- `cmd/atct/codex_monitor_supervisor.go`: `codexMonitorWatchOutput.Write`、`runCodexMonitorWatchScoped`
- `internal/store/goal_handoff.go`: `RequestPlanHandoffReview`、review receive/reject/complete の publish path
- `internal/store/queries/task.sql`: plan handoff request upsert
- `internal/store/notify.go`: `SubscribeEvents`、non-blocking `publishEvent`
- `internal/store/workflow_events.go`: `publishWorkflowEvents`、`ReconcileWorkflow`
- `internal/httpapi/server.go`: `eventPasses`、`handleEvents`、`handleEventsReconcile`、`eventProjectID`
- `internal/store/migrations/0027_drop_workflow_event_delivery.sql`: delivery persistence の削除

## 変更パス

この調査で作成したファイルは本 Markdown だけ。

```text
doc/investigations/2026-09-07-task-1187-delivery-trace.md
```

production code、テストコード、Goal249 実装、commit、push は変更していない。

## review request の状態と provenance

artifact 作成後、指定された `atct_task_handoff_review_request(task_id=1187, handoff_id=goal-251-trace-delivery-20260907, review_request_report=非空)` を呼び出したが、ATCT は次を返した。

```text
task handoff review is not in the required state
```

その後の読み取り専用確認では、対象 handoff が既に次の state になっていた。

```text
id                                task_id  requested_by  received_by  requested_at                 received_at                  completed_report_at
goal-251-trace-delivery-20260907  1187     5837          5838         2026-09-06T15:00:29.103817Z  2026-09-06T15:02:57.065553Z  2026-09-06T15:33:00.887425Z
```

従って、最初の review request は呼び出し済みだが、呼び出し時点で handoff が review request 可能状態ではなかった。続く retry handoff では session `5840` がこの artifact を review request したが、実在しない path を report に記載したため `2026-09-06T15:39:25.32201Z` に reject された。今回 session `5838` は、証拠を収集した元 handoff と session、前回 retry submission の session、実在する artifact path をこの文書で明示して再提出する。状態を戻す操作、完了を取り消す操作、追加の ATCT tool は実施していない。

今回 retry handoff の canonical state は次のとおりである。

```text
id                                      task_id  requested_by  received_by  requested_at                   received_at                    review_requested_at
goal-251-provenance-correction-20260907 1195     5837          5838         2026-09-06T15:44:14.578612Z  2026-09-06T15:44:43.946636Z  null
```
