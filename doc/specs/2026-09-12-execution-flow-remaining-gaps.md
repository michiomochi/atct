# execution-flow 残差分の設計

## 目的

`doc/execution-flow.md` の未完了の差分 2・9・10 を実装する。handoff を受ける
agent は受領時に stable session key を確定し、タスクの実装は必ず subcommander から
executor への task handoff を経由する。タスク handoff の公開名は `task` を含むものだけにする。

## 非目標

- review / plan handoff の状態機械、人間レビュー、monitor の割当規則を変更しない。
- SQLite に残る既存 handoff 履歴を書き換えない。
- `atct_session_identify` を削除しない。再接続と診断で引き続き使える。

## 1. 受領で session key を確定する

`atct_task_handoff_receive` と `atct_goal_handoff_receive` は `session_key` を必須入力にする。
MCP shim は現在の transport session とその key を識別して canonical session を得てから、
その canonical session を handoff の受領者として保存する。receive の応答は今と同じく
role と claim evidence を返す。

`monitor_token` がある場合は既存の `atct_session_identify` による明示的な bind を残す。
receive は token bind の代替ではない。これにより receive だけで監視対象を推測したり、
token を session key として再利用したりしない。

識別または受領に失敗した場合は handoff を受領済みにしない。既存 key への再接続では
canonical session に受領を記録し、transport 用の空 session を所有者として残さない。

## 2. タスク handoff の公開 API を単一化する

公開 MCP tool は次の task 名だけにする。

- `atct_task_handoff_request`
- `atct_task_handoff_receive`
- `atct_task_handoff_review_request`
- `atct_task_handoff_review_receive`
- `atct_task_handoff_review_reject`
- `atct_task_handoff_review_reject_receive`
- `atct_task_handoff_complete`
- `atct_task_handoff_report_amend`

旧 `atct_handoff_*` tool と、対応する generic `handoff.*` RPC route を削除する。
既存の task-specific route は唯一の実装を呼ぶ。goal / plan handoff の公開 API は変更しない。
現行の運用文書、MCP schema test、wrapper contract、README の稼働手順は canonical 名へ更新する。

## 3. 自己 task claim を agent-facing API から外す

`atct_task_claim` と `atct_task_release`、および agent が呼べる `task.claim` /
`task.release` route を削除する。subcommander は task を作成して handoff request するだけで、
executor は handoff receive でのみ作業権限を得る。

store 内部の claim / release 実装は、人間による古い lock の回復や既存データの検証に必要な
限り残す。ただし agent-facing UI、MCP instructions、CLI context / pending の次アクションから
自己 claim を案内しない。unassigned task は subcommander が executor に handoff request
するまで `todo` のままとする。

## 検証

- key を持つ task / goal receive は canonical session を handoff の受領者にし、role evidence を返す。
- key が空、または識別に失敗する receive は handoff 状態を変えない。
- MCP tool 一覧に `atct_handoff_*`、`atct_task_claim`、`atct_task_release` がなく、task-specific tool は残る。
- generic `handoff.*` と agent-facing `task.claim` / `task.release` は未知 method として拒否される。
- subcommander は task handoff request を実行でき、executor の receive → review → complete は従来どおり動く。
- `go test ./... -count=1`、`bash tests/wrapper_test.bash`、`git diff --check` を通す。
