# 役割が引けないときに答えをどこまで渡すか（ゴール 156・2026-08-28）

**変更は `internal/daemon/handler.go` と `internal/daemon/unapplied_decision_scope_test.go` の 2 ファイル。**

## 何が壊れていたか

`38df3fc`（他ゴールの答えを subcommander の応答から外す）は、**役割が引けたときにだけ効く。**

    internal/daemon/handler.go  unappliedDecisionsForSessionInProject
        role, _ := d.deriveSessionRole(ctx, agentSessionID)
        if role.Role == "subcommander" && role.GoalID != 0 {
            return d.store.ListUnappliedDecisionsForGoal(ctx, role.GoalID)   // 絞る
        }
        return d.store.ListUnappliedDecisionsForProject(ctx, projectID)      // project 全体

`deriveSessionRole` は、project を claim していないセッションと、
受領済み goal handoff を持たないセッションに `executor` を返す。**MCP の再接続で
セッション ID が入れ替わると、subcommander もここに落ちる**（`doc/specs/2026-08-25-session-id-swap.md`）。
落ちた瞬間から、**応答には project 全体の未適用の答えが付く。**
2026-08-26 の実測では `atct_task_update` 3 回と `atct_decision_poll` 1 回で
他ゴールの決定が返り続けた。poll すれば他ゴールの答えを applied にできた。

## ゴール 176 との切り分け

**実測された 4 回はすべてツール応答の `unapplied_decisions` に現れた。**
この封筒を作る経路は `unappliedDecisionsForSessionInProject` の 1 本だけであり、
起動時スナップショット（SessionStart の文脈注入）を通らない。
**したがってこの 4 回の原因はここ（fail-open な既定）である。**
起動時スナップショットの絞り込み漏れは、同じ「他ゴールが見える」症状を
**別の窓**（起動文脈）で作る。**症状が同じでも窓が違うので、両方直す必要がある。**
このゴールはツール応答の窓だけを直す。

## 呼び手の棚卸し（完了条件 (3)）

`ListUnappliedDecisionsForProject` の呼び手は `unappliedDecisionsForSessionInProject`
の 1 箇所だけである。そこへ入ってくる経路は 2 群に分かれ、**群ごとに正しい既定が違う。**

### 群 A: 通知封筒（`unapplied_decisions`）— 絞るべき

無関係なツール応答の末尾に押し付ける通知である。受け手は「自分に関係がある」と読む。
`responseWithScopedUnappliedDecisions` 経由が 10 箇所、`unappliedDecisionsForSession` 直呼びが 2 箇所。

| 経路 | メソッド |
|---|---|
| `responseWithScopedUnappliedDecisions` | `goal.update_content` / `task.update_content` / `task.declare` / `task.update` / `decision.ask`（3 箇所）/ `decision.poll` / `decision.withdraw` / `goal.set_derived_from` / `goal.complete` |
| `unappliedDecisionsForSession` 直呼び | `goal.claim` / `task.claim` |

**すべて要求そのものが goal を指している**（`goal_id`、または task から引いた `goal_id`、
`decision.withdraw` は決定の `goal_id`）。**絞る材料は既に引数に入っている。**

### 群 B: 復帰経路（`goal.list` の `orphaned_decisions`）— 絞らないのが正しい

`goal.list` の返り値は、**セッション ID が入れ替わって自分の答えを拾えなくなった者の
唯一の復帰経路である**（mcpshim の instructions が「Answers from an earlier session arrive as
`orphaned_decisions`」と案内している）。**入れ替わった者は、定義上、役割が引けない。**
ここを fail-closed にすると、**復帰させたい相手にだけ何も返らなくなる。**
だから `goal.list` は project 全体を返す。**ただし黙って返さない。**

## 決定

**群 A は「要求されたゴール」で絞る。群 B は project 全体を保ち、絞れていないことを申告する。**

    commander                   -> project 全体（変更なし。commander は project を見る必要がある）
    subcommander（goal あり）   -> 自分のゴール（変更なし）
    それ以外（入れ替わり・executor・セッションなし）
        群 A -> 要求されたゴールだけ（goal_id が 0 なら何も返さない）
        群 B -> project 全体 + 申告

### なぜ「何も返さない」(①) にしないか

**入れ替わった commander が黙って答えを失う。**沈黙は「答えが無い」と区別できない。
今日の実測はまさに「入れ替わったことは呼び手に見えない」ことだった。
**見えない失敗を、別の見えない失敗に置き換えるだけである。**

### なぜ「申告だけ」(②) にしないか

**申告は読まれて初めて効く。**ゴール 154 が「規則を書いても届かない」を実測している。
群 A の漏れは、**読まなくても消える形で消せる**——要求が既にゴールを指しているのだから、
それで絞ればよい。**申告は、絞れない群 B にだけ使う。**

### なぜ「関わったゴール全部」(③) にしないか

**入れ替わったセッションは `goal_handoffs` に自分の名前を持たない**（旧 ID で入っている）。
③ は入れ替わりに対して①と同じ挙動（何も返らない）になり、判定を 1 段複雑にして
得るものが無い。

### 要求されたゴールで絞ることの利点

**executor にも正しく効く。**executor は 1 タスク＝1 ゴールで働くが、
今までは project 全体を受け取っていた。要求で絞れば、executor の漏れも同時に消える。

## 応答の形（群 B の申告）

`goal.list` の `data` に 1 フィールド追加する。既存フィールドは変えない。

    "orphaned_decisions_scope": {
      "scope": "project",          // "project" | "goal"
      "goal_id": 0,                // scope が "goal" のときだけ
      "role": "executor",          // 引けた役割
      "note": "role could not be derived; ..."   // 絞れていないときだけ
    }

`note` が入るのは **`scope` が `"project"` かつ `role` が `"commander"` でないとき**、
つまり fail-open している瞬間だけである。commander には `note` が入らない。

## 検査

**入れ替わりは検査で直接作る**（完了条件 (4)）。実際の再接続を待たない。
goal A の handoff を受領した subcommander セッションと別に、
**project も claim せず handoff も受領していない新しいセッション ID** を作れば、
`deriveSessionRole` は `executor`（`project_id` も `goal_id` も空）を返す。これが入れ替わり後の状態である。

| 検査 | 主張 |
|---|---|
| 入れ替わりセッションが goal A に `task.update` | 封筒は決定 A だけ（決定 B が来ない）= (1) |
| 入れ替わりセッションが goal B に `task.update` | 封筒は決定 B だけ（固定値でなく要求に従う） |
| 入れ替わりセッションが `goal.list` | `orphaned_decisions` は A と B、`orphaned_decisions_scope.note` が入る = (1) の申告側 |
| commander が `task.declare` / `goal.list` | A と B（既存検査を変えない）= (2) |
| commander が `goal.list` | `orphaned_decisions_scope.note` が入らない |
| セッションなしで `task.declare` | 要求ゴールだけ（`TestTaskDeclareWithoutSessionKeepsProjectWideDecisions` を置き換える） |

**既存検査 1 件の主張が変わる。**`TestTaskDeclareWithoutSessionKeepsProjectWideDecisions` は
「セッションが無ければ project 全体」を固定していた。**これが直す対象そのものなので、
`TestTaskDeclareWithoutSessionScopesToRequestGoal` に置き換える。**
