# Goal 240: handoff entry は全遷移で書かれることを保証する

人間の差し戻し（decision 868）:「task_handoff_entries は必ず書かれることは保証されてる？」

## 監査結果（2026-10-03, main b954da5 を取り込んだ tree）

entry を持つのは task と goal の handoff だけ（`task_handoff_entries` / `goal_handoff_entries`）。
plan handoff と task-create handoff には entry テーブルが無く、このモデルの対象外（設計上）。
entry の kind は DB の CHECK で 6 種に固定されている: request / received / review_requested /
review_received / review_rejected / completed。

task と goal の handoff で、状態を変える Store の経路と entry の書き込み（同一 tx か）:

| 遷移 | Store 関数（task / goal） | entry |
|---|---|---|
| request | requestTaskHandoff / requestGoalHandoff | request（同一 tx）|
| 旧 open の reclaim | reclaimOpen*HandoffTx | completed（同一 tx）|
| receive | ReceiveTaskHandoff / ReceiveGoalHandoff | received（同一 tx）|
| review request | RequestTaskHandoffReview / RequestGoalHandoffReview | **無い** |
| review receive | ReceiveTaskHandoffReview / ReceiveGoalHandoffReview | **無い** |
| reject | RejectTaskHandoffReview / RejectGoalHandoffReview | **無い** |
| reject receive | Receive*HandoffReviewRejection | **無い** |
| recover | RecoverTaskHandoff / RecoverGoalHandoff | **無い** |
| complete（reviewer） | Complete*HandoffByReviewer | completed（同一 tx）|
| complete | Complete*Handoff（*ForTask / *ForGoal は委譲）| completed（同一 tx）|
| amend | Amend*HandoffReport | **無い**（appendHandoffEntryTx が amend を黙って捨てる）|
| task 更新 / goal withdraw による close | updateTask / WithdrawActiveGoal | completed（同一 tx）|

結論: 書く経路は状態更新と同じ tx にあるが、review request / review receive / reject /
reject receive / recover / amend の 6 経路は entry を書かない。保証は無い。

## 設計

保証の置き場所は Store 層。handoff テーブルを更新する経路はすべて Store の関数で、
各関数が「状態の更新」と「entry の追記」を同じ tx に持つ。DB trigger で導く案は採らない
（entry の author と body が状態の列だけでは決まらない経路があり、migration の追加と
既存 entry との整合のリスクが大きい）。

1. 欠けている経路に、同じ tx で entry を足す（task と goal の両方）。kind は 6 種のみ。
   - review request → `review_requested`（body = review_request_report, author = 要求した session）
   - review receive → `review_received`（body = `received`, author = 受領した reviewer）
   - reject → `review_rejected`（body = reject_report, author = reviewer）
   - reject receive → `received`（body = `review rejection received`, author = 受領した worker）
   - recover（全 phase）→ `request`（body = `recovered: <reason>`, author = recover した caller）。後継の worker が受け取る、再依頼として読む
   - amend → `completed`（body = 訂正後の report, author = 訂正した session）。entry は不変なので、訂正は新しい completed entry として追記する。最後の completed が現行の report
   - `appendHandoffEntryTx` が amend / system を黙って捨てる分岐は、amend の呼び出しが無くなった後に残る意味がないので除く
2. 「全経路で書かれる」を固定する:
   - 状態の列から導く不変条件のヘルパー: handoff の `requested_at` / `received_at` / `review_requested_at` /
     `review_rejected_at` / `completed_report_at` が入っていれば、対応する kind の entry が最低 1 件ある
   - 全遷移を table-driven で通し、各遷移の直後に、entry の増分（件数と最後の kind）と上の不変条件を確かめる（task と goal 両方）
   - 目録テスト: sqlc の queries から handoff テーブルを更新する query の名前を列挙し、既知の分類表（query 名 → 対応する遷移）と一致することを確かめる。新しい更新 query を足して分類しなければ落ちる
   - 原子性: entry の書き込みを失敗させたときに、状態の更新も戻ることを遷移ごとに確かめる（例: 本文が 16 KiB を超える report）

## 範囲外
plan handoff / task-create handoff の entry（テーブルが無い。必要なら別 goal）、migration の追加（不要）。
