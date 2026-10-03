# Goal 248: ack socket path が既定 TMPDIR で 104 byte を超える

## 問題
Goal 248 の ack テストは unix socket を `t.TempDir()` 配下に作る。macOS の既定 TMPDIR
（`/var/folders/...`）と長いテスト名で path が 104 byte に届き、`bind: invalid argument` で
`go test ./...` が落ちる。対象: TestAckTransportSendsAcceptedWireToUnixListener、
TestCodexMonitorAcknowledgementWireSuppressesBridgeAction、
TestCodexMonitorAcknowledgementsServeAuthenticatesWireRecords、
TestCodexMonitorCommanderAckEnvironmentIsTUIOnlyAndCleanedUp。

本番の `newCodexMonitorAckRuntime` の失敗は `ackRuntime, _ =`（codex_monitor_supervisor.go）で
捨てられ、通知抑制が黙って無効になる。

## 決定
1. テスト: socket を置く dir は `os.MkdirTemp("", "a")` ではなく、短い固定 base
   （`/tmp`）から作る helper `shortSocketDir(t)`（`os.MkdirTemp("/tmp", "a")` + `t.Cleanup(RemoveAll)`）を
   各 package の test に 1 つ置いて使う。TMPDIR に依存させない。
2. 本番: path の退避は作らない。ack socket は `<monitorDir>/<pid>.a` で、同じ dir の
   App Server socket `<pid>.sock` より 3 byte 短い。ack が上限に当たる環境では App Server
   socket が先に失敗するので、退避を足しても到達できる状況が増えない。代わりに
   `newCodexMonitorAckRuntime` の失敗を `deps.stderr` に 1 行で出し、抑止が無効であることを見えるようにする。
   動作（抑止無効のまま起動を続ける）は変えない。
3. flaky TestCodexMonitorDirectResolutionSkipsMarkedShim: 原因を調べ、テスト側の待ちが
   負荷に弱いなら直す。直せなければ再現条件を報告する。

## 範囲外
本番の socket path の退避、App Server socket の path 長対策。
