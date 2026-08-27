# ClaimLiveness を 1 本のクエリにする

ゴール 159。1 回の検知が SQLite の 1 本の接続で数百クエリを流す件。

## 決めたこと（結論から）

**⑥ だけをやる。**`ClaimLiveness` と `GoalClaimLiveness` の N+1 を、
それぞれ 1 本の JOIN に置き換える。**`SetMaxOpenConns(1)` は触らない。**

理由:

- **⑥ は接続数を触らずに効く。**1 本の接続を占有する時間そのものが縮む
- **⑤（接続数を増やす）は、遅いクエリを並列に走らせるだけである。**
  `store.go:31` のコメントが書いているとおり daemon は単一書き手であり、
  接続を増やすと WAL の書き込み競合を新しく招く。⑥ のあとに測って必要なら決める
- **⑦（やらない）は採らない。**伸び方が実測できている（後述）

## 現状

    internal/store/store.go:31   db.SetMaxOpenConns(1)

daemon の HTTP・RPC・検知が SQLite の 1 本の接続を共有する。
遅いクエリが 1 つあれば画面も MCP も待つ。

    internal/store/claim_liveness.go  ClaimLiveness
      ListGoals(project)                1 回
        -> ListTasks(goal)              goals の数だけ
          -> ListTaskHandoffs(task)     tasks の数だけ
            -> GetAgentSessionLiveness  開いている handoff の数だけ

    internal/store/claim_liveness.go  GoalClaimLiveness
      ListGoals(project)                1 回
        -> ListGoalHandoffs(goal)       goals の数だけ
          -> GetAgentSessionLiveness    開いている handoff の数だけ

**`ClaimLiveness` は全ステータスのゴールと全タスクを歩く。**
`DetectWakeup` の本体（`internal/store/wakeup.go:160` 以降）は active なゴールしか歩かないので、
**1 回の検知のクエリ本数を決めているのは `ClaimLiveness` である。**

### 数（プロジェクト atct、`~/.atct/atct.db` の複製、2026-08-28 02:28 JST）

    goals              163
    tasks              659
    開いた task handoff   9
    開いた goal handoff  17
    active goals        18

**実測**（`internal/store/claim_liveness_bench_test.go`。`database/sql` のドライバを包んで
`QueryerContext` と `PrepareContext`+`Stmt.QueryContext` の両経路を数えた。
`ListGoals` を単独で測って 1 が出ることで数え漏れと二重計上がないことを確かめている）:

    ATCT_REAL_DB=/tmp/atct159/copy.db go test ./internal/store/ -run ClaimLiveness -v -count=1

    ClaimLiveness       832 クエリ   （= 1 + 163 + 659 + 9。予測と一致）
    GoalClaimLiveness   181 クエリ   （= 1 + 163 + 17。予測と一致）
    DetectWakeup        923 クエリ   （**うち 832 が ClaimLiveness。9 割である**）

    ClaimLiveness 単発      10 回の中央値 11.605 ミリ秒
    ClaimLiveness 20 並列    最小 239.108 / 中央 251.447 / **最大 254.651 ミリ秒**

**20 並列は単発の 21.9 倍である。**`SetMaxOpenConns(1)` なので `database/sql` が完全に直列化し、
20 × 11.6 = 232 ミリ秒にほぼ一致する。**待ちの正体は競合ではなく順番待ちである。**

ゴール本文の実測は 2026-08-26 の `goals=133 tasks=579` で 713 クエリ・20 並列の最大 185 ミリ秒だった。

    2026-08-26   goals 133 / tasks 579 -> 713 クエリ / 20 並列 最大 185ms
    2026-08-28   goals 163 / tasks 659 -> 832 クエリ / 20 並列 最大 255ms

**2 日で 119 クエリ・70 ミリ秒伸びている。**1 日あたり goals +15 / tasks +40 / **+35 ミリ秒**。

クエリ本数は goals + tasks に比例し、直列化された実時間はクエリ本数に比例するので、
**この 2 点を直線で延ばすと** 20 並列の最大は

    500 ミリ秒   (500-255)/35 =  7 日後 -> 2026-09-04
    1 秒         (1000-255)/35 = 21 日後 -> 2026-09-18

**2 点の外挿であり、この 2 日は活動が多かった期間なので上振れしている可能性がある。**
それでも桁は動かない。**「いま困っていない」は今日の話である。**

## 効く仕掛け: 開いた handoff は 1 タスクに 1 本しかない

    internal/store/migrations/0015_handoff_exclusivity.sql
    CREATE UNIQUE INDEX idx_task_handoffs_open_task_id
      ON task_handoffs(task_id) WHERE completed_report_at IS NULL;
    CREATE UNIQUE INDEX idx_goal_handoffs_open_goal_id
      ON goal_handoffs(goal_id) WHERE completed_report_at IS NULL;

**部分ユニークインデックスがあるので、開いた handoff は 1 タスク（1 ゴール）に最大 1 本である。**
現在のコードは「id 昇順で最初の未完了 handoff」を採るが、**候補が 1 本しかないので同じものを指す。**
よって `MIN(id)` の相関副問い合わせは不要で、`completed_report_at IS NULL` で絞るだけでよい。

**この不変条件はデータベースが保証している。**実装のコメントにインデックス名を書くこと。

さらに、絞った結果は 9 行と 17 行しかない。
**832 クエリのうち 823 本は「開いた handoff を持たないタスク」を読むためだけに走っている。**

## 置き換え後のクエリ

`internal/store/queries/task.sql` に既にある `ListOpenTaskHandoffsForGoal` が同じ形の先例である。
これをプロジェクト単位に広げる。

    -- name: ListOpenTaskHandoffClaims :many
    SELECT t.id, t.goal_id, t.title, t.description, t.status, t.agent,
           t.sort_order, t.declare_key, t.snoozed_until, t.created_at, t.updated_at,
           th.requested_by, th.received_by
    FROM task_handoffs AS th
    JOIN tasks AS t ON t.id = th.task_id
    JOIN goals AS g ON g.id = t.goal_id
    WHERE g.project_id = ? AND th.completed_report_at IS NULL
    ORDER BY g.created_at, g.id, t.sort_order, t.id;

    -- name: ListOpenGoalHandoffClaims :many
    SELECT g.id, g.project_id, ... (ListGoals と同じ列),
           gh.requested_by, gh.received_by
    FROM goal_handoffs AS gh
    JOIN goals AS g ON g.id = gh.goal_id
    WHERE g.project_id = ? AND gh.completed_report_at IS NULL
    ORDER BY g.created_at, g.id;

    ClaimLiveness       1 +  9 = 10 クエリ   （832 から）
    GoalClaimLiveness   1 + 17 = 18 クエリ   （181 から）
    DetectWakeup        923 - 832 + 10 = 101 クエリ

**`DetectWakeup` に残る 91 クエリは本体の別の N+1 である**（`internal/store/wakeup.go:193` 以降が
active なゴールごとに `ListTasks` / `ListOpenDecisions` / `ListGoalHandoffs` を、
そのタスクごとに `ListTaskHandoffs` を呼ぶ）。**active なゴールは 18 件しかないので、
`ClaimLiveness` を直したあとの主項ではない。今回は触らない。**

`GetAgentSessionLiveness` は 1 件ずつ残す。**9 本・17 本なので束ねる価値がない。**
`claimIsRunning` を変えないことで、`claimIsDefinitelyDead` と共有している判定が動かない。

### 並び順

`ListGoals` は `ORDER BY created_at` で、同時刻の同着に決着を付けていない。
JOIN 側は `g.created_at, g.id` と書く。**`g.id` を足すのは並びを決めるためで、
既存の実測値では同着がないので出力は変わらない。**

## 完了条件への当て方

    (1) クエリ本数    変更前後で数える。計測用の足場を test に置く
    (2) 同じ結果      旧実装（歩く版）と新実装を同じ DB に当てて running/stale を突き合わせる
    (3) GoalClaimLiveness  **揃える。**同形であり、片方だけ直すと 181 クエリが残る
    (4) 20 並列        変更前後で取る
    (5) やらない場合    該当しない（やる）
    (6) SetMaxOpenConns  **触らない。**よって書き込みの排他は動かない

### 計測の足場

`ATCT_REAL_DB` で本物の複製を指す形は
`internal/daemon/detection_realdata_test.go` に先例がある。**同じ作法に揃える。**

クエリ本数は `database/sql` のドライバを包んで数える。
`Store.db` は非公開だが **test は `package store` に居るので差し替えられる**（`claim_liveness_test.go` が
`processStartedAt` を差し替えているのと同じ）。**製品コードに計測用の穴を開けない。**

## ゴール 180 との関係

ゴール 180 が `internal/daemon/wakeup.go:307` に `handoffWorktreeActivity` を足した。
これは `git status` と `git log` を fork する（各 3 秒の上限つき）。

**ただし DB の 1 本の接続を握らない。**`s.DetectWakeup` が返ったあとに走り、
`EventDetectionHandoffUnreported` が実際に publish されるときだけ呼ばれる（publish は間隔で絞られている）。
**「1 本の接続を全員で共有していて画面も MCP も待つ」という症状の原因ではない。**
検知 1 周の実時間には乗るので、計測では **DB クエリの時間と fork の時間を分けて出す。**

## 結果（実測・2026-08-28）

新旧を**同一プロセス・同一接続・同じ負荷条件**で並べて測った。
旧実装は差分テスト用に写し取ったヘルパー（`claimLivenessLegacy` / `goalClaimLivenessLegacy`）である。

    ATCT_REAL_DB=/tmp/atct159/copy.db go test ./internal/store/ -run ClaimLiveness -v -count=1

    goals=163 tasks=659 open_task_handoffs=9 open_goal_handoffs=17

                        クエリ         単発中央値          20 並列 最大
    ClaimLiveness       832 ->  10    11.432 -> 0.769ms   255.7 ->  4.45ms
    GoalClaimLiveness   181 ->  18     4.217 -> 1.492ms    69.8 ->  9.51ms
    DetectWakeup        923 -> 101

**20 並列の最大が 255.7 から 4.45 ミリ秒になった。57 分の 1 である。**
`SetMaxOpenConns` は 1 本のままである。**接続数を触らずに効いた。**

計測の足場は旧実装のクエリ本数が `1 + goals + tasks + open_task_handoffs` と一致することも
assert している。**数字が偶然一致しただけでないことを機械が見張る。**

### 同じ結果が返ることの検査（完了条件 (2)）

`reflect.DeepEqual` で `[]domain.Task` / `[]domain.Goal` をまるごと比べる。
**件数だけでなく並びとフィールドの全部である。**

    fixture     task  running=2 stale=1     goal  running=1 stale=1
    複製 DB     task  running=0 stale=9     goal  running=0 stale=17

**否定側**: 変換を自作すると、時刻の parse 失敗が error ではなくゼロ値になり、
**速くなって答えが変わる**。既存の `taskFromFields` / `goalFromFields` を使って error を外に出し、
壊れた `created_at` で error が返る test を別に置いている。

## やらなかったこと

    ⑤ SetMaxOpenConns を増やす     20 並列の最大が 4.45 ミリ秒になったので、いま増やす理由がない
    DetectWakeup 本体の N+1        101 クエリ。active なゴール 18 件に比例する。別ゴールで測る
