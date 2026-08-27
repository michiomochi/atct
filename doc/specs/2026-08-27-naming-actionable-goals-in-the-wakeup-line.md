# wakeup の行に、着手可能なゴールを名前で出す

ゴール 136。実測（2026-08-26）で、commander が着手可能なゴールを 5 時間以上放置した。
`actionable_goals=6` と「動かせるものはない」が同時に成り立ってしまい、**行だけでは矛盾に
気づけなかった。**行に名前が無く、割り当て済みかどうかも分からないためである。

## 何を足すか

    atct wakeup: actionable_goals=6 unassigned_goals=2 unstarted_tasks=12 waiting_answer_tasks=0 untouched_tasks=2 delegated_tasks=0 waiting_answers=0 unassigned=[136,140]

既存の並びは変えない。`actionable_goals` の直後に `unassigned_goals=N` を挿し、行末に
`unassigned=[...]` を置く。

## 決めたことと、その理由

### 名前の形はゴールの数値 ID

ゴール本文は「先頭 8 文字か、本文の 1 行目か」と書いているが、**先頭 8 文字は連番化
（コミット 34bbf32）より前の記述である。**今の ID は数値なので、切り出す前提が消えている。

本文の 1 行目は採らない。1 行目は長さが制御できず、行が一気に破綻する。**ID なら
`atct goal get 136` にそのまま渡せる。**行は「引く先」を指せれば足りる。

### 列挙するのは未割当のゴールだけ

矛盾に気づくのに要るのは「6 件のうち何件が誰も持っていないか」であって、委譲済みの
ゴール ID ではない。委譲済みまで並べると行が伸び、**肝心の未割当が埋もれる。**

### 未割当の判定は goal handoff の行の有無だけで行う

`ReceivedAt != nil && CompletedReportAt == nil` の goal handoff が 1 件でもあれば
「割り当て済み」。1 件も無ければ「未割当」。

**`received_by` を `agent_sessions.session_key` で引き直さない。**実測（2026-08-26）で
5 ゴールの `received_by` が `session_key` で引けなかった。受領はされているが鍵が無い。
鍵で引けないことを未割当と読むと、**割り当て済みのものを未割当と数える。**
行の有無だけで判定すれば、鍵の欠落は判定に影響しない。

### 上限は 5 件。超えた分は `+N` で出す

    unassigned=[136,140,141,142,143,+15]

実測の 6 件でも 20 件でも、行の長さが一定に収まる。**切り捨てを黙って行わない。**
`+15` があることで、読み手は「5 件しか無い」と読み違えない。

### 0 件でも `unassigned=[]` を出す

形を常に同じにする。フィールドが消えると、読み手も検査も「0 件」と「欠落」を
区別できない。

### 対象は actionable なゴールに限る

未着手タスクを持つゴール（`actionable_goals` に数えるのと同じ集合）だけを見る。
タスクが 1 件も宣言されていないゴールは `detection.undeclared_goal` が別途出るので、
この行には載せない。

## 触る場所

    internal/store/wakeup.go        WakeupState / WakeupEvent に件数と ID を足す。DetectWakeup で判定
    internal/daemon/wakeup.go       イベントに載せる
    cmd/atct/watch.go               watchDecision の受け口と formatWatchDecision の行
    internal/store/wakeup_test.go   判定の検査（鍵の無い received_by を含む）
    cmd/atct/watch_test.go          行の形の検査（0 件・5 件・20 件）
    cmd/atct/wakeup_delivery_test.go 既存の期待文字列の追従
