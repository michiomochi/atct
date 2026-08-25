# 持ち主の記録を 1 つにする（goal と task の claim を落とす）

2026-08-24。人間の指示:

> commander はプロジェクトを claim し、subcommander が goal を claim し、
> executor が task を claim する、という整理のはず
>
> 各 claimed_by は使わず task_handoffs 等を使うのではダメ？

**案 1 で進める**——プロジェクトだけ claim を残し、goal と task は handoff だけにする。

## 1. いまの模型は subcommander を作れない（実測）

役割は claim からしか導かれない（`internal/daemon/handler.go`）。

```go
if goal.ClaimedBy == sessionID { response.Role = "subcommander" }
```

一方でスキルの `## Delegate a claimed goal` はこう言う。

```
1. Claim the goal with `atct_goal_claim` before handing it off.
4. > First invoke `atct_role` with `expected_role` set to `subcommander`.
   > If it reports `matches: false`, do not start work; return the goal.
```

**渡す側が claim を持ち、受け取る側は持たない。**したがって受け取った側の役割は:

```
{"role":"executor","project_id":"","goal_id":""}
```

**手順 4 が手順 1 のせいで必ず失敗する。**`matches: false` を見て、ゴールを返して終わる。

**task 側は同じ規則で成立する。**受け取る側が executor でよいからである。
**goal 側だけが破綻する。**受け取る側が subcommander でなければならないのに、
claim を持てないためである。

### これは調整で直る種類の欠陥ではない

規則が 2 つあり、両立しない。

```
A. 委譲する側が claim を持つ（`## Claim before you start`）
B. subcommander は goal claim を持つ（役割の定義）
```

**claim と handoff が、どちらも「誰のものか」を言おうとしている。**
食い違いはその重複から出ている。**片方を消せば、食い違う場所が無くなる。**

## 2. claim の 3 つの仕事は handoff で果たせる

`2026-08-24-handoffs-for-goals-and-bodies.md` の 3 節で、claim が
**3 つの仕事**を担っていることを数えた。すべて移せる。

| claim の仕事 | handoff での実現 | 根拠 |
|---|---|---|
| 排他 | `(task_id) WHERE completed_report_at IS NULL` の部分一意索引 | SQLite 3.51.0 で実測済み |
| 死者からの回収 | `received_by` も `agent_sessions` への外部キー | `claimIsRunning` は session id しか見ない |
| 停滞の検知 | `received_at` | `claimed_at` と同じ役割 |

**handoff のほうが強い。**`tasks.claimed_by` は参照制約が無く**存在しないセッションでも
書ける**が、`received_by` は書けない（2026-08-24 に executor が FK で落ちている）。

### 自分でやる場合

**自分宛の handoff を作る**（`requested_by == received_by`）。

`db18e025` でこの案を「形式的だ」として却下したが、**claim が無くなるなら形式ではなく
唯一の記録**になる。**却下の理由が消える。**

## 3. プロジェクトだけ claim が残る。**非対称は欠陥ではない**

**誰もプロジェクトを渡さない。**goal と task には委譲する側がいるが、
**プロジェクトの上には誰もいない。**人間がセッションを立て、それが取る。
`requested_by` に入れるものが無い。

```
project   claim      鎖の根。上に渡し手がいない
goal      handoff    commander → subcommander
task      handoff    subcommander → executor
```

**却下した案:** `project_handoffs` を作り `requested_by` を NULL にする
（「人間が渡した」を NULL で表す）。形は揃うが、**`requested_by` の意味が
「委譲した側」から「委譲した側、または人間」に薄まる。**
**非対称は模型の欠陥ではなく、プロジェクトが根であるという事実である。**

## 4. 役割の導出

```
project の claim を持つ                   commander
受領済みで未完了の goal handoff を持つ    subcommander
どちらでもない                            executor
```

**先に project を見る。**2 層で回すとき、commander は task を配るために
自分宛の goal handoff を作るが、**project claim が勝つので commander のままである。**

## 5. 委譲の門番

いまは「対象自身に生きた claim があること」を見ている（`requireLiveTaskClaim`）。
**これは「委譲する側が対象を claim する」前提の形である。**

**親を見る形に変える。**階層とそのまま一致する。

```
goal を配る  → project の claim を持っていること
task を配る  → その goal の handoff を受領していること
```

**「claim の無いものは委譲できない」という検査の意図は保たれる。**
見る場所が対象から親に移るだけである。

## 6. 落とすもの・残すもの

```
落とす   goals.claimed_by / goals.claimed_at
         tasks.claimed_by / tasks.claimed_at
残す     projects.claimed_by / projects.claimed_at
         atct_project_claim / atct_project_release
         atct_task_claim / atct_task_release      ← 書き込む先が handoff に変わる（8.1）
         atct_goal_claim / atct_goal_release      ← 同上
```

**`claim_undelegated` の検知が不要になる。**claim と handoff を突き合わせる必要が
無くなるためである。**2026-08-24 にその突き合わせで 3 回壊れている。**

## 7. 規模（2026-08-24 実測）

```
Go        125 箇所（うちテスト 78、本体 47）
SQL        31 箇所
web        11 箇所
```

**`1e082f2f`（UUID をやめて連番）と同じ箱である。**あちらは 8 表を作り直す。

### 順序: **こちらを先にやる**

`1e082f2f` は表を作り直すので、**claim の列が残っていればそれも一緒に運ぶ。**
**落としてから移行するほうが運ぶ量が減る。**

## 8. 決めたこと（2026-08-24・commander）

### 8.1 `claim` という動詞は残す。**消えるのは列だけ**

**当初 6 節に「`atct_task_claim` を落とす」と書いたが、取り下げる。**

自分でやる場合に `handoff_request` + `receive` の 2 回を呼ばせるのは、
**1 回で済んでいた最も多い経路を悪くする。**名前も委譲を示唆して読み違えを招く。

    atct_task_claim    自分宛の受領済み handoff を 1 回で作る
    atct_task_release  それを完了させる
    atct_goal_claim / atct_goal_release   同じ

**動詞は 2 つとも意味を持ち続ける。**`claim` は「自分で取る」、`handoff` は「人に渡す」。
**書き込む先が同じ 1 つの表になるだけで、重複は消える。**

### 8.2 既存の行は、セッションが実在するものだけ移す

実測（2026-08-24）:

    生きている claim   tasks 4 件 / goals 0 件
    うちセッションが実在しない   1 件

**外部キーが無いので実在しないセッションを指す claim が書けている。**その実例である。

**実在するものは自分宛の handoff にする**
（`requested_by = received_by = claimed_by`、`requested_at = received_at = claimed_at`）。
**実在しないものは捨て、捨てた件数を移行が報告する。**

### 8.3 部分一意索引を入れる。**死んだ受け取り手は自動で引き継ぐ**

    CREATE UNIQUE INDEX ... ON task_handoffs(task_id) WHERE completed_report_at IS NULL

**開いた handoff は 1 つまでを DB が保証する。**

`TestTaskHandoffAllowsSecondHandoffForSameTask` はこれを禁じられる。
**しかしその形（pane を作り直して 2 人目が持つ）は、claim では
`ClaimLiveness` が死んだ持ち主から奪い返して処理していた。**同じことをする。

    開いた handoff がある + 受け取り手が生きている   → 拒む
    開いた handoff がある + 受け取り手が死んでいる   → 古いほうを完了させてから作る
                                                       完了報告に「セッションが停止した」と書く

**これは claim の 2 つ目の仕事（死者からの回収）をそのまま移したものである。**
**索引と両立し、pane の作り直しも通る。**

## 9. やらないこと

**`projects.claimed_by` を消さない。**3 節のとおり。

**役割を claim 以外から導かない。**「受領済みの handoff を持つ」は claim の
置き換えであって、別系統の判定を足すのではない。**導出は 1 本のままにする。**

## 10. 3 層が成立することの実測（2026-08-25・0.54.0）

**1 節の欠陥が解けたことを、稼働中の daemon で確かめた。**3 つのセッションを別々に立て、
daemon に直接 `session.role` を問うた（自己申告ではなく観測）。

```
$ printf '{"id":"r","method":"session.role","params":{"agent_session_id":"<id>"}}\n' \
    | nc -U ~/.atct/atct.sock

commander     {"role":"commander",   "project_id":"1ff70f35...","goal_id":"36d5332e..."}
subcommander  {"role":"subcommander","project_id":"",          "goal_id":"419e0dff..."}
executor      {"role":"executor",    "project_id":"",          "goal_id":""}
```

**中間層が成立したのはこれが初めてである。**1 節の実測（`{"role":"executor"}`）と比べること。

ゴールは実際に進んだ。subcommander が task `8ec0e09c` を executor に渡し、`1bd6830` が着地した。

### 10.1 スキルの委譲手順は 2 か所とも成立しない

**そのまま実行して両方とも失敗した。**どちらも順序と前提の誤りで、daemon の欠陥ではない。

**手順 1「渡す前に対象を claim しろ」** — claim が handoff を書くので、claim した後に
handoff を要求すると部分一意索引に当たる。

```
atct_goal_claim(419e0dff)      → 成功
atct_goal_handoff_request(...) → goal handoff already open:
                                 goal "419e0dff..." has a live handoff owner
```

**渡す側は対象を持たない。親を持つ。**goal を渡す側は project claim を、task を渡す側は
受領済みの goal handoff を持つ。

**手順 4「まず role を確かめ、次に receive しろ」** — 役割は**受領済みの** goal handoff から
導かれるので、受領前に問えば必ず `executor` が返る。

```
{"expected_role":"subcommander","matches":false,"role":"executor",
 "project_id":"","goal_id":""}
```

**受領を先にする。**`atct_goal_handoff_receive` は開いた未受領の要求があるときしか通らないので、
**受領の排他が誤配の防止を担い、役割の確認は事後の裏取りになる。**（決定 `96e311f2`）

### 10.2 2 層の経路

commander が task を配るには、**自分宛の goal handoff が要る**（`atct_goal_claim`）。
project claim だけでは配れない。**それでも役割は commander のまま**である。

```
atct_goal_claim(36d5332e) → {"role":"commander"}
```

拒否のメッセージが `task handoff task is unclaimed` と、**このゴールで消したはずの概念を
名乗る。**真の条件は「呼び出し元がその goal の handoff を受領していない」である（task `960c0051`）。

### 10.3 なぜ検査が拾わなかったか

`TestSessionRoleDerivesFromClaims` は `goal.claim` を直接 dispatch して subcommander を作る。
**近道で状態を作っているので、request → receive の順路を一度も通っていない。**
`tests/wrapper_test.bash` はスキルの文言と境界表の一致しか見ない。

**「スキルに書いてある順番どおりに呼ぶと成立するか」を実行する検査が 1 つも無かった。**
task `58ae6a12` がそれを足す。
