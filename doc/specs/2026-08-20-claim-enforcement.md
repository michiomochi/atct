# 他人のタスクを勝手に done にできるのを直す

日付: 2026-08-20
ゴール: 他人のタスクを勝手に done にできる

## 解く問題

`internal/store/queries/task.sql:27` の `UpdateTaskStatus` は `WHERE id = ?` だけで
`claimed_by` を見ていなかった。**どの agent session でも他人のタスクを done にできた。**

`claim` が守っていたのは claim 自体だけで、作業は守っていなかった。

## 決定

### 引き取りの経路を潰さないことが要点

**`todo` への戻しを保持者限定にしてはいけない。** v0.24.0 で出した理由がこう指示している。

```
A task claimed by another agent session is no longer running.
You can take it over by returning it to todo with `atct_task_update`, then claim it.
```

**claim を持たない人が `todo` に戻せることが前提の文言である。** 単純に保持者限定にすると、
出したばかりの引き取り経路が消える。

### 操作ごとに許す相手を分ける

| 操作 | 誰が許されるか |
|---|---|
| `done` / `dropped` | **claim の保持者だけ** |
| `todo`（解放） | 保持者、**または claim が実行されていないとき誰でも** |
| `doing` | claim 経由のみ（変更しない） |
| claim が無いタスク | 誰でも（変更しない） |

「実行されていない」の判定は **`store.ClaimLiveness`** を使う（v0.24.0）。
**判定を 2 箇所に書かない。** 同じ判定が 2 箇所にあったせいで、同日に `goal.tasks` と
`decision_history.task_id` の 2 件を落としている。

### 操作者の ID を渡す

`UpdateTask` の署名に `agentSessionID` を足す。**誰が操作しているかを渡さずに
保持者判定はできない。** 呼び出し側（`internal/daemon/handler.go`）は
`p.AgentSessionID` を渡す。これは MCP のリクエストに乗っている値である。

### エラーは何をすればよいかを言う

拒否したときのエラー文に「なぜ拒否されたか」と「代わりに何をすればよいか」の両方を入れる。
同日、v0.12.0 の CHECK 制約で生の SQL エラーが人間の画面に出た事例がある。同じ形を作らない。

実装された文面:

```
task "<id>" is claimed by another agent session; only the claim holder can set it
to done; if that session is no longer running, return it to todo with
atct_task_update, then claim it before retrying

task "<id>" is claimed by another agent session that is still running; wait for it
to finish or stop, then return it to todo with atct_task_update and claim it
```

### 判定中に claim が変わったら拒否する（実装者の判断）

commander は指定していなかったが、`ClaimLiveness` を呼ぶ間に claim が変わる競合がある。
**黙って通さずエラーにして再試行を促す。** 妥当な判断なので採用した。

## 検証

- **否定側**: 他人が `done` にしようとして拒否される
- **否定側**: 他人が `dropped` にしようとして拒否される
- **否定側**: **実行中の claim を他人が `todo` に戻せない**
- 肯定側: **放置された claim なら他人が `todo` に戻せる**（引き取りの経路）
- 肯定側: 保持者自身は `done` にできる
- 肯定側: claim が無いタスクは誰でも変えられる

**肯定側の 3 つが重要である。** これが通ることで、引き取りの文言が壊れていないと分かる。

### RED の落ち方から分かったこと

`done` の拒否を外しても**拒否自体は消えず**、`ClaimLiveness` の経路に落ちて
「実行中だから待て」という別のエラーになった。テストが**文言まで検査していた**ので捕まった。

```
error "...claimed by another agent session that is still running..."
does not contain "only the claim holder"
```

**文言を見ていなければ、この壊し方は通り抜けていた。** 保持者でない者が `done` にはできないが、
理由が違う状態になる。二重の防御は偶然だが、**エラー文言を検査する価値はここに出た。**

## やらないこと

- `tasks.claimed_by` の名前と中身を変えない
- `atct_task_claim` の挙動を変えない
- `doing` への遷移の経路を変えない
