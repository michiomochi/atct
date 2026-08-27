# goal-127: ensureAgentSessionProject 呼び出し箇所の調査

調査対象は `internal/daemon/handler.go` の `handle` 内で、定義行
（handler.go:291）を除く `ensureAgentSessionProject` の11呼び出しである。
行番号は `grep -n 'ensureAgentSessionProject' internal/daemon/handler.go` で引き直した。

ここで「呼べてしまう」は、`ensureAgentSessionProject` の前段に保持者認可がなく、後続の
store 呼び出しまで到達できるという意味である。後続 store 関数自身の競合・状態チェックは
前段の保持者判定には数えていない。また、`agent_session_id == 0` なら
`ensureAgentSessionProject` は即時に成功し、非0で未関連付けのセッションなら対象 project
への関連付けを行うため、project claim や goal/task handoff の保持者確認にはならない。

## 1. 11箇所の一覧

| # | case ラベル | 行番号 | 呼ぶ前の保持者判定 | 判定が見ている関数・フィールド | 判定が無い場合、その case を呼べてしまうのは誰か |
|---:|---|---:|---|---|---|
| 1 | `case "project.claim"` | 543 | なし | なし | 現在の project claim 保持者でない任意のセッション（`agent_session_id == 0` または未関連付けを含む）が呼び出し、claim 試行まで到達できる。 |
| 2 | `case "goal.claim"` | 788 | なし | なし | 対象 project の claim 保持者でも対象 goal handoff の受領者でもない任意のセッションが呼び出し、goal claim 試行まで到達できる。 |
| 3 | `case "goal.update_content"` | 831 | なし | なし | 対象 goal handoff の受領者でない任意のセッション（`agent_session_id == 0` または未関連付けを含む）が呼び出し、goal content 更新まで到達できる。 |
| 4 | `case "task.update_content"` | 864 | なし | なし | 対象 goal handoff の保持者でない任意のセッションが呼び出し、task content 更新まで到達できる。 |
| 5 | `case "task.declare"` | 892 | なし | なし | 対象 project の claim 保持者でない任意のセッションが呼び出し、task declaration まで到達できる。 |
| 6 | `case "task.update"` | 920 | なし | なし | 対象 task handoff の保持者でも project claim 保持者でもない任意のセッションが呼び出し、task status 更新まで到達できる。 |
| 7 | `case "task.claim"` | 977 | なし | なし | 対象 task handoff の保持者でない任意のセッションが呼び出し、task claim 試行まで到達できる。 |
| 8 | `case "decision.ask"` | 1159 | なし | なし | 対象 goal handoff の受領者でない任意のセッションが呼び出し、decision 作成まで到達できる。 |
| 9 | `case "decision.ask"` | 1167 | なし | なし | 対象 goal/task の handoff 保持者でない任意のセッションが呼び出し、decision 作成まで到達できる。 |
| 10 | `case "goal.set_derived_from"` | 1323 | なし | なし | 対象 goal handoff の受領者でない任意のセッションが呼び出し、derived-from 更新まで到達できる。 |
| 11 | `case "goal.complete"` | 1355 | なし | なし | 対象 goal handoff の受領者でない任意のセッションが呼び出し、completion report 作成まで到達できる。 |

`GetGoal`、`GetTaskGoalID`、`ProjectIDForTask` は対象 goal/task/project を解決するだけで、
project の `ClaimedBy` や goal/task handoff の `ReceivedBy` を比較していない。そのため、
これらが直前にあっても表では保持者判定を「なし」とした。

## 2. 参考実装: `case "project.release"`

`handler.go:552-585` は `ensureAgentSessionProject` を使わず、次の2つの認可条件を
独立に調べて OR で許可している。

1. `d.store.ListProjects(ctx)` を呼び、`project.ID == projectID` の行について
   `project.ClaimedBy == agentSessionID` を比較する。これが `isHolder` であり、
   project の現在の claim 保持者かを見る。
2. `d.store.ProjectIDForAgentSession(ctx, agentSessionID)` を呼び、戻り値
   `callerProjectID` と要求された `projectID` を比較する。エラーがなく、
   `callerProjectID == projectID` なら `isProjectBound` とする。

実際の条件は次の形である。

```go
isHolder := project.ID == projectID && project.ClaimedBy == agentSessionID
callerProjectID, sessionProjectErr := d.store.ProjectIDForAgentSession(ctx, agentSessionID)
isProjectBound := sessionProjectErr == nil && callerProjectID == projectID
if !isHolder && !isProjectBound {
    // deny
}
```

`isHolder` はデータ上の project claim の所有者を確認し、`isProjectBound` は
agent session の project 関連付けを確認する。両者は別の関係なので、どちらか一方を
満たせば release を許可する2本立てになっている。なお `agent_session_id == 0` はこの
case の冒頭で拒否されるため、`ensureAgentSessionProject` の0 bypassとは異なる。

## 3. `case "task.release"` は `ensureAgentSessionProject` を呼ばない

`handler.go:996-1005` の `case "task.release"` は、直接
`d.store.ReleaseTaskAs(ctx, p.TaskID, p.AgentSessionID)` を呼ぶ。

`ReleaseTaskAs`（`internal/store/task.go:750-753`）は次の委譲だけを行う。

```go
return s.UpdateTask(ctx, taskID, domain.TaskTodo, agentSessionID)
```

`UpdateTask`（task.go:385-397）は `TaskTodo` への遷移なので `openTaskHandoff` を取得し、
`authorizeTaskStatusRelease`（task.go:546-579）に認可を渡す。保持者判定は次の順である。

- open な task handoff があれば、その `ReceivedBy` と `agentSessionID` を比較する。
  一致すれば `isHolder` として許可する。
- 一致しなければ `ProjectIDForTask` で task の project を引き、
  `ProjectIDForAgentSession` の戻り値と比較する。同じ project に bound なら許可する。
- それでも該当せず、handoff の `ReceivedBy` が0または保持セッションが
  `claimIsRunning` で生存中なら拒否する。保持セッションが停止済みなら、`TaskTodo` への
  release は許可する（`TaskDone`/`TaskDropped` ではこの停止済み保持者によるフォールバック
  は許可されない）。

したがって `task.release` は handler の project scope 自動関連付けではなく、store 内で
handoff の `ReceivedBy`、task project、session project binding、保持者の liveness を
使って保持者・同一 project の caller・停止済み保持者を区別している。

## 4. goal handoff の保持者を照会できる store 関数

`GoalHandoff` の保持者判定に使う主なフィールドは `GoalID`、`RequestedBy`、
`ReceivedBy`、`RequestedAt`、`ReceivedAt`、`CompletedReportAt` である。関数は次のとおり。

| 関数 | シグネチャ | 戻り値と判定への使い方 |
|---|---|---|
| `openGoalHandoff`（非公開） | `func (s *Store) openGoalHandoff(ctx context.Context, goalID int64) (*GoalHandoff, error)` | 受領済み（`ReceivedAt != nil`）かつ未完了（`CompletedReportAt == nil`）の単一 handoff を返す。該当なしは `nil`、複数は `ErrGoalHandoffAmbiguous`。store package 内部からのみ使える。 |
| `GetGoalHandoff` | `func (s *Store) GetGoalHandoff(ctx context.Context, handoffID string) (GoalHandoff, error)` | handoff ID で1件の `GoalHandoff` を返す。`GoalID` が対象と一致すること、`ReceivedBy` と `ReceivedAt` が受領済みかを確認できる。 |
| `ListGoalHandoffs` | `func (s *Store) ListGoalHandoffs(ctx context.Context, goalID int64) ([]GoalHandoff, error)` | 指定 goal の全 handoff（partial row を含む）を返す。各行の `ReceivedBy`/`ReceivedAt` と `CompletedReportAt` を見て受領済み・未完了を判定できる。 |
| `ListOpenGoalHandoffs` | `func (s *Store) ListOpenGoalHandoffs(ctx context.Context) (map[int64]*GoalHandoff, error)` | 全 goal の `completed_report_at IS NULL` 行を goal ID キーの map で返す。request-only（`ReceivedAt == nil`）も含むので、受領済み判定には `ReceivedAt != nil` を追加で見る。 |
| `ListGoalSessions` | `func (s *Store) ListGoalSessions(ctx context.Context, goalID int64) ([]GoalSession, error)` | `GoalSession{SessionKey string, Role string, HandoffOpen bool}` の一覧を返す。数値の `agent_session_id` ではなく session key で照会する補助 API で、goal handoff と task handoff のセッションを統合して返す。 |

SQL 側の対応は次のとおり。

- `queries/goal_handoff.sql:1-7` の `ListOpenGoalHandoffs :many` は、
  `goal_handoffs` から `completed_report_at IS NULL` の行を全列（`requested_by`、
  `received_by`、各 timestamp を含む）取得する。
- sqlc 生成関数 `sqlcgen.(*Queries).ListOpenGoalHandoffs`
  （`sqlcgen/goal_handoff.sql.go:75-105`）の戻り値は
  `([]GoalHandoff, error)` であり、store の `ListOpenGoalHandoffs` が
  `map[int64]*GoalHandoff` に変換する。
- `queries/goal_handoff.sql:9-31` の `ListGoalSessionKeys :many` は、goal handoff の
  `received_by` と `agent_sessions` を join し、さらにその goal 配下の task handoff も
  統合する。生成関数 `sqlcgen.(*Queries).ListGoalSessionKeys`
  （`sqlcgen/goal_handoff.sql.go:43-64`）の戻り値は
  `([]ListGoalSessionKeysRow, error)`。row は `SessionKey string`、`Role string`、
  `HandoffOpen int64` で、store が `[]GoalSession` に変換する。

`ListOpenGoalHandoffs` の store 戻り値は明示的に
`map[int64]*GoalHandoff`（キーは `GoalID`）である。handler 側の
`goalHandoffClaimedBy`（`handler.go:94-102`）は次の実装である。

```go
func goalHandoffClaimedBy(handoff *store.GoalHandoff) int64 {
    if handoff == nil {
        return 0
    }
    if handoff.ReceivedBy != 0 {
        return handoff.ReceivedBy
    }
    return handoff.RequestedBy
}
```

既存の role 判定（handler.go:175-182）は、`handoff != nil`、
`handoff.ReceivedAt != nil`、`goalHandoffClaimedBy(handoff) == agentSessionID` の3条件を
併用する。つまり `goalHandoffClaimedBy` 単独ではなく、`ReceivedAt` を見ることで
request-only handoff を「受領済み」と誤認しない。

## 5. project claim 保持者の照会 API

project ID を引数にして claim 保持者だけを直接返す公開 `Store` 関数は、
`internal/store/project.go` にはない。`ListProjects(ctx) ([]domain.Project, error)` が
`domain.Project.ClaimedBy` を返す直接の一覧 API である。

補足すると、`ResolveProject(ctx, cwd) (domain.Project, error)` も戻り値の
`domain.Project.ClaimedBy` を埋めるが、これは cwd から project を解決する関数であり、
project ID を直接引く API ではない。また `ClaimProject` は claim 操作の結果として
`domain.Project` を返す。`ClaimProject` 内で sqlc の `GetProject(ctx, id)` は使われるが、
project.go の公開 read-only Store API としては露出していない。

`ProjectIDForAgentSession` は session の project 関連付けを返すだけで、project の
`ClaimedBy`（claim 保持者）を返す照会ではない。したがって、project ID から保持者を
直接確認する既存の公開手段は `ListProjects` の `ClaimedBy` を走査する方法であり、
`project.release` の `isHolder` もその方法を使っている。
