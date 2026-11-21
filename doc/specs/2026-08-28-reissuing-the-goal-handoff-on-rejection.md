# 却下されたら goal handoff を再発行する

ゴール 183。

## 問題

完了報告の正常なフローはこうである。

    1. atct_goal_complete           kind=completion の Decision を作る
    2. atct_goal_handoff_complete   goal handoff を閉じる

人間が `reject` を選ぶと、ゴールは active のままで作業が再開する。**しかし handoff は
閉じたままである。**役割は「受領済みで未完了の goal handoff を持っているか」から導かれる
（`internal/daemon/handler.go` の `deriveSessionRole`）ので、**subcommander の役割は
executor に落ち、コミットできなくなる。**

復旧には project claim が要るため commander の再発行が必要になる。**却下 1 回につき
往復 1 回。**2026-08-27 は 1 日で 3 回起きた。

## 決定

**却下の瞬間に、閉じた goal handoff を同じ受領者へ再発行する（受領済みの状態で）。**

ゴール本文の選択肢のうち A を採る。却下は人間の通常操作であり、その 1 回ごとに別の
エージェントの介入を要求する設計は人間の操作を重くする。B（完了報告と handoff 完了の
1 操作化）は範囲が広く、C（手順の正式化）は往復が残る。

### 誰が保持者に戻るか

**却下前の受領者に戻す。**新しい保持者を選ばない。具体的には、却下された Decision の
`agent_session_id` と `received_by` が一致する、完了済み handoff のうち
`completed_report_at` が最も新しいものを選び、その `requested_by` / `received_by` を
そのまま引き継いだ行を新規に作る。

**既存の行を書き換えない**（`completed_report_at` を NULL に戻さない）。完了報告の記録が
消えるうえ、履歴が「一度も閉じなかった」ように見える。append-only の新しい行にすれば、
却下と再発行が記録に残る。これは commander が手でやっている再発行と同じ形である。

新しい行の ID は `<元の handoff ID>-reopen-<decision ID>`。却下 1 回につき 1 つに決まる。

`goal_handoffs` には `WHERE completed_report_at IS NULL` の一意索引があるので、
**同時に開ける handoff は 1 つだけ**という既存の不変条件はそのまま守られる。

### 何もしない場合

次のいずれかなら再発行しない。無言で成功させる（却下そのものは成立させる）。

- **ゴールに既に開いている handoff がある** — 別の誰かが既に保持者である。奪わない
- **Decision の `agent_session_id` に一致する完了済み handoff が無い** — 報告者が handoff を
  持っていない（commander が自分で完了報告を出した場合など）。復元すべき保持者が居ない
- **Decision が `kind=completion` でない** — 却下の意味が違う

### 原子性

`RejectCompletion` を 1 つのトランザクションにまとめる。**`RejectGoal`
（`internal/store/goal.go`）が既に同じ形をしている**——`BeginTx` → 更新 → `Commit` →
コミット後に `publish`。これに合わせる。Decision の応答だけが成立して再発行が落ちる状態を
作らない。

イベントの発火は現状の `answerDecision` と同じ 3 つを commit 後に出す
（`publish(decisionID)` / `publishAll()` / `Event{Name: "decision.rejected"}`）。

### 順序を逆にした事故について

ゴール本文の 3 件目は `atct_goal_handoff_complete` を先に呼んだ事例で、原因が別である。
この設計は**却下が来た時点で**救う。人間が却下も承認もしない間の停止は残る。
それは B の範囲なので、`next_steps` に書いて分ける。

## 役割が落ちる引き金は 4 つある。救うのは 1 つだけである

2026-08-27 から 08-28 の実測が 9 件ある（atct 側 6 件・dotfiles 側 3 件）。

| 引き金 | handoff | 復旧手段 |
|---|---|---|
| daemon の再起動（**版が上がったかとは無関係**） | 開いたまま | `atct_session_identify` の呼び直しで戻る |
| **transport とセッションキーの対応が失われる**（原因未測定） | 開いたまま | `atct_session_identify` の呼び直しで戻る |
| 完了報告の却下 | 閉じたまま | **戻らない。commander の再発行が要る** |
| `atct_goal_handoff_complete` の順序誤り・executor の誤呼び出し | 閉じたまま | **戻らない。commander の再発行が要る** |

**2 行目は範囲外とする。**handoff が閉じていないので、この設計が触る場所を通らない。
原因が未測定なので範囲に入れると先に測ることになり、1 日 3 回起きている却下側の修正が
遅れる。**既に `atct_session_identify` で戻る**（dotfiles が実測）。

このゴールの作業中にも 1 件踏んだ。`atct_session_identify` が 2963 を返した直後の
`atct_task_claim` が、**`session_key` が空の新しい agent_session 2981** に紐づき、
以後の `atct_handoff_request` が落ちた。**DB に匿名セッション行が増えている**ので、
「transport ごとにセッションが作られている」側の傍証になる。decision 485 に記録した。

**この設計が救うのは 3 行目だけである。**1・2 行目は既に回復手段がある。4 行目は
**閉じたことを知らせる出来事が無い**ので、開き直す引き金が取れない。**却下されていない
handoff を勝手に開かないことは、このゴールの完了条件 4 が明示的に禁じている。**
機構で誤った close を止めるのはゴール 192（proposed）の仕事であり、192 が入っても
却下と daemon 再起動は close を止められないので、このゴールは要る。**まとめない。**

3 つに共通するのは**「`atct_role` を呼ぶまで気づけない」**ことである。これは別の作業単位に
なるので `next_steps` に置く。

## ゴール 179 と分ける

ゴール 179（取り下げ側）とは分ける。理由は 2 つ。

1. **必要な機構が逆向きである。**却下は「作業を再開させる」ので handoff を**開く**。
   取り下げは「作業を止める」ので handoff を**閉じて停止を届ける**。触る呼び出し口も
   `RejectCompletion` と `WithdrawActiveGoal` で別である
2. **179 の本体は通知の配送**である。`WithdrawActiveGoal` は既に開いている作業を
   原子的に閉じているので、残っているのは wakeup 側の仕事になる。こちらは store の
   状態遷移で閉じる

1 つにまとめると、1 日 3 回起きている側の修正が、別サブシステムの設計に引きずられて遅れる。

## 完了条件との対応

| 完了条件 | 満たし方 |
|---|---|
| 1. commander を経由せず再開できる | 受領済みの状態で再発行するので、却下後の `atct_role` が直ちに subcommander を返す |
| 2. 却下前の保持者以外が保持者にならない | `received_by` を元の行から複写する。既に開いた handoff があるときは何もしない |
| 3. 却下 → 再開 → 再提出 → 承認 の通し | `internal/e2e` に通しテストを置く |
| 4. 却下されていない handoff が開かない | 承認時・他ゴール・報告者が handoff を持たない場合・既に開いている場合の 4 つを否定側として検査する |
| 5. (1) を壊すと落ちる検査 | 却下直後に `deriveSessionRole` が `subcommander` を返すことを daemon 層で検査する |

## 触る場所

- `internal/store/goal.go` — `RejectCompletion` をトランザクション化し、再発行を組み込む
- 既存の sqlc クエリで足りる。`RequestGoalHandoff`（ID 指定の INSERT）と
  `ReceiveGoalHandoff` を `WithTx` で使う。**スキーマ変更も新しいクエリも要らない**
- `skills/atct/SKILL.md` — `## Recover when your role comes back wrong` の記述を直す。
  却下による役割喪失は自動で戻るので、commander への依頼はそれ以外の場合に限る
