# Codex CLI monitor path architecture investigation

調査日: 2026-08-29
対象: この worktree の ATCT SSE/watch、Codex の接続入口、App Server 相当の実装、プロセス寿命、関連テスト

## 結論

このリポジトリには、Codex CLI を起動・監視するコードも、Codex App Server の protocol/client/server 実装もない。`codex exec`、`app_server`、`app-server` に相当する production の分岐・実行・ソケット接続は見つからない。Codex 関連の実装入口は MCP だけである。

従って現在の経路は、Codex 固有の monitor ではなく次の共有 MCP 経路である。

```text
.codex-plugin/plugin.json
  -> .mcp.json (HTTP http://127.0.0.1:8787/mcp)
  -> internal/daemon/server.go:Daemon.HTTPHandler
  -> internal/mcpshim.Register / daemon Unix RPC
```

`atct watch` は `cmd/atct` の独立した stdout プロセスで、SSE を読んで人間向けの一行に変換する。`skills/start/SKILL.md` はこの Monitor の attach を Claude Code のみに限定し、Codex は skip すると明記している。新しい Codex CLI monitor を設計する際は、既存 MCP の挙動と、monitor が存在しない現在の Codex 挙動を保つ必要がある。

## Codex と CLI の入口

- `.codex-plugin/plugin.json:26-27` は `skills`, `hooks`, `mcpServers: "./.mcp.json"` を宣言する。Codex 専用 command、exec hook、App Server 設定はない。
- `.mcp.json:2-6` は `atct` を `type: "http"`, `url: "http://127.0.0.1:8787/mcp"` として登録する。
- `bin/atct` と `bin/atct-mcp` は `bin/_resolve` に渡す薄い wrapper。`bin/_resolve` は `atct` / `atct-mcp` の release binary を `~/.atct/bin/` に cache し、必要ならダウンロードして exec する。Codex CLI を起動する wrapper ではない。
- `cmd/atct-mcp/main.go:42-70` は stdio 用の汎用 MCP shim。`daemonctl.Ensure`、Unix socket の `run.register{pid,cwd}`、`mcpshim.Register`、`mcp.StdioTransport` の順で動く。
- `internal/daemon/server.go:59-76` の `Daemon.HTTPHandler` は ATCT の streamable HTTP MCP endpoint `/mcp`。HTTP request ごとに `run.register` を行うが、渡すのは `pid: os.Getpid()` だけで、これは HTTP daemon の PID であり Codex client の PID/CWD ではない。CWD を渡す stdio shim と identity の意味が異なる。
- `internal/mcpshim/client.go:Client.Call` は RPC ごとに Unix connection を開き、1 行の `rpc.Request` / `rpc.Response` を送って閉じる。event stream や client process supervision は持たない。
- `internal/mcpshim/tools.go:Register` は MCP server connection ごとに session ID holder を作る。`atct_session_identify` は `session.identify` を呼び holder を canonical ID に更新し、`atct_role` は `session.role` を読む。残りの agent tools は daemon RPC に転送され、返却 envelope は `RawWithUnappliedDecisions{Data,Role,UnappliedDecisions,ClaimableTasks}`。
- `internal/daemon/handler.go:535-583` の `run.register` / `session.identify` / `session.role` が daemon 側の identity 入口。`internal/store/store.go:RegisterAgentSessionInProject` と `IdentifyAgentSession` は、可能なら PID と process start time を保存する。

`cmd/atct/main.go` の一般 CLI 入口は `daemon`, `project`, `goal`, `context`, `pending`, `watch`, `role`, `handoff`。`watch` の引数は `-goal` と `-project` を parse する (`main.go:219-221`) が、dispatch は `runWatch(dir, config.watchGoalID)` のみ (`main.go:413-415`) で `watchProjectScope` を渡していない。したがって現在の実行時挙動では `-project` と bare watch の差は無く、どちらも `goalID == ""` の path に入る。この点は `skills/start/SKILL.md` の Commander 指示 (`atct watch -project`) と一致していない。

## SSE/watch の現在のデータフロー

### daemon / store 側

`internal/store/notify.go` の中心型は `DecisionEvent{Name string, Data any}`。`Store.SubscribeEvents` は buffer 16 の channel を返し、`publishEvent` は各 subscriber に non-blocking send する。channel が満杯なら event を捨てる。バスは daemon process 内のメモリだけで、永続化、sequence、cursor、replay、SSE `id:`、`Last-Event-ID` はない。

主な producer は次のとおり。

- `internal/store/decision.go`: `decision.created`, `decision.answered`, `decision.withdrawn`, `decision.applied`。default 適用も `decision.answered` で、`DefaultAppliedAt` を payload に含む。
- `internal/store/goal.go`: `goal.created`, `decision.approved`, `decision.rejected`, `goal.withdrawn`。withdrawal payload は `GoalWithdrawnEvent`。
- `internal/store/task_handoff.go:CompleteTaskHandoff` と `goal_handoff.go:CompleteGoalHandoff`: 共通名 `handoff_reported`。task handoff は `DetectionEvent` の `TaskID` を持ち、goal handoff は `TaskID` を持たない。request/receive 自体は generic event ではない。
- `internal/daemon/handler.go` の `handoff.yielded`: `DetectionEvent{ProjectID,GoalID,TaskID}` を `handoff_yielded` として publish。
- `internal/daemon/wakeup.go:runMaintenanceWith`: expired default を適用し、`keepalive` を先に publish し、`wakeupTracker.evaluateWith` の結果を publish。評価失敗時は stable な `wakeup.evaluate_failed` ID を recovery まで再利用する。

`internal/store/wakeup.go:12-111` の event/payload は以下である。

- `WakeupEvent`: `WakeupID`, `ProjectID`, `ActionableGoalCount`, `UnassignedGoalCount`, `UnassignedGoalIDs`, `UnstartedTaskCount`, `WaitingAnswerTaskCount`, `UntouchedTaskCount`, `DelegatedTaskCount`, `WaitingAnswerCount`。
- `WakeupDiscrepancyEvent`: `WakeupID`, `ProjectID`, detector/counted の unstarted task count。
- `WakeupEvaluateFailedEvent`: `WakeupID`, `Reason`。
- `DetectionEvent`: `DetectionID`, optional `DecisionID`, `ProjectID`, optional `GoalID` / `TaskID` / `HandoffID`, `WorktreeActivity`, `CompleteReport`。
- `KeepaliveEvent`: `At`。
- `GoalWithdrawnEvent`: `GoalID`, `ProjectID`, `Reason`, dropped task IDs、closed task handoff IDs、withdrawn decision IDs。

`wakeupTracker` は `internal/daemon/wakeup.go` の in-memory state。active condition は wakeup が初回 3 分・再送 3 分、condition-specific detection は原則 15 分、handoff/undelegated claim は 30 分、answered-unapplied decision は即時、default/stale claim は 3 分で publish する。tracker は daemon restart で消える。

### HTTP API

- `internal/httpapi/server.go:421-...:handleInbox` の `/api/inbox` は `inboxResponse{OpenDecisions,UnappliedDecisions,ActiveGoals,ProposedGoals,AttentionTasks}` を返す。watch が使うのは `UnappliedDecisions` だけで、これは event bus の replay ではない。
- `/api/projects` は project の numeric `id` と `root_path` を返す。
- `internal/httpapi/server.go:1397-1434:handleEvents` の `/api/events` は query の `project_id` / `goal_id` を `parseEventFilter` で canonical numeric ID に解決し、`eventPasses` (`eventProjectID`, `eventMatchesGoalID`) で filter して `event: <Name>\ndata: <JSON>\n\n` を flush する。初期 snapshot、event ID、replay は送らない。
- goal filter では `KeepaliveEvent` と `WakeupEvaluateFailedEvent` は goal ID が無くても通る。project filter でも payload の project ID が 0 の event は通る。domain decision の project ID は goal lookup で後から解決するため、lookup error は drop になる。
- `eventProjectID` は `domain.Goal` (`goal.created`) を扱わず 0 を返すため、`project_id` 付き SSE でも `goal.created` は全 project を通る。逆に `eventMatchesGoalID` も `domain.Goal` を扱わないため、`goal_id` 付き SSE では `goal.created` が通らない。`wakeup` も goal payload ではないため goal filter では通らない。これは CLI の `watchScopeFilter` より先に起きる server-side 制約である。
- `/api/ws` (`internal/httpapi/ws.go`) は同じ store event bus/filter を WebSocket で運ぶ別経路だが、Codex CLI/App Server の経路ではない。

### `atct watch`

`cmd/atct/watch.go` の主要 data shape は `watchDecision` (`id`, decision/project/wakeup/detection/goal/task/handoff IDs、default state、wakeup counts、reason、worktree activity、complete report)、`watchInbox{unapplied_decisions}`, `watchProject{id,root_path}`, `watchSSEFrame{name,data}`。daemon の numeric ID と legacy string ID の両方を `decodeEntityID` / custom `UnmarshalJSON` で string に正規化する。

`runWatch(dir, goalID)` は次の順で動く。

1. `watchBaseURLs` で registry の HTTP address、次に `127.0.0.1:8787..8796` を試す。
2. `/api/projects` と CWD の longest-root match から project ID を決める。project lookup が失敗、または CWD が未登録なら project filter は空になる。
3. `daemonctl.RegisterWatchScoped` で watch を登録し、project ID が空でない場合だけ `ReapWatches` と roster 出力を行う。
4. `watchSnapshotWithProject` が `/api/inbox` を取り、snapshot の unapplied decisions を出す。
5. `watchLoopWithEnsureAndProjectIDAndGoal` が `/api/events?project_id=...&goal_id=...` に接続する。接続が切れるたび snapshot を取り直してから再接続する。

`readWatchSSEFrames` は Scanner の最大 token 1 MiB、frame channel buffer 16、複数 `data:` 行の連結、comment/未知 field の無視を行う。SSE frame に `id` は無く、reconnect 時に replay cursor を送らない。

`emitWatchDecisionWithState` の delivery state は watch process 内だけにある。

- 通常の decision は `(eventName, decisionID, defaultApplied)` で dedup。
- `goal.created`, `detection.*`, `handoff_reported` は event と target（decision/goal/handoff/task）で dedup。fresh な `DetectionID` では dedup しない。
- `wakeup` は rendered line の直前内容だけを比較し、daemon restart 後も同じ内容を抑制する。discrepancy/evaluate failure は `(eventName,wakeupID)`、`handoff_yielded` は毎回出す。
- `formatWatchDecision` に無い event は visible output にならない。malformed JSON、必須 ID 欠落、writer error は loop error になる。

`cmd/atct/watch_scope.go` の `watchScopeFilter` は現在、`goalID != ""` なら全 event を許可し、`goalID == ""` なら commander/action-worthy 粒度を選ぶ。後者は human の `decision.approved/rejected/answered`（default 適用を除く）、`goal.created`、goal-level detection、`wakeup.discrepancy`、`wakeup.evaluate_failed`、goal handoff の `handoff_reported` を通す。一方 task handoff、`handoff_yielded`、task-level detection、default/unapplied detection を抑える。`wakeup` は actionable/unassigned の goal-level 3 項目が変わった場合だけ通す。未知 event name は predicate では通すが formatter が捨てる。

これは action-worthy event を別の durable queue に入れる実装ではない。`DetectWakeup` (`internal/store/wakeup.go`) が DB から状態を再計算し、tracker が条件成立後に event を作り、lossy in-memory bus が配送するだけである。`/api/inbox` が reconnect 時に回収するのは unapplied decision のみで、missed wakeup/detection/handoff/approval event の一般 replay にはならない。

## fail-open、start/stop、orphan

- `watchLoopWithEnsureAndProjectIDAndGoal` は snapshot/SSE error を受けても、context が生きていれば `ensureWatchDaemon`、`waitForWatchReconnect`、再試行へ進む。Ensure error は stdout に出し、5 回連続で Ensure を disable するが、connection retry は続ける。成功 snapshot で failure count を reset する。`watchReconnectInterval=5s`, snapshot timeout 5s, keepalive missing timeout 90s。
- keepalive は 30 秒ごとに daemon から送られ、watch は 90 秒来なければ一行だけ報告する。keepalive 自体は visible output にしない。
- SIGINT/SIGTERM の context cancellation は watch loop が nil で終了する。`runWatch` の通常終了では registration cleanup が走るが、強制 kill なら registry file が残り、次の同一 project watch の startup reap が回収する。
- snapshot error / HTTP error は retry されるが、project discovery failure は `runWatch` で黙って無視されるため、project-scoped URL にならない。これは filter の fail-open 側の具体的挙動である。

daemon lifecycle は `internal/daemonctl` と `cmd/atct/main.go:runDaemon` にある。

- `daemonctl.Registry` は `PID`, `HTTPAddr`, `SocketPath`, `Version`, `StartedAt`。`Registry.Healthy` は PID の signal 0 と Unix socket accept の両方を要求する。
- `Ensure` は `flock` (`AcquireLock`, 5s) の下で health/version を確認し、healthy daemon を再利用する。stale socket/registry を消して `exec.Command(executable, "daemon")` を `Setsid` 付きで起動し、10s 以内に registry と socket が ready になるのを待つ。固定 socket を同時に起動しないための lock であり、daemon は caller から detached。
- `runDaemon` は Unix RPC server、HTTP server、30s maintenance ticker を起動してから registry を書く。defer で HTTP `Shutdown` 5s、listener close、registry/socket remove を行う。
- `StopWithWatchWarning` は live PID の watch scope を警告するだけで、daemon stop は続行する。`Stop` は registry PID に SIGTERM、最大 10s 待機後 stale socket/registry を clear する。live watch は接続失敗後 Ensure するため、daemon stop 後に再起動し得る。
- `internal/daemonctl/watchreg.go:RegisterWatchScoped` は `~/.atct/watchers/<pid>` に `{pid,project_id,goal_id,started_at}` JSON を書く。cleanup は自分の file だけ消す。
- `ReapWatches` は dead PID の registration を削除し、同じ `(project,goal)` scope の live watch のうち、registration order が自分より古いものだけ SIGTERM して 5s 待つ。異なる scope は触らない。legacy plain-PID registration、invalid start time、self registration を読めない場合は live process を kill しない。
- watch registration の liveness は `ProcessAlive` の PID check のみで、process start time と照合しない。PID reuse の誤認余地がある。一方 claim 側の `internal/store/claim_liveness.go:claimIsRunning` は `agent_sessions(pid,started_at)` と `processStartedAt` の一致まで確認する。`processStartedAt` は Linux の `ps` / `/proc` fallback (`process_linux.go`)、Darwin の `SysctlKinfoProc` (`process_darwin.go`) を使う。identity が無い claim は `claimIsDefinitelyDead` で reclaim しない。

## Codex の既存挙動で維持すべきもの

リポジトリが現在提供している Codex 契約は次の範囲である。

- ordinary interactive Codex は `.mcp.json` の HTTP `/mcp` を通じて ATCT MCP tools を使う。`atct_session_identify`、`atct_role`、claim/task/decision/handoff tools の名称、入力 ID の numeric/string 受理、response の canonical numeric ID を変えない。
- Codex には現状 `atct watch` の自動 attach、Codex stdout/stderr interception、parent process supervision、answer injection はない。`skills/start/SKILL.md` と `skills/stop/SKILL.md` も Codex に Claude Monitor/TaskStop を試さないよう指定している。
- `codex exec` 専用の production path はなく、リポジトリはその command の child process、exit code、stdio、transcript を管理していない。従って新しい monitor を暗黙に追加して existing `codex exec` の stdio/終了挙動や MCP `/mcp` session を変えないことが現状維持条件になる。実際の Codex CLI runtime/App Server protocol はこの repo からは検証できない。
- `cmd/atct-mcp/main.go` の stdio shim と `internal/daemon/server.go` の HTTP MCP endpoint は別入口だが、いずれも最終的に同じ daemon Unix RPC/MCP tool contract に到達する。新しい monitor がこの共有経路を置き換える根拠は現状にない。

## 設計へ渡す制約（判断は含めない）

1. 既存の Codex CLI/App Server attach point は無く、repo 内で使えるのは汎用 CLI/MCP/HTTP/SSE の境界だけである。
2. HTTP MCP の `run.register` は Codex client の PID/CWD を持たず、stdio shim だけが自分の PID/CWD を渡す。agent session identity、watch process identity、Codex parent/child identity は現在同一ではない。
3. SSE event bus は durable queue ではなく、buffer 16・non-blocking drop・replay 無し。snapshot recovery は unapplied decision に限定される。
4. action-worthy 粒度は現在 `watchScopeFilter` にあり、server の URL filter とは別。さらに `-project` は parse されるが `runWatch` に未接続である。
5. daemon stop/start は fixed per-user socket、version check、watch reconnect を前提にする。watch orphan cleanup は scope + registration time + PID の既存 semantics を壊さない必要がある。
6. claim cleanup は process start time を含む厳密な liveness、watch cleanup は PID-only という非対称性がある。
7. `go.mod` に Codex SDK/App Server client や `agmsg` は無く、今回の調査でも依存追加はしていない。

## focused test seams

- Watch loop/formatter: `cmd/atct/watch_test.go` の `watchSnapshotFunc`, `watchEnsureFunc`, `watchRoundTripper`, `cancelOnOutput`, `watchLoopWithEnsure...`。`TestWatchEnsuresDaemonAfterConnectionFailure`, `TestWatchStopsEnsuringAfterFiveFailures`, `TestWatchReadsSnapshotAfterDisconnect`, `TestWatchReportsReconnectWhileUnavailable` が retry/fail-open、`TestWatchPassesGoalIDToEvents` と `TestWatchPassesMatchingProjectIDToEvents` が query、`TestEmitWatch...` 群が formatting/dedup を固定する。
- Scope/delivery: `cmd/atct/watch_scope_test.go` は goal/project/pass-through predicate、default/unapplied/task-vs-goal filtering、unknown event を検査する。`cmd/atct/wakeup_delivery_test.go` は rendered wakeup の unchanged suppression、per-watch state、A→B→A を検査する。
- SSE server: `internal/httpapi/server_test.go` の `newTestServer`, `openSSEStream`, `readSSEFrame`, `assertSSEDecision`。`TestSSEFiltersDecisionEventsByProjectID`, `TestSSEFiltersDetectionEventsByGoalID`, `TestSSEPublishesEvaluateFailureForGoalSubscription`, `TestSSEGoalIDPublishesKeepaliveButNotWakeup`, `TestSSEPublishesGenericWakeupAndKeepaliveEvents`, `TestSSEPublishesAllDecisionTransitionsWithExactPayloads` が server filter/payload/keepalive の seam である。
- Maintenance/detection: `internal/daemon/wakeup_test.go` は `newWakeupTracker`, `runMaintenanceWith`, injected `detect`, in-memory `SubscribeEvents` を使い、grace、resend、discrepancy、evaluate failure、detection cleanup を検査する。`internal/daemon/task_handoff_test.go` は `handoff.yielded` event、`detection_realdata_test.go` は実 DB copy に対する detection を検査する。
- MCP/entry: `cmd/atct-mcp/main_test.go` の `resolveAtctPath` tests、`internal/mcpshim/client_test.go`、`schema_test.go`、`input_id_test.go`、`notifications_test.go` が Unix RPC、28 tools、session identify/role、ID schema、unapplied envelope を検査する。
- Lifecycle/orphan: `internal/daemonctl/ensure_test.go` は start/reuse/stale/unresponsive/version/concurrent start、`registry_test.go` は PID/socket health、`stop_test.go` は stop warning と scoped watch registration/reap（dead/live/duplicate/legacy/order）を検査する。
- Process identity: `internal/store/claim_liveness_test.go` は PID reuse、missing/unreadable start time、running/stale claims、`internal/store/process_proc_test.go` は Linux `/proc/<pid>/stat` の nested command と tick conversion を検査する。

この調査では production code を変更していない。
