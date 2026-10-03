# 古くなった proposed goal を commander の節目の確認に回す仕様

日付: 2026-10-03
ゴール: 272
置き換える仕様: `2026-09-19-goal-272-stale-goal-approval.md`（14 日で自動取り下げ）

## 目的

proposed のまま 7 日以上動きのない goal を、daemon が取り下げるのではなく、commander が
決まった節目で現行 main と照合して整理する。人間の差し戻し:

- decision 866:「無条件で取り下げるのではなく、1週間以上たっているものは再度確認され整理される」
- decision 877:「goal_confirmations は不要じゃない？各チェックポイントで古くなってるかを確認する
  プロセスをいれればよくない？」

見本は 2026-10-03 に commander が手でやった整理である。proposed goal を 1 件ずつ現行 main と
照合し、次のどれかに根拠付きで当てはまるものだけを理由付きで取り下げ、有効なものは残した。

- 修正がすでに main にある
- 別の goal に置き換えられた
- 前提が消えた

この判定は daemon にはできない。daemon は「7 日以上動きのない proposed goal」を見つけやすく
するだけにし、確認の記録は持たない。残した goal は次の節目でまた確かめる（手間は小さい）。

## 節目（チェックポイント）

**commander が `atct_goal_list` を見る 2 つの場面**とする。

1. `/atct:start` の開始時（セッションの最初の Look）
2. goal を完了させた直後（`atct_goal_review_complete` の後に回ってくる次の Look）

根拠: `atct_goal_list` は skills/start の loop の Look で commander が必ず通る呼び出しで、
`awaiting_approval_count` のような proposed 側の状況もすでにここで見ている。専用の呼び出しや
新しい経路を足さずに済む。ループの毎周ではなく上の 2 場面に絞るのは、残した goal を毎周
確かめ直す手間を避けるため。goal の委譲前は、委譲する goal の判断と無関係な整理を割り込ませる
ことになるので節目にしない。

## 動作

### 1. 自動取り下げをやめる

`ReconcileStaleGoalApprovals` と maintenance からの呼び出し、14 日の定数、`HasGoalWork`、
`ListOpenAgentGoalApprovals`、`ErrGoalHasWork` を削除する。daemon は goal を変えない。

### 2. 見つけやすくする（`atct_goal_list`）

`goal.list` のレスポンス `data` に `review_due_goals` を足す。proposed の goal のうち
`goals.updated_at` から 7 日以上経ったものの `{id, title, updated_at}` の配列。creator は
問わない（知らせるだけで害がない）。7 日は定数 `goalReviewDueAfter` とし、設定項目は作らない。
ハンドラがすでに取得している `ListGoals` の結果を絞るだけで、store・query・migration は増やさない。

### 3. 節目での手順（skills/commander/SKILL.md、skills/start の loop の Look に 1 行の参照）

節目で `review_due_goals` が空でなければ、各 goal を現行 main と照合し、上の 3 つの根拠の
どれかに当てはまるものを `atct_goal_withdraw` で理由（根拠）付きに取り下げる。当てはまらない
ものは何もせず残す（記録しない）。

### 4. 取り下げの経路

commander 専用の `atct_goal_withdraw` を proposed にも使えるようにした変更を残す。
`WithdrawActiveGoal` の proposed 用の更新条件は `status = 'proposed'` だけ。open な
goal_approval は既存の transaction で理由付きで withdrawn になる。decision の作成者 session に
依存しないため、`agent_session_id=0` の decision も取り下げられる。作業記録（task、handoff、
追加 decision）のある proposed goal も取り下げられる点は、needs_review に残す。

### 5. 作らないもの

- `goal_confirmations` テーブル、migration、`atct_goal_confirm`、`goal.confirm`、
  `Store.ConfirmProposedGoal`
- wakeup（`wakeup.goal_review_due`、`WakeupState.ReviewDueGoals`）。残した goal は 7 日超の
  まま残るため、確認の記録なしでは wakeup が消えず、daemon 再起動のたびに再発行されて節目での
  確認と重複する。
- watch の配送・表示、`atct pending` の一覧

## 非目標

daemon の自動取り下げ、main との自動照合、新 decision kind・UI・設定・migration、
active/done/dropped の goal の見直し、Goal 307 の修正。

## 受け入れ条件

1. 7 日以上動きのない proposed goal（creator 問わず）に対し、daemon の maintenance は goal も
   approval も変更しない。
2. `atct_goal_list` の `review_due_goals` に、proposed かつ updated_at が 7 日以上前の goal だけが
   入る（6 日、active、done は入らない）。
3. commander の `atct_goal_withdraw` が proposed の goal（creator・実行記録を問わず）を理由付きで
   dropped にし、open な approval を withdrawn にする。active の既存挙動は変わらない。
4. `goal_confirmations`、migration、`atct_goal_confirm`、wakeup、watch、pending の追加が
   残っていない（main との差分に出ない）。
5. SKILL.md に節目と手順が書かれ、skills/start の Look から参照されている。
6. `go build`、`go vet`、`go test ./... -count=1`、`./script/schema-check.sh`、wrapper test が
   現行 main を取り込んだ tree で通る。
