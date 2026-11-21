# ゴール 172 調査報告: session の project 紐付け

調査日: 2026-08-27

対象は `agent_sessions.project_id` を `run.register` 時に決める変更のコード調査。実装・生成コードの更新・既存データの変更は行っていない。ゴール 156 の本文は読んでいない。

## 結論

- 既存の cwd → project 解決は `Store.ResolveProject(ctx, cwd)` にある。worktree を主リポジトリへ正規化し、`projects.root_path` の最長一致で解決するため、新しい解決経路は不要。
- 現在の `run.register` は `pid` だけを受け取り、`RegisterAgentSession` の INSERT は `project_id = NULL`。stdio の `cmd/atct-mcp` は cwd を持つが、HTTP の `server.go` はクライアントの cwd を知らない。
- `ensureAgentSessionProject` の 11 箇所は、NULL の補填だけでなく「別 project なら拒否」も担う。登録時に補填しても 11 箇所すべての越境拒否が残るため、依頼書の基準で不要は 0 件。
- `deriveSessionRole` と未適用 decision の scope は `agent_sessions.project_id` を読まない。NULL により role が導出できない、という因果は現コードにはない。
- `project_id` は既に nullable な外部キーであり、登録時に解決できた場合だけ埋める設計なら migration は不要。NOT NULL 化を別途行うなら SQLite の table rebuild と既存 NULL 行の扱いが必要。

## A. cwd から project を解決する既存経路

`internal/store/project.go:137` の `NormalizeRoot(ctx, path)` は `normalizeWorktreePath` を通す。`git -C <cwd> rev-parse --show-toplevel` 等で worktree の主リポジトリを求め、主リポジトリと git dir が異なる場合も同じ project root に寄せる。最後に `normalizeProjectPath` が trailing slash を除き、可能なら symlink を評価する。

`internal/store/project.go:161` の `Store.ResolveProject(ctx, cwd)` は上記正規化後、`internal/store/queries/project.sql:16-21` の `root_path = cwd OR cwd LIKE root_path || '/%'` を root path 長の降順で実行する。したがって nested project がある場合も最長の root が選ばれる。未登録なら `store.ErrProjectNotFound`。

同じ経路の利用箇所:

- `internal/daemon/handler.go:355-380` の `resolveOrRegisterProject` は、解決に失敗したとき `NormalizeRoot` → `CreateProject` まで行う。これは `goal.list` 用の自動 project 登録で、単なる解決とは挙動が違う。
- `cmd/atct/context.go:219-234` の `resolveProjectSelection` は、明示的な project 名がなければ `Store.ResolveProject` を呼ぶ。
- `project.create` の handler も root path を `NormalizeRoot` してから作成する。
- `project.resolve` という RPC は存在しない。現状の project 解決 API は store 内の `ResolveProject`。

従って `run.register` からは `ResolveProject(ctx, cwd)` を呼べる。未登録 cwd を NULL のまま成功させる決定なら、`ErrProjectNotFound` だけを「未紐付け」として扱い、`resolveOrRegisterProject` は呼ばない。後者を呼ぶと登録時に新しい project 行を作るため、仕様が変わる。

## B. `run.register` の全経路と cwd の可視性

本番コード（テストを除く）で `run.register` を発行しているのは次の 2 箇所。handler/store/generated code は受信・実装側であり、発行元ではない。

| 発行元 | 現在の payload | cwd | 調査結果 |
| --- | --- | --- | --- |
| `cmd/atct-mcp/main.go:57` (`main`) | `pid: os.Getpid()` | `os.Getwd()` で取得可能 | `cwd` を追加できる唯一の現行 stdio 発行元。 |
| `internal/daemon/server.go:66` (`(*Daemon).HTTPHandler`) | `pid: os.Getpid()` | daemon の `r` はあるが、クライアントの OS cwd はない | 現在の HTTP/MCP リクエストに cwd header/field を読む処理はない。ここで `os.Getwd()` を呼んでも daemon 起動時の cwd であり、HTTP クライアントの cwd ではない。 |

受信側は `internal/daemon/handler.go:503-512`。`run.register` の params は `pid` のみで、store の `RegisterAgentSession(ctx, pid)` に直結する。`cmd/atct` には `run.register` の直接発行はない。CLI は `addGoal` / `listGoals` 等で cwd を個別 RPC に渡し、daemon 自体の起動 cwd を session 登録に使う処理もない。

`session.identify` の reattach は `internal/store/store.go:86-154`。stable key が既存 row に見つかると、その canonical row の process identity だけを更新し、既存 row の `project_id` は保持する。新しく transport 用に作った row の `project_id` を canonical row へコピーする処理も、canonical row が NULL の場合に解決する処理もない。したがって、登録時に canonical 候補へ project を設定できれば reattach 後の role/claim は既存の canonical project と整合する。なお reattach で不要になった新 transport row は project NULL のまま残り得る。

HTTP 経路で登録時の cwd を成立させるには、既存にない HTTP/MCP の cwd 受け渡し契約（header 等）を決める必要がある。これはコードから一意に決まらない返却事項である。`internal/mcpshim/tools.go:18-20,513-520` の `atct_goal_list` は tool 引数として cwd を持つが、登録時の HTTP handshake より後なので代替にはならない。

## C. `ensureAgentSessionProject` 11 箇所の棚卸し

対象は `internal/daemon/handler.go:291-320`。session が NULL/未関連なら target project を設定し、既に別 project なら scope violation を返す。下表の target は各 RPC の入力から計算されるため、登録時 cwd の project と常に同じとは限らない。

| # | handler 行 / RPC | targetProjectID の由来 | 登録時に埋まった後の判定 | 不要か |
| ---: | --- | --- | --- | --- |
| 1 | 575 `project.claim` | `p.ProjectID` そのもの | 任意の project を claim しようとした session の越境を拒否 | いいえ |
| 2 | 820 `goal.claim` | `GetGoal(p.GoalID).ProjectID` | goal ID が別 project なら拒否 | いいえ |
| 3 | 863 `goal.update_content` | `GetGoal(p.GoalID).ProjectID` | goal の project scope を拒否 | いいえ |
| 4 | 896 `task.update_content` | `GetTaskGoalID` → `GetGoal` → `ProjectID` | task/goal の project scope を拒否 | いいえ |
| 5 | 924 `task.declare` | `GetGoal(p.GoalID).ProjectID` | 別 project の goal への task 宣言を拒否 | いいえ |
| 6 | 952 `task.update` | `ProjectIDForTask(p.TaskID)` | task project と session project の不一致を拒否 | いいえ |
| 7 | 1009 `task.claim` | `ProjectIDForTask(p.TaskID)` | 別 project の task claim を拒否 | いいえ |
| 8 | 1191 `decision.ask` | `GetGoal(p.GoalID).ProjectID` | decision の goal project の越境を拒否 | いいえ |
| 9 | 1199 `decision.ask`（task 指定時） | `ProjectIDForTask(p.TaskID)` | task と goal の入力が別 project の場合も拒否。#8 と同じ project になる保証を store 側は検証していない | いいえ |
| 10 | 1355 `goal.set_derived_from` | `GetGoal(p.GoalID).ProjectID` | derived-from 対象 goal の project scope を拒否 | いいえ |
| 11 | 1390 `goal.complete` | `GetGoal(p.GoalID).ProjectID` | completion の claim/handoff 認可とは別に project scope を拒否 | いいえ |

つまり新規 session の通常経路では association branch が不要になるだけで、mismatch branch は必要。既存 NULL row、未登録時代の row、または異なる project の entity ID を受けた場合にも guard が必要なので、11 箇所を削除する根拠はない。

## D. role / decision scope と project_id の関係

`internal/daemon/handler.go:156-190` の `deriveSessionRole` は、`ListProjects` の `project.ClaimedBy == agentSessionID` なら commander、受領済み goal handoff の holder なら subcommander、それ以外は executor とする。`ProjectIDForAgentSession` も `agent_sessions.project_id` も呼ばない。`decision.poll`（handler.go:1275 付近）はこの role で subcommander の decision が保持 goal 外かを検査するだけで、session project は見ない。

`unappliedDecisionsForSessionInProject`（handler.go:219-228）は subcommander かつ `GoalID != 0` のとき `ListUnappliedDecisionsForGoal`、それ以外は `ListUnappliedDecisionsForProject`。後者の `projectID` は session row から来ず、`unappliedDecisionsForSession`（handler.go:230-236）が `GetGoal(goalID).ProjectID` から渡す。`goal.list` では cwd 解決結果 `ns.ID`（handler.go:633,739）が渡される。

従って NULL が原因で role が導出できない、という goal 156 と同じ根はコード上確認できない。project-wide へ fail-open する分岐の直接原因は、session が commander claim も受領済み goal handoff も持たず role が executor になること（または session ID が 0）であり、`project_id` NULL ではない。

一方、project_id を読む箇所は別にある。

- `internal/daemon/handler.go:608` の `project.release` は `ProjectIDForAgentSession` が `ErrAgentSessionNotAssociated` になると `isProjectBound=false` とし、holder でない限り拒否する。
- `internal/store/task.go:605` の task lock release も同じ project-bound 判定をする。NULL session は live owner / terminal status の通常の解除を bypass しない。holder、同じ project に bound、または owner が dead で stale lock を戻す場合だけ既存条件に従って進む。
- `internal/store/run.go:18-36` の `ProjectIDForAgentSession` は NULL を `ErrAgentSessionNotAssociated` として返す。

## E. schema / migration / sqlc

`schema.sql:10-17` の `agent_sessions.project_id` は `INTEGER REFERENCES projects(id)` で `NOT NULL` ではない。既存の index も `project_id, registered_at` 用にあるため、解決できた project を INSERT 時に指定するだけなら schema 変更も migration も不要。解決できない cwd は NULL のままにでき、既存 2213 行の backfill/drop も不要。

runtime migration は `internal/store/migrations.go:17-18,543-581,584-635` の `//go:embed migrations/*.sql` と連番検証で適用される。現行 SQL migration は `0020_drop_legacy_ids.sql` までで、`schemaVersion=6`（store.go:21）は歴史的な SQLite user_version であり、migration 番号とは別である。

もし将来 `project_id NOT NULL` を採るなら SQLite の `ALTER COLUMN` では足りず、`0018_integer_primary_keys.sql:20-32,208-238` や `0019_integer_agent_session_ids.sql:29-42,123-143` と同じ table rebuild（new table → copy → drop → rename → index 再作成）が必要。現存する NULL 行を deterministic に project へ割り当てるか、移行を拒否する必要があり、削除を伴う設計はこの調査の範囲外。

`sqlc.yaml` は `schema.sql` と `internal/store/queries` を入力に `internal/store/sqlcgen` を生成する。`internal/store/queries/task.sql:91-95` と生成済み `internal/store/sqlcgen/task.sql.go:732-756` の `RegisterAgentSession` は共に `VALUES (NULL, ?, ?, ?)` / 3 引数を持つ。atomic に project_id を INSERT する実装で query の引数を変えるなら、生成 output の params/SQL も同期が必要。ただし依頼の禁止に従い `go tool sqlc generate` は実行していない。既存の `AssociateAgentSessionWithProject` を register 後に呼ぶ二段階案なら query/generated output を変えずに済むが、INSERT と association は別操作になる。

## F. 実装時の変更候補とファイル行数

行数は調査時点で `wc -l` を実行した結果。nullable のまま、atomic に登録する案を前提にした production の候補は次の通り。

| file (`wc -l`) | 関数 / 内容 |
| --- | --- |
| `cmd/atct-mcp/main.go` (71) | `main`: `os.Getwd()` を取り、run.register payload に cwd を追加。 |
| `internal/daemon/server.go` (222) | `(*Daemon).HTTPHandler`: HTTP 側で cwd を受け取る契約を採る場合のみ変更。現状は client cwd を取得できない。 |
| `internal/daemon/handler.go` (1415) | `dispatch` の `run.register`: cwd を parse、`ResolveProject` の結果を登録処理へ渡す。未登録時の `ErrProjectNotFound` は NULL 継続。`ensureAgentSessionProject` 11 箇所は残す。 |
| `internal/store/store.go` (217) | `RegisterAgentSession`: project ID を INSERT 層へ渡す API または wrapper。`IdentifyAgentSession` は canonical project を保持するため変更不要。 |
| `internal/store/queries/task.sql` (235) | atomic 案なら `RegisterAgentSession` の INSERT に project_id parameter を追加。 |
| `internal/store/sqlcgen/task.sql.go` (984) | 上記 query を使う限り generated params と SQL の同期が必要。自動生成は禁止中なので実装時に扱いを決める。 |

再利用するだけで変更不要な候補は `internal/store/project.go` (265; `NormalizeRoot`, `ResolveProject`)、`internal/store/run.go` (55; `ProjectIDForAgentSession`)、`schema.sql` (144)、`internal/store/migrations.go` (996)。`cmd/atct/main.go` (705) は run.register 発行元ではなく、`internal/mcpshim/tools.go` (788) は goal.list の cwd を既に持つため、登録契約を新設しない限り変更不要。

store API の既存 `RegisterAgentSession(ctx, pid)` の signature 自体を変更する場合は、production 以外にも直接 caller がある。wrapper/new method で既存 API を残すなら不要。変更する場合の直接 test caller と `wc -l` は以下。

| file (`wc -l`) | file (`wc -l`) |
| --- | --- |
| `cmd/atct/context_test.go` (917) | `cmd/atct/delegated_task_display_test.go` (76) |
| `cmd/atct/pending_test.go` (1770) | `internal/daemon/goal_handoff_test.go` (304) |
| `internal/daemon/server_test.go` (1962) | `internal/daemon/session_test_helpers_test.go` (53) |
| `internal/e2e/full_flow_test.go` (766) | `internal/mcpshim/schema_test.go` (1158) |
| `internal/store/agent_session_test.go` (508) | `internal/store/claim_liveness_test.go` (451) |
| `internal/store/goal_handoff_test.go` (699) | `internal/store/goal_test.go` (746) |
| `internal/store/project_test.go` (430) | `internal/store/task_handoff_test.go` (731) |
| `internal/store/task_test.go` (1825) | `internal/store/wakeup_test.go` (1111) |

## G. テスト配置

- `run.register`: `internal/daemon/run_test.go:15` の `TestDaemonRegistersAgentSessionAndAssociatesItWithGoalProject` は現在「登録直後は NULL、goal.list 後に project」を検証しているため、登録時 cwd の成功と未知 cwd の NULL に変更・追加する主要箇所。`:84` の `TestRunRegisterAllocatesSequentialAgentSessionID` は ID 採番回帰用。HTTP handshake の登録契約を追加するなら `internal/daemon/web_test.go:158-463`（MCP initialize/tool call）も対象。
- role: 実装は `internal/daemon/handler.go:156`。`internal/daemon/handler_test.go:63` の `TestSessionRoleDerivesFromClaims`、`internal/daemon/unapplied_decision_scope_test.go:294` の `TestSessionRoleUnchangedAfterExtractingHelper`、`internal/daemon/server_test.go:554` の `TestSessionIdentifyReattachesProjectClaimForRole` が project_id 非依存と reattach の回帰を担う。
- `ProjectIDForAgentSession`: 実装は `internal/store/run.go:18`、store 単体は `internal/store/agent_session_test.go:208` の `TestProjectIDForAgentSession`。project/task release の bound/mismatch 回帰は `internal/daemon/handler_test.go:437,492,563,625`。
- `ensureAgentSessionProject` の越境拒否と未適用 decision scope は `internal/daemon/unapplied_decision_scope_test.go:267-350` と `internal/daemon/handler_test.go:347-659` が中心。登録時に埋めても cross-project rejection が残ることをテストする必要がある。

## 検証

- `go build ./...` は既定の build cache が sandbox 外だったため一度失敗した。その後、`GOCACHE=/private/tmp/atct-172-go-build-cache go build ./...` を実行し、exit code 0（成功）。module cache の aqua 警告は出たが build の終了は成功だった。
- 依頼書の禁止に従い `go test ./...` は実行していない。

## 返却事項 / blocker

HTTP MCP には client cwd を登録時に運ぶ既存フィールドがない。stdio は `cmd/atct-mcp` から cwd を渡せるが、HTTP は header 等の transport 契約を決めない限り登録時解決できない。未登録 project の自動作成をするか（`resolveOrRegisterProject`）、NULL のままにするかも混同しないこと。その他の調査対象は指定文書に記録済み。
