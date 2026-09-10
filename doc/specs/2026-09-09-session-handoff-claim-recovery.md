# Session 消失後の handoff / claim recovery

## Goal

agent session が discard された、または monitor crash 後に戻らないと確定したとき、open な goal / plan / task / task-create handoff を、その handoff の上流責務者だけが安全に回復できるようにする。live owner は決して置換しない。既存の handoff、Decision、task、作業ツリーは保持する。

## Context and decision

`doc/execution-flow.md` を正本とする。handoff は durable な責務記録であり、monitor は状態を再読して促すだけで、所有権の根拠にはしない。Goal 230 は commander turnover 時の goal-review receiver だけを補った代表事例で、Goal 261 のような session 消失では receiver/reviewer/requester 自体が消えるため、同じ限定分岐を handoff ごとに増やす方法は採らない。

検討した案は次の三つである。

1. handoff 種別ごとに Goal 230 型の fallback を足す。差分は小さいが、request / receive / review の相互作用を取りこぼす。
2. monitor-health の lease 切れで自動移管する。速いが、monitor crash と live agent を区別できず、live owner 保護に反する。
3. **明示 recovery 操作を共通化する。** `claimIsDefinitelyDead` で session の死亡を証明し、正規の上流責務者が一度だけ recovery を記録して次の正規状態へ戻す。これを採用する。

## Invariants

- liveness は既存の PID + process-start identity で `definitely dead` のみを返す。PID / start identity が無い、読めない、または一致する session は自動 recovery 不可である。
- monitor crash、SSE 断、monitor-health lease 切れは session 死亡の証拠ではない。monitor は recovery 必要性を通知できるが、DB ownership を変更しない。
- recovery caller は handoff の phase ごとの上流責務者に限る。goal と plan は target project の現在の commander、task と task-create は target goal の現在の received subcommander である。foreign project / wrong role / same downstream owner は拒否する。
- compare-and-set SQL が stale actor の現在の session ID と phase を同時に照合する。discarded session は全 mutation の前に拒否する。二重 caller は一方だけ成功し、live actor も stale 判定後に戻った actor も上書きされない。
- recovery は旧 row を削除・通常完了にしない。replacement request が必要な stale receiver/requester phase だけは、その row を `recovered_at` で terminal にして partial-unique lock を外す。旧 session、phase、理由、recovered_by、新 request、timestamp は append-only audit event に残す。
- Decision と task の status、review report / reject report、作業中ファイル、idempotency key は変更しない。retry は recovery event ID を返すだけで二重移管しない。

## Actor-specific recovery state machine

Recovery は handoff を「最初から作り直す」操作ではない。責務を持つ phase だけを正規の再実行可能状態へ戻す。

| Handoff / phase | stale actor only | Authorized caller | CAS target | Kept unchanged | Resumed normal transition |
| --- | --- | --- |
| goal: requested | requester commander | current project commander | requester session + phase | no receiver/claim exists | atomically terminalize old row as recovered and issue a new goal request |
| goal: received / worker review requested / rejection pending | receiver subcommander | current project commander | receiver session + phase | reviewer/requester, reports, task handoffs and tasks | atomically terminalize old row as recovered and issue a new goal request; replacement subcommander ordinarily receives it |
| goal: review received; original requester lineage stale, recorded reviewer live | original requester commander lineage | recorded current commander reviewer | no handoff-field mutation; completion guard CASes caller = `ReviewReceivedBy` | worker receiver, goal claim, all request/review/completion fields | recorded reviewer ordinarily completes, then requests human goal review |
| goal: review requested / review received | reviewer commander | current project commander | review receiver session + phase | worker receiver, goal claim, review request/report | clear only stale review receipt to `review requested`; current commander ordinarily receives then completes/rejects |
| plan: review requested / review received | reviewer commander | current project commander | plan review receiver session + phase | submitting subcommander, its goal claim, plan request/report | clear only stale review receipt to `review requested`; current commander ordinarily receives then completes/rejects |
| task-create: requested / received | requester/receiver subcommander | current received goal-holder | task-create requester/receiver session + phase | goal receiver/claim, created tasks, idempotency keys | atomically terminalize the old attempt as recovered and create a normal replacement task-create request; the current goal holder ordinarily receives it and retries `task.create` |
| task: requested | requester subcommander | current received goal-holder | requester session + phase | no executor receiver exists | terminalize old row as recovered and issue a new task request |
| task: received / worker review requested / rejection pending | executor receiver | current received goal-holder | executor session + phase | subcommander reviewer/requester, task/report | terminalize old row as recovered and issue a new task request to a replacement executor |
| task: review requested / review received | reviewer subcommander | current received goal-holder | task review receiver session + phase | executor receiver, task claim, review request/report | clear only stale review receipt to `review requested`; current goal holder ordinarily receives then completes/rejects |

reviewer-only recovery は worker / receiver / claim を一切変更しない。receiver-only recovery は reviewer / requester を変更しない。replacement は旧 handoff の receiver を書換えず、新 request + ordinary receive で得る。`recovered_at` は normal completion ではなく、その row を reopen 不可の audit history にする。

Goal 230/240 の completion-lineage case は handoff recovery event ではない。`ReviewReceivedBy` は live commander が review を受領した durable evidence なので、`CompleteGoalHandoffByReviewer` の既存 reviewer check を通った後、`RequestGoalReview` の guard は `RequestedBy == ReviewReceivedBy` を要求してはならない。guard はその request caller が `ReviewReceivedBy` と一致し、現在の project commander であることを確認する。したがって stale original requester を書換えず、live reviewer と live subcommander claim を不変にして complete → goal-review request へ進める。

## API and persistence

`atct_handoff_recover` という一つの typed MCP tool / daemon method を追加する。入力は `handoff_kind`, `handoff_id`, 対象 ID、`reason`だけである。caller session は transport から導出し、旧/new session ID は入力させない。kind-specific Store method が対象と phase を検証する。session discard は別の human-decision boundaryなので `atct_session_discard_request` / `atct_session_discard` だけを追加する。user-facing alias や monitor 固有の recovery endpoint は増やさない。

`handoff_recoveries` は append-only audit table とする。unique `(handoff_kind, handoff_id, recovered_phase, stale_session_id)` により同じ recovery の retry を吸収する。列は handoff kind/id、goal/task ID、phase、stale session、proof kind、discard decision ID（nil 可）、recovered-by session、新 request ID（nil 可）、reason、created_at である。正常な receive/complete/reject は既存 handoff columns を引き続き正本とし、この table は authorization の代わりにならない。

### Human-confirmed session discard

PID が残るが agent session が失われた場合は、自動判定では回復しない。current project commander は `atct_session_discard_request` で対象 session、project、理由を指定して human Decision を作る。human が approve した後だけ、同じ commander が `atct_session_discard` を実行できる。reject/timeout/foreign project/wrong role は session に何も書かない。

discard は不可逆な session-revocation record (`discarded_at`, `discarded_by`, `discard_decision_id`, `discard_reason`) を一回だけ書く。approve 済み Decision、target session、project authorization、session が既に discarded でないことを一つの transaction で照合する。live PID は human approval を妨げないが、approval を持たない caller、monitor、lease expiry は live session を discard できない。すべての handoff mutation は caller session が discarded でないことを条件にし、recovery SQL は stale actor session と phase を照合する。この二つの CAS により discard 後の旧 session の late receive/request/review/complete/reject は拒否され、recovery は discard proof と audit event と next request を一つの transaction で書く。

session key での reattach は discarded row を復活させず、replacement session は recovery 後の ordinary receive を行う。monitor cleanup は discard API を呼べず、health row は監査の補助以外に使わない。

## Error handling and observability

live、unknown、foreign、phase mismatch、already recovered の各拒否は handoff と audit event を変更しない。monitor crash は monitor-health に degraded/stopped を記録するだけである。watch reconciliation は stale candidate を責務者に通知するが、recovery tool を自動実行しない。

`atct_goal_sessions` と workflow reconciliation は recovery event と current owner を表示する。Goal 230 / 261 の手作業 turnover は、この同じ event と authorization 経路を通る。履歴 API は旧 owner と recovery reason を返し、正常な current-state API は新 owner のみを返す。完了済み task-create attempt の後に plan が改訂・承認された場合も、旧 row を履歴として残したまま新しい task-create request を作成する。

## Acceptance criteria

1. goal, plan, task, task-create の全 phase で live / unknown owner は recovery 不可、confirmed discard と PID/start mismatch のみ可能である。
2. commander turnover は goal と plan を、subcommander turnover は task-create と task を、各々の正規 callerだけが回復できる。Goal 230/240 の live reviewer + stale original-requester lineage は、row mutation なしで complete → goal-review request を通る。
3. monitor crash / lease expiry だけでは recovery 不可だが、監査可能な candidate notification は出る。
4. concurrent recovery、同じ event の retry、discard と old session の late receive/request/review/complete/reject、foreign caller、二重 receiver は state を壊さず拒否または idempotent replay となる。
5. recovery 前後で Decision、task status、review reports、idempotent task-create replay、および legacy handoff read API は保存される。

## Non-goals

- monitor / wrapper の自動再起動、lease による所有権移管、作業ツリーや未コミット変更の処理。
- closed handoff の reopen、human completion decision の自動処理、Goal 230 専用の別機構。
