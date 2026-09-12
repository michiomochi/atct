# Bootstrap

ATCT の role は起動引数や agent の自己申告で選ばない。server が canonical session に紐づく
claim と handoff から導出する。起動時に必要なのは session と monitor を結び、導出済みの scope
だけを monitor へ渡すことである。

# Agent 共通

## 用語

| 用語 | 意味 |
| --- | --- |
| session key | harness の SessionStart が出す不透明な `session_id`。agent は変更せず identify に渡す。 |
| canonical session | session key に結び付いた ATCT の agent session。claim と handoff の所有者である。 |
| assignment | canonical session から導出した role と scope の組。role は commander / subcommander / executor。 |
| scope | commander の project、subcommander の goal、executor の task handoff。executor は複数 scope を持ち得る。 |

## 起動フロー

role を作るのは `session.identify` ではなく、project claim または handoff の受領である。monitor の接続方法は
harness ごとに異なるが、bind 後に監視を始める流れは共通である。

```mermaid
sequenceDiagram
    participant H as SessionStart hook
    participant A as Agent / MCP
    participant S as ATCT server
    participant M as Harness Monitor

    H->>A: session key を表示
    A->>S: session.identify(session key)
    S->>S: canonical session を確定
    S-->>A: canonical session
    alt /atct:start の commander
        A->>S: project.claim(project_id, force=true)
        S->>S: commander assignment を導出
        S-->>M: monitor.bind(project scope)
    else goal handoff を受ける subcommander
        A->>S: goal.handoff.receive(goal_id)
        S->>S: subcommander assignment を導出
        S-->>M: monitor.bind(goal scope)
    else task handoff を受ける executor
        A->>S: task.handoff.receive(handoff_id, task_id)
        S->>S: executor assignment を導出
        S-->>M: monitor.bind(task scope 群)
    end
    M->>S: bind 済み scope 群で watch / health / liveness を開始
```

`session.identify` は identity を結ぶだけで role を作らない。claim または handoff の受領が成功した応答で
assignment が更新され、server は monitor を再 bind する。agent は必要なら `atct_role` で導出結果を診断できる。

## assignment を作る起動操作

session を identify した直後に、起動元と受領者が次の操作を行う。handoff を作る側は新しい generic
monitor session を起動し、受領者に対象 ID を渡す。monitor の role / scope は渡さない。

| 起動する role | 起動元が先に行うこと | 起動した session が行うこと | bind 結果 |
| --- | --- | --- | --- |
| commander | — | `/atct:start`: `atct_goal_list` で project ID を得て、`atct_project_claim(project_id, force=true)` | project scope |
| subcommander | commander が `atct_goal_handoff_request` し、goal ID を渡す | `atct_goal_handoff_receive(goal_id)` | goal scope |
| executor | subcommander が `atct_task_handoff_request` し、handoff ID と task ID を渡す | `atct_task_handoff_receive(handoff_id, task_id)` | 受領済み task handoff ごとの scope |

project claim は commander を作る。goal / task handoff の request は受領者の assignment をまだ変えず、
受領者が receive したときにだけ変える。executor が後から別の task handoff を receive した場合も、server は
scope を追加して monitor を再 bind する。

## 原則

- role は server が導出する。CLI の `--role`、`--project`、`--goal`、`--task` で指定しない。
- authorization は server が canonical session から毎回判定する。monitor の scope は通知・health・liveness
  の配送先を決めるだけで、権限を与えない。
- executor に「一つの task」は仮定しない。open かつ受領済みの task handoff ごとに scope を持つ。
- `atct_role` は agent が role を診断する API として残す。通常の monitor bind の入力にはしない。

## assignment の導出

server は状態を次の順で評価する。

| role | 導出根拠 | scope |
| --- | --- | --- |
| commander | project claim を canonical session が保有する | その project |
| subcommander | open かつ受領済みの goal handoff を canonical session が保有する | その goal |
| executor | open かつ受領済みの task handoff を canonical session が保有する | 各 task handoff |

複数の根拠がある場合も server が明確な優先順位と scope 集合を返す。曖昧な task を client 側で一件だけ
選ばない。

## scope の更新

assignment は起動時だけの固定値ではない。project claim、goal / task handoff の receive・review・complete・
recovery で変化する。server は該当 canonical session の assignment を再計算し、紐付いた monitor へ
`monitor.bind` 更新を送る。

monitor は bind 前には agent action を配送せず、project 全体を仮の executor scope として監視しない。
bind 後に scope ごとの snapshot を取得してから event を処理するため、識別中の通知を取りこぼさない。
assignment が空になった monitor は health と liveness を停止し、次の bind 更新を待つ。

# Claude

Claude は通常の session として開始する。SessionStart hook が session key を表示し、agent は共通フローに従って
identify と assignment を確立する。

Claude Watch は assignment の scope に attach でき、通知を表示する。既に起動した session に後から attach
できる。server が assignment を更新したら Watch の scope も更新する。Claude 固有の接続方法は session key
だけで足り、monitor token は使わない。

# Codex

Codex は、monitor wrapper が新しい interactive process を起動する。通常の Codex process を後から monitor に
変えることはできない。

## monitor token

`atct codex monitor` は monitor token を生成し、子の Codex process に自動注入する。agent は token を表示・
判断せず、最初の `atct_session_identify` で session key とともに server へ渡す。server は token を使って、
session key だけでは特定できない起動済み monitor process と canonical session を結ぶ。

## 起動

Codex は常に次で起動する。

```sh
atct codex monitor -- <codex args>
```

`--role`、`--project`、`--goal`、`--task` は bootstrap 完了前に scope を固定してしまうため廃止する。
新しい session をこの入口から起動する。

## 通知

Codex Bridge は bind 済み scope の通知を queue へ入れ、Codex thread が idle になった時に turn input として渡す。
event / wakeup の条件と内容は Agent 共通であり、Claude Watch と異なるのは配送方法だけである。

# 現状との差

この文書は目標仕様であり、現状とは次の差がある。

| 範囲 | 現状 | 目標仕様への変更 |
| --- | --- | --- |
| Agent 共通 | `session.identify` は canonical session ID と再接続の有無だけを返す。assignment は `atct_role` または claim / receive の応答で別に確認する。 | identify 後の claim / receive を契機に server が assignment を再計算し、紐付いた monitor を bind する。 |
| Agent 共通 | monitor と canonical session を結ぶ server API、`monitor.bind`、bind 後の snapshot はない。 | monitor 登録・session との関連付け・bind 更新を server の責務として追加する。 |
| Claude | `atct watch --monitor -project` または `-goal` を agent が scope 指定して attach する。 | Watch は server 導出 assignment に attach し、scope の変更を server から受ける。 |
| Codex | 起動時に `--role` と `--project` / `--goal` / `--task` を解決して scope を固定する。指定しない場合は空の watch scope で開始する。 | 常に `atct codex monitor -- <codex args>` で起動し、wrapper が token を注入して server の bind を待つ。 |
| Codex | monitor token と agent への自動注入はない。 | token を `atct_session_identify` の入力に加え、起動済み monitor と canonical session を対応付ける。 |

移行時は `doc/continuous-execution.md`、`skills/atct/SKILL.md`、Claude Watch と Codex Bridge の実装を同じ
server bind 契約へ更新する。Stop hook は既に session key を server へ渡して role を解決しており、この移行で
role / scope の環境変数を追加しない。
