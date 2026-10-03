# 古くなった proposed goal を commander の見直しに回す仕様

日付: 2026-10-03
ゴール: 272
置き換える仕様: `2026-09-19-goal-272-stale-goal-approval.md`（14 日で自動取り下げ）

## 目的

proposed のまま 7 日以上動きのない goal を、無条件に取り下げず、commander の
見直し対象として知らせる。見直しの結果は「理由付きで取り下げ」か「確認済みとして
残す」のどちらかで記録する。人間の差し戻し（decision 866）:
「無条件で取り下げるのではなく、1週間以上たっているものは再度確認され整理される」。

見本は 2026-10-03 に commander が手でやった整理である。proposed goal を 1 件ずつ現行
main と照合し、「修正が main にある」「別 goal に置き換えられた」「前提が消えた」の
どれかに根拠付きで当てはまるものだけを取り下げ、有効なものは残した（30 件中 13 件）。
この判定は daemon にはできない。daemon は見直しの時機を知らせ、結果を記録するだけにする。

## 動作

### 1. 自動取り下げをやめる

`ReconcileStaleGoalApprovals` と maintenance からの呼び出しを削除する。daemon は
goal の状態を変えない。14 日の定数、`ListOpenAgentGoalApprovals`、`HasGoalWork`、
`ErrGoalHasWork` も不要になるので削除する。

### 2. 見直し期限

proposed の goal は、次のうち最も新しい時刻から 7 日経つと「見直し待ち」になる。

- `goals.updated_at`（提案文や spec/plan の更新を含む）
- その goal の最新の確認記録（後述）の `confirmed_at`

creator では絞らない。現状 proposed を作るのは agent だけだが、人間が作った goal も
古くなり、知らせるだけなら対象を広げても害がない。7 日は定数 `goalReviewDueAfter`
とし、設定項目は作らない。

### 3. commander への知らせ

既存の wakeup の仕組みに載せる。

- `store.EvaluateWakeup` が `WakeupState.ReviewDueGoals []domain.Goal` を返す
  （`UndeclaredGoals` と同じ形。active 以外を飛ばす既存ループとは別に proposed を走査）。
- daemon の `evaluateWith` が、goal ごとに `wakeup.goal_review_due`
  （`store.EventWakeupGoalReviewDue`、`WakeupEvent{GoalID}`）を発行する。開始時刻は
  期限到達時刻（最終動作 + 7 日）で、猶予は 0。既存の `recordWakeupEvent` が使う
  `publishWakeup` に従い、状態が続く間は 1 回だけ出る。見直しで goal が proposed で
  なくなる、または確認で期限が延びると状態が消え、次の期限到達でまた出る。
- `cmd/atct/watch_scope.go` の project 範囲の配送リスト（commander の watch）に
  `wakeup.goal_review_due` を加える。subcommander の goal 範囲の watch には出ない。
- `cmd/atct/watch.go` の表示: 「proposed goal N は 7 日動きがない。現行 main と照合し、
  `atct_goal_withdraw`（理由付き）で取り下げるか `atct_goal_confirm` で残す」。
- `atct pending`（`cmd/atct/pending.go`）にも見直し待ち一覧を出す。wakeup は 1 回しか
  出ないため、見逃した commander が再度見つけられる経路が必要である。

wakeup は decision を作らない。人間の inbox に 2 つ目の問いは増やさない。

### 4. 見直しの結果の記録

- **取り下げ**: 既存の commander 専用 `goal.withdraw` / `atct_goal_withdraw` をそのまま
  使う。`WithdrawActiveGoal` の proposed 用の更新から creator・実行記録・approval の
  条件を外し、`status = 'proposed'` だけを条件にする。commander が理由を付けて明示的に
  行う操作なので、自動処理用に付けた「記録があれば拒否」の保護は不要になる。
  open な goal_approval は既存の transaction で理由付きで withdrawn になる。
  decision の作成者 session に依存しないため、`agent_session_id=0` の decision も
  取り下げられる（commander が poll できない問題に左右されない）。
- **確認済みとして残す**: 新しい commander 専用の操作 `goal.confirm`
  （MCP: `atct_goal_confirm`、引数 `goal_id` と非空の `note`）。proposed の goal にだけ
  使え、`goal_confirmations(id, goal_id, note, confirmed_at)` に 1 行追加する。これで
  期限は confirmed_at + 7 日に延びる。`goals.updated_at` は動かさない（提案の中身が
  変わったわけではないため）。note は「なぜまだ有効か」を書く欄で、後から見直しの
  根拠を辿れるようにする。proposed 以外の goal への確認は拒否する。
- migration は 1 本（`0048_goal_confirmations.sql`、新規テーブルのみ）。既存の goals の
  列には触れない。

## 非目標

- daemon が goal を自動で dropped にすること
- main との照合を daemon で自動判定すること
- 見直しのための新しい decision kind、UI、設定項目
- active / done / dropped の goal の見直し
- Goal 307（`agent_session_id=0` の decision の配送）の修正

## 受け入れ条件

1. 7 日動きのない proposed goal（creator 問わず）に対し、daemon の maintenance は
   goal も approval も変更しない。
2. 7 日未満の proposed goal には `wakeup.goal_review_due` が出ない。7 日以上で 1 回出る。
3. `atct_goal_confirm` で期限が確認時刻 + 7 日に延び、wakeup 状態が消える。
   proposed 以外、または note が空なら拒否される。commander 以外は拒否される。
4. commander の `atct_goal_withdraw` が proposed の goal（creator・実行記録を問わず）を
   理由付きで dropped にし、open な approval を withdrawn にする。active の既存挙動は
   変わらない。
5. commander の watch に見直し待ちの行が出る。goal 範囲の watch には出ない。
6. `atct pending` に見直し待ちの goal が出る。
7. `go build`、`go vet`、`go test ./... -count=1`、`./script/schema-check.sh`、wrapper test が
   現行 main を取り込んだ tree で通る。
