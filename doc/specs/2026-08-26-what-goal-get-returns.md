# `atct_goal_get` の返す内容を分けない

## 1. 何を決めたか

結論は、**`atct_goal_get` を分けない**である。

`goal.list` は一覧を返し、`goal.get` はゴールとそのタスクの詳細を返す。現状の2つの経路で用途を分担できているため、`goal.get` に別の読み取り経路を追加しない。

## 2. なぜ

**理由 1: 分割は既にある。**

`goal.list` はタスク一覧を返すとき、`summaryLine` でタスクの `description` を120 runesまでに切る。`TestContractN11GoalListTruncatesTaskDescription` がこの契約を固定している。一方、`goal.get` はタスクの `description` 全文を返し、`TestContractB12GoalGetReturnsFullTaskDescription` が固定している。

つまり、「タスクの一覧だけ欲しい」場面はすでに `goal.list` が持っている。3本目の読み経路を足すと、読む側がどの経路を呼ぶかを知る必要があり、同じ目的の契約が増える。

**理由 2: 費用の形が違う。**

`goal.get` の呼び出し元は `internal/mcpshim/tools.go` だけであり、`web/` と `cmd/` は RPC を呼んでいない。`goal.get` の費用はゴールを拾うときの1回分であって、`task.update` のようにタスクを閉じるたびに乗るものではない。したがって、ゴールとタスクを1回で拾う現在の形を保つ。

## 3. 分けた場合どうなるか

ゴール本文だけを返す経路と、タスク一覧だけを返す経路に分ける案は考えられる。分ければ一方だけを必要とする呼び出し側は受け取るデータを減らせるが、既存の `goal.list` と `goal.get` に3本目の読み経路が加わる。

その結果、呼び出し側は3つの経路から適切なものを選び、詳細が必要な場合は複数回の取得結果を組み合わせる必要がある。契約・テスト・利用側の分岐も増える。ゴールを拾うときの1回の費用を減らすために、この読み分けの複雑さを導入する形にはしない。

## 4. 採らなかった理由

「14,000の主成分は goal 本文だから、タスクを切り出しても無駄」という理由は、実測で否定されたため採らない。

`short-content` では `goal.get` 全体が 10,135 バイトで、`{"goal":...}` が 389 バイト、`{"tasks":[...]}` が 9,747 バイトだった。タスク側が主成分である。`long-content` では全体が 15,760 バイト、ゴール本文が 5,974 バイト、タスク側が 9,787 バイトで、タスク側は本文の約1.6倍だった。どちらも一方が常に支配するわけではなく、支配項はゴールによって変わる。

この理由を削り、判断の根拠は「分割は既にある」と「費用の形が違う」の2つにする。

## 5. 測定

測定者は `atct-fa9180a6-subcommander`、時点は `e7871e3` である。次のコマンドで測定済みの値を転記する。

```text
$ GOCACHE=… go test ./internal/daemon/ \
    -run 'TestContractN12TaskUpdateTruncatesDescription|TestContractN13GoalGetResponseSizeBreakdown' \
    -count=1 -v

task.update include_unapplied_answers=false len(raw)=696    （修正前 1233）
task.update include_unapplied_answers=true  len(raw)=702    （修正前 1239）

goal.get shape=short-content len(raw)=10135 len({"goal":...})=389  len({"tasks":[...]})=9747
goal.get shape=long-content  len(raw)=15760 len({"goal":...})=5974 len({"tasks":[...]})=9787
```

`short-content` は fixture のゴール本文が15文字の形、`long-content` は本文5,974バイトの形である。現実的な大きさの本文でもタスク側は9,787バイトで本文の1.6倍あり、どちらも常に支配するわけではない。

## 6. 残るギャップ

`goal.list` は project 単位である。「このゴールのタスク一覧だけ」を引く経路はない。一覧だけ欲しい呼び出し側は `goal.list` を呼び、プロジェクト内の全アクティブゴールを受け取ってから自分で絞ることになる。

これは残課題として残す。「分けない」は現状の判断であり、このゴール単位の経路が必要になったときは、利用実態を確認したうえで再検討する。
