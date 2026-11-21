# Goal 248 ack socket path 修正 plan

Spec: doc/specs/2026-10-03-goal-248-ack-socket-path.md

1 task（executor）:
1. 失敗する 4 テストの socket dir を短い helper に替える。まず既定 TMPDIR で再現（赤）を確認してから直す。
2. `newCodexMonitorAckRuntime` の失敗を `deps.stderr` に出す。テストを 1 つ足す:
   `deps.listenUnix` が失敗を返すとき commander monitor の stderr に出力があり、TUI env に
   ack 変数が付かず、終了コードは変わらないこと。
3. TestCodexMonitorDirectResolutionSkipsMarkedShim の flaky を調べる。
4. 既定 TMPDIR のまま `go test ./cmd/atct ./cmd/atct-mcp -count=1` と
   `go test -race ./cmd/atct ./cmd/atct-mcp -count=1`、`go vet ./...`。

subcommander が task 受領後に、既定 TMPDIR で `go test ./... -count=1`、wrapper_test.bash を実行して確認する。
