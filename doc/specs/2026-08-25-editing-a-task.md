# 宣言したタスクを直す

ゴール 577e8da5。実測は 2026-08-22（稼働版 0.42.0）と 2026-08-25（commander の 5 件）。

## 問題

宣言したタスクの `title` / `description` / `files` を直す経路が無い。

    クエリ   UpdateTaskDescription        無い
    RPC      task.update_description      無い
    MCP      atct_task_update_description 無い

`goal.update_content`（141d71dc で入れた）と同じ形の欠陥がタスク側に残っている。

そのうえ再宣言は黙って無視される。`internal/store/queries/task.sql` の `CreateTask` が

    ON CONFLICT(goal_id, declare_key) DO NOTHING

なので、同じ `idempotency_key` で宣言し直してもエラーも更新も起きない。
**「やり直した」と思ったまま古い説明が残る。**これがいちばん悪い形である。

## 実測

2026-08-25、commander がこの問題を 5 回踏んだ。446d87f0 / 6d326601 / 8ad49303 /
340866f0 / 4fa268b2 の `description` が古くなり、毎回 handoff の依頼書で上書きした。
atct に残る記録は誤ったまま。

**とくに 8ad49303 は因果そのものが誤りだった。**

    ps 拒否 → realProcessStartedAt が err → claimIsRunning が false
      → requireProjectClaimForGoal が必ず拒む
      → goal.handoff.request が必ず失敗

MCP は daemon 側で走るので、executor の sandbox で `ps` が拒まれても
`goal.handoff.request` は失敗しない。宣言時に因果を 1 本たどらず、
症状から推測して書いたのが原因である。**記録は誤ったまま 2 本の handoff を通った。**

## 決めたこと

### 1. 書き換えを許す status は `todo` と `doing`

稼働 DB での実測（2026-08-25）:

| 測ったもの | 結果 |
|---|---|
| 受領済み・未完了の handoff を持つタスクの status | `todo` 1/1 |
| 全タスクの status 分布 | done 545 / dropped 38 / todo 26 / doing 1 |
| 上の 5 件の handoff 8 本の時系列 | すべて直列（前の完了後に次を request） |

**handoff の受領はタスクを `doing` にしない。**executor は claim ではなく handoff で
所有するので、作業中のタスクは `todo` のままである。よって commander が実際に踏んだ
5 件はすべて `todo` で、`todo` だけでも作業中の訂正は覆える。

`doing` を足すのは、自分で claim して進める経路（`ClaimTask`）のためである。
実測では 610 件中 1 件しかないが、そこだけ直せないのは説明がつかない。

`done` は拒む。**完了報告の根拠が後から変わるため**で、49ee01d8
「done のゴールにもう一度完了報告を出せてしまい、承認済みの文章が黙って上書きされる」
と同じ形の欠陥になる。`dropped` も拒む。落とした理由の記録が動くのは同じ害である。

### 2. `title` と `files` も同じ経路で直せる

ゴールの実測で誤っていたのは**ファイルパス**（`files` と `description` の両方に出る）
だった。説明だけ直せてタイトルが直せないと、次に同じ差し戻しが起きる。

3 つを 1 本の RPC で部分更新する。**省略した項目は変えない。**3 つとも省略は拒む
（何も変えない呼び出しが成功を返すと、呼んだ側は直ったと思う）。

**ツール名は `atct_task_update_content` とする。**ゴール本文は
`atct_task_update_description` と書いているが、`title` と `files` も直せる以上その名前は
実体と合わない。兄弟の `atct_goal_update_content` と形をそろえる。

### 3. 再宣言は `DO NOTHING` のまま、無視したことを返す

`CreateTask` のコメントが書いているとおり、この制約は**圧縮後の再宣言でタスクが
増えるのを止めるため**にある。

- `DO UPDATE` に変えると、圧縮後の再宣言が `done` のタスクの説明を黙って書き換える。
  決定 1 で拒んだものが declare 経由で通ってしまう
- 衝突をエラーにすると、その再宣言の冪等性そのものが壊れる

よって `DO NOTHING` は残し、**`task.declare` の応答に「この宣言で作られたか」を
入れる。**無視されたと分かれば、呼んだ側は `atct_task_update_content` に回れる。

## 経路

`goal.update_content` と同じ 3 段。

    internal/store/queries/task.sql   UpdateTaskContent
    internal/store/task.go            (*Store).UpdateTaskContent
    internal/daemon/handler.go        case "task.update_content"
    internal/mcpshim/tools.go         atct_task_update_content

スキーマは変えない。`title` / `description` / `files` の列は既にある。移行は要らない。

## 拒み方

エラーに**実際の status を入れる。**

    task 7851d1e3 is done, not todo or doing

`goal.update_content` を写すとここで落とす。`internal/daemon/handler.go:643` は
`store.ErrGoalNotProposed` を daemon 側の sentinel に差し替えていて、**ID も status も
落ちる。**`internal/rpc/rpc.go:14` の `Error` は文字列なので、包んだまま返せば
そのまま MCP の呼び出し側へ届く。**差し替えないこと。**

## 検査

- **status ごとに 2 件以上のフィクスチャを置く。**1 件だけだと「常に通す」実装でも通る
- 否定側を必ず置く。`done` と `dropped` で拒まれること、**かつ拒否メッセージに実際の
  status が入っていること**を見る
- 部分更新は、省略した項目が変わらないことを見る
- 再宣言は、同じ `idempotency_key` で 2 回宣言して 2 回目が「作られていない」と
  返すことを見る。**行数が増えないことだけを見ると、返り値の欠落を見逃す**
- **破壊検証が skip で終わったものは検証されていない。**skip で終わったら
  subcommander が sandbox の外で測り直す

## 範囲外

- web UI からタスクを直す経路（`internal/httpapi` と `web/`）。MCP が通ってから別のゴールで
- `done` のタスクを直す経路。決定 1 で拒むと決めた
