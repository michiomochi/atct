# Goal 215 Implementation Plan

**Goal:** bridge 非対応 notification が Codex monitor を無効化しないようにする。

**Architecture:** 共通 params decode から thread status を外し、status decode を `thread/status/changed` の method branch へ局所化する。notification queue test に remoteControl の scalar status を追加し、既存 idle status の queue 進行 assertion を維持する。

## Constraints

- 変更対象は `cmd/atct/codex_monitor.go` とその対応テストだけ。
- decoder の許容範囲を広げない。未知通知を method dispatch で no-op にする。
- TDD: regression test を先に追加して failure を確認してから最小実装を行う。

### Task 1: regression test と最小 bridge 修正

**Files:**

- Modify: `cmd/atct/codex_monitor_test.go` (`TestCodexMonitorQueueDeliversFIFOAfterIdle`)
- Modify: `cmd/atct/codex_monitor.go` (`(*codexMonitorBridge).HandleNotification`)

1. queue に 2 turn を置いた既存テストで、最初の `turn/completed` の前または後に `remoteControl/status/changed` / matching threadId / scalar `status: "disabled"` を送る assertion を追加する。pre-fix では `decode Codex remoteControl/status/changed notification` で fail することを確認する。
2. params の共通 struct から `Status codexThreadStatus` を外す。`thread/status/changed` case 内で params status を `codexThreadStatus` へ decode し、decode error は同じ notification decode error として返す。
3. focused test を実行する: `go test ./cmd/atct -run '^TestCodexMonitorQueueDeliversFIFOAfterIdle$' -count=1`。
4. 対象 package を実行する: `go test ./cmd/atct -count=1`。
5. subcommander が diff、focused test、package test を独立にレビューする。
