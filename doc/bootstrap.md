# Bootstrap

ATCT の role は起動引数や agent の自己申告で選ばない。server が canonical session に紐づく
claim と handoff から導出する。起動時に必要なのは session と monitor を結び、導出済みの scope
だけを monitor へ渡すことである。

## 用語

| 用語 | 意味 |
| --- | --- |
| session key | harness の SessionStart が出す不透明な `session_id`。agent は変更せず identify に渡す。 |
| canonical session | session key に結び付いた ATCT の agent session。claim と handoff の所有者である。 |
| assignment | canonical session から導出した role と scope の組。role は commander / subcommander / executor。 |
| monitor token | `atct codex monitor` が起動時に作る不透明な識別子。monitor と canonical session を結ぶ。 |
| scope | commander の project、subcommander の goal、executor の task handoff。executor は複数 scope を持ち得る。 |

## 原則

- role は server が導出する。CLI の `--role`、`--project`、`--goal`、`--task` で指定しない。
- authorization は server が canonical session から毎回判定する。monitor の scope は通知・health・liveness
  の配送先を決めるだけで、権限を与えない。
- executor に「一つの task」は仮定しない。open かつ受領済みの task handoff ごとに scope を持つ。
- `atct_role` は agent が role を診断する API として残す。通常の monitor bind の入力にはしない。

## 起動フロー

```mermaid
sequenceDiagram
    participant M as atct codex monitor
    participant H as SessionStart hook
    participant A as Agent / MCP
    participant S as ATCT server

    M->>S: monitor token を登録（未 bind）
    M->>A: Codex を起動
    H->>A: session key を表示
    A->>S: session.identify(session key, monitor token)
    S->>S: canonical session を確定し assignment を導出
    S-->>A: canonical session と assignment
    S-->>M: monitor.bind(token, scope 群)
    M->>S: scope 群で watch / health / liveness を開始
```

agent が role を知る時点は `session.identify` の成功応答時である。server はその応答に assignment を
含める。agent は必要なら `atct_role` で同じ導出結果を診断できる。

monitor は session key を推測しない。起動時に作った monitor token を SessionStart / MCP 経路で
`session.identify` へ渡し、server が token と canonical session を結ぶ。

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

## Codex の起動

Codex は常に次で起動する。

```sh
atct codex monitor -- <codex args>
```

`--role`、`--project`、`--goal`、`--task` は bootstrap 完了前に scope を固定してしまうため廃止する。
通常の Codex process を後から monitor に変えることはできない。新しい session をこの入口から起動する。

## 現状との差

現状の `session.identify` は canonical session の関連付けだけを返し、role / scope を返さない。agent は
続けて `atct_role` を呼ぶ。Codex monitor は role と scope を起動引数で解決し、未指定の monitor は
unscoped watch になる。

この文書の bootstrap は未実装の目標仕様である。実装後は `doc/continuous-execution.md` と
`skills/atct/SKILL.md` の旧起動形式をこの仕様へ更新する。
