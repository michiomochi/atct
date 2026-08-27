# executor sandbox 実測

測定日時: 2026-08-27 17:59–18:12 JST
sandbox 設定: `sandbox_mode = "workspace-write"`
worktree: `/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/180`（`wt/goal-180`）

## A. 起動経路

| コマンド | 結果 | 出力（そのまま） |
| --- | --- | --- |
| `ls -la "$(which herdr)"` | 成功（exit 0） | <pre>lrwxr-xr-x@ 1 masayoshi.michikawa@kanmu.co.jp  staff  13 Jul  2 23:47 /Users/masayoshi.michikawa@kanmu.co.jp/.local/share/aquaproj-aqua/bin/herdr -> ../aqua-proxy</pre> |
| `herdr --version` | 成功（exit 0） | <pre>Aug 27 17:59:04.004 WRN update the last used datetime program=aqua version=2.57.0 env=darwin/arm64 exe_name=herdr package_name=ogulcancelik/herdr package_version=v0.8.0 registry=standard error="update the last used datetime: create a package timestamp file: open /Users/masayoshi.michikawa@kanmu.co.jp/.local/share/aquaproj-aqua/metadata/pkgs/github_release/github.com/ogulcancelik/herdr/v0.8.0/herdr-macos-aarch64/timestamp.txt: operation not permitted"
herdr 0.8.2</pre> |
| `"$HERDR_BIN_PATH" --version` | 成功（exit 0） | <pre>herdr 0.8.2</pre> |
| `herdr agent get atct-180-executor-1` | 失敗（exit 1） | <pre>Aug 27 18:12:59.267 WRN update the last used datetime program=aqua version=2.57.0 env=darwin/arm64 exe_name=herdr package_name=ogulcancelik/herdr package_version=v0.8.0 registry=standard error="update the last used datetime: create a package timestamp file: open /Users/masayoshi.michikawa@kanmu.co.jp/.local/share/aquaproj-aqua/metadata/pkgs/github_release/github.com/ogulcancelik/herdr/v0.8.0/herdr-macos-aarch64/timestamp.txt: operation not permitted"
Error: Os { code: 1, kind: PermissionDenied, message: "Operation not permitted" }</pre> |
| `"$HERDR_BIN_PATH" agent get atct-180-executor-1` | 失敗（exit 1） | <pre>Error: Os { code: 1, kind: PermissionDenied, message: "Operation not permitted" }</pre> |
| `./bin/atct --version` | 失敗（exit 2） | <pre>unknown subcommand "--version"
Usage: atct &lt;command&gt; [options]

Commands:
  daemon start          Start the daemon if it is not already running
  daemon stop           Stop the running daemon
  project add [name]   Register the current project
  project list         List registered projects
  goal add &lt;content&gt;   Create a goal for the current project
  goal list            List goals for the current project
  context [-brief]      Print the current goal context for an AI session
  pending              Print unanswered human decisions for the current project
  watch [-goal string]  Stream human decision events for a Monitor
  claim-check &lt;ids...&gt;|any  Exit 0 only if the tasks are claimed by a running session
  role                 Report the claim-derived role for an agent session
  handoff complete &lt;handoff-id&gt; &lt;task-id&gt;  Report a handoff complete
  handoff yielded &lt;task-id&gt;  Report that the worker yielded

Options:
  -listen string   HTTP listen address (default "127.0.0.1:8787")
  -project string  Select a registered project by name (context, pending)
  -expect string   Expected role for the role command
  -agent-session-id string  Session identity for the role command</pre> |
| `atct --version` | 失敗（exit 127） | <pre>zsh:1: command not found: atct</pre> |

シム経由で起動できるか: `--version` が答える。
サーバの socket へ繋がるか: `agent get` が答える。この 2 つは別の問いである。

## B. ビルドと整形

| コマンド | 結果 | 出力（そのまま） |
| --- | --- | --- |
| `go build ./...` | 成功（exit 0） | <pre>Aug 27 18:00:26.234 WRN update the last used datetime program=aqua version=2.57.0 env=darwin/arm64 exe_name=go package_name=golang/go package_version=go1.25.1 registry=standard error="update the last used datetime: create a package timestamp file: open /Users/masayoshi.michikawa@kanmu.co.jp/.local/share/aquaproj-aqua/metadata/pkgs/http/golang.org/dl/go1.25.1.darwin-arm64.tar.gz/timestamp.txt: operation not permitted"
go: writing stat cache: open /Users/masayoshi.michikawa@kanmu.co.jp/go/pkg/mod/cache/download/github.com/michiomochi/atct/@v/v0.57.1-0.20260827084405-2dc9efd0e8db.info849050110.tmp: operation not permitted</pre> |
| `go vet ./...` | 成功（exit 0） | <pre>Aug 27 18:00:31.762 WRN update the last used datetime program=aqua version=2.57.0 env=darwin/arm64 exe_name=go package_name=golang/go package_version=go1.25.1 registry=standard error="update the last used datetime: create a package timestamp file: open /Users/masayoshi.michikawa@kanmu.co.jp/.local/share/aquaproj-aqua/metadata/pkgs/http/golang.org/dl/go1.25.1.darwin-arm64.tar.gz/timestamp.txt: operation not permitted"</pre> |
| `gofmt -l .` | 成功（exit 0） | <pre>Aug 27 18:00:10.030 WRN update the last used datetime program=aqua version=2.57.0 env=darwin/arm64 exe_name=gofmt package_name=golang/go package_version=go1.25.1 registry=standard error="update the last used datetime: create a package timestamp file: open /Users/masayoshi.michikawa@kanmu.co.jp/.local/share/aquaproj-aqua/metadata/pkgs/http/golang.org/dl/go1.25.1.darwin-arm64.tar.gz/timestamp.txt: operation not permitted"</pre> |

`go` の各実行前に `export GOCACHE="${TMPDIR:-/tmp}/atct-gocache-180"` を実行した。

## C. パッケージ別のテスト

| コマンド | 結果 | 出力（そのまま） |
| --- | --- | --- |
| `go list ./...` | 成功（exit 0） | <pre>github.com/michiomochi/atct/cmd/atct
github.com/michiomochi/atct/cmd/atct-mcp
github.com/michiomochi/atct/internal/daemon
github.com/michiomochi/atct/internal/daemonctl
github.com/michiomochi/atct/internal/domain
github.com/michiomochi/atct/internal/e2e
github.com/michiomochi/atct/internal/httpapi
github.com/michiomochi/atct/internal/mcpshim
github.com/michiomochi/atct/internal/rpc
github.com/michiomochi/atct/internal/store
github.com/michiomochi/atct/internal/store/sqlcgen
github.com/michiomochi/atct/web</pre> |
| `go test github.com/michiomochi/atct/cmd/atct -count=1 -timeout 120s` | 失敗（exit 1） | <pre>--- FAIL: TestListenHTTPExplicitAddressDoesNotFallBack (0.00s)
    main_test.go:115: net.Listen("127.0.0.1:0") error = listen tcp 127.0.0.1:0: bind: operation not permitted</pre> |
| `go test github.com/michiomochi/atct/cmd/atct-mcp -count=1 -timeout 120s` | 成功（exit 0） | <pre>ok  	github.com/michiomochi/atct/cmd/atct-mcp	0.404s</pre> |
| `go test github.com/michiomochi/atct/internal/daemon -count=1 -timeout 180s` | 失敗（exit 1） | <pre>--- FAIL: TestDecisionPollForSubcommanderRefusesOtherGoalDecision (0.04s)
    decision_poll_scope_test.go:76: daemon.Serve exited before socket appeared: listen atct-decision-scope-2526568463/daemon.sock: listen unix atct-decision-scope-2526568463/daemon.sock: bind: operation not permitted</pre> |
| `go test github.com/michiomochi/atct/internal/daemonctl -count=1 -timeout 120s` | 失敗（exit 1） | <pre>--- FAIL: TestEnsureStartsDaemonWhenAbsent (10.04s)
    ensure_test.go:98: Ensure: the daemon did not become ready in time after 10s; see /var/folders/jc/218_rmj13m1fp32zsd2t16w00000gn/T/atct1062395197/daemon.log</pre> |
| `go test github.com/michiomochi/atct/internal/domain -count=1 -timeout 120s` | 成功（exit 0） | <pre>ok  	github.com/michiomochi/atct/internal/domain	0.529s</pre> |
| `go test github.com/michiomochi/atct/internal/e2e -count=1 -timeout 120s` | 失敗（exit 1） | <pre>--- FAIL: TestFullFlowThroughDaemonAndHTTP (3.03s)
    full_flow_test.go:352: daemon socket /var/folders/jc/218_rmj13m1fp32zsd2t16w00000gn/T/atct-e2e423071807/atct.sock did not become available</pre> |
| `go test github.com/michiomochi/atct/internal/httpapi -count=1 -timeout 120s` | 失敗（exit 1） | <pre>--- FAIL: TestHTTPWithdrawActiveGoalRejectsDroppedGoal (0.04s)
panic: httptest: failed to listen on a port: listen tcp6 [::1]:0: bind: operation not permitted [recovered, repanicked]</pre> |
| `go test github.com/michiomochi/atct/internal/mcpshim -count=1 -timeout 120s` | 失敗（exit 1） | <pre>--- FAIL: TestGoalListResponseIncludesUnappliedDecisions (0.00s)
    notifications_test.go:17: Listen: listen unix /var/folders/jc/218_rmj13m1fp32zsd2t16w00000gn/T/atct3724650071/daemon.sock: bind: operation not permitted</pre> |
| `go test github.com/michiomochi/atct/internal/rpc -count=1 -timeout 120s` | 成功（exit 0） | <pre>?   	github.com/michiomochi/atct/internal/rpc	[no test files]</pre> |
| `go test github.com/michiomochi/atct/internal/store -count=1 -timeout 120s` | 成功（exit 0） | <pre>ok  	github.com/michiomochi/atct/internal/store	13.783s</pre> |
| `go test github.com/michiomochi/atct/internal/store/sqlcgen -count=1 -timeout 120s` | 成功（exit 0） | <pre>?   	github.com/michiomochi/atct/internal/store/sqlcgen	[no test files]</pre> |
| `go test github.com/michiomochi/atct/web -count=1 -timeout 120s` | 成功（exit 0） | <pre>?   	github.com/michiomochi/atct/web	[no test files]</pre> |

同じ worktree で他の作業が同時に走っていると、sandbox とは無関係の失敗が混ざる。

## D. sandbox の境界

| コマンド | 結果 | 出力（そのまま） |
| --- | --- | --- |
| `touch "${TMPDIR:-/tmp}/atct-180-probe" && rm -f "${TMPDIR:-/tmp}/atct-180-probe"` | 成功（exit 0） | <pre></pre> |
| `touch "$HOME/.atct/.probe-180"` | 拒否（exit 1） | <pre>touch: /Users/masayoshi.michikawa@kanmu.co.jp/.atct/.probe-180: Operation not permitted</pre> |
| `curl -sS -m 5 -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8787/mcp` | 失敗（exit 7） | <pre>curl: (7) Failed to connect to 127.0.0.1 port 8787 after 0 ms: Couldn't connect to server
000</pre> |
| `curl -sS -m 5 -o /dev/null -w '%{http_code}\n' https://example.com` | 失敗（exit 6） | <pre>curl: (6) Could not resolve host: example.com
000</pre> |

## E. MCP

| コマンド | 結果 | 出力（そのまま） |
| --- | --- | --- |
| `atct_session_identify(session_key="atct-180-executor-1")` | 成功 | <pre>{"data":{"agent_session_id":2686,"reattached":false}}</pre> |
| `atct_handoff_receive(task_id=717)` | 成功 | <pre>{"data":{"CompleteReport":"","CompletedReportAt":null,"ID":"goal-180-task-717-exec1-01","ReceivedAt":"2026-08-27T08:58:48.969684Z","ReceivedBy":2686,"RequestReport":"Codex の workspace-write sandbox で、この worktree の検証コマンドが通るかを 1 本ずつ実測し、表を doc/investigations に残す。調査のみ。","RequestedAt":"2026-08-27T08:57:23.873331Z","RequestedBy":2673,"TaskID":717}}</pre> |
| `atct_role(expected_role="executor")` | 成功 | <pre>{"data":{"does":["implement","test","close the task it was given"],"does_not":["make design decisions","re-delegate","commit","write internal version-control details"],"expected_role":"executor","matches":true,"role":"executor"}}</pre> |

## 結論

executor が実行してよい検証コマンド:

- `go test github.com/michiomochi/atct/cmd/atct-mcp -count=1 -timeout 120s`
- `go test github.com/michiomochi/atct/internal/domain -count=1 -timeout 120s`
- `go test github.com/michiomochi/atct/internal/rpc -count=1 -timeout 120s`
- `go test github.com/michiomochi/atct/internal/store -count=1 -timeout 120s`
- `go test github.com/michiomochi/atct/internal/store/sqlcgen -count=1 -timeout 120s`
- `go test github.com/michiomochi/atct/web -count=1 -timeout 120s`
- aqua のシム（`$(which herdr)` / `go` / `gofmt`）は worktree 外への timestamp 書き込みを拒否されるが、警告のみで実行は成功する。警告を失敗と読むな。

executor が実行できない検証コマンド:

- `go test github.com/michiomochi/atct/cmd/atct -count=1 -timeout 120s` — bind
- `go test github.com/michiomochi/atct/internal/daemon -count=1 -timeout 180s` — socket
- `go test github.com/michiomochi/atct/internal/daemonctl -count=1 -timeout 120s` — socket
- `go test github.com/michiomochi/atct/internal/e2e -count=1 -timeout 120s` — socket
- `go test github.com/michiomochi/atct/internal/httpapi -count=1 -timeout 120s` — bind
- `go test github.com/michiomochi/atct/internal/mcpshim -count=1 -timeout 120s` — socket
- `$HOME/.atct/` へのファイル作成、localhost 接続、および DNS 解決を必要とする外部 HTTP リクエスト。
