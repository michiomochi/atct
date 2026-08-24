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
         atct_goal_claim / atct_goal_release
         atct_task_claim / atct_task_release
残す     projects.claimed_by / projects.claimed_at
         atct_project_claim / atct_project_release
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

## 8. 決めていないこと

- **既存の行をどうするか。**いま claim を持つ task は、handoff を持たないものがある。
  **移行で自分宛の handoff を作るか、捨てるか。**捨てると「誰がやっていたか」が消える
- **部分一意索引と、pane 作り直しの両立。**
  `TestTaskHandoffAllowsSecondHandoffForSameTask` は、同じタスクに 2 つ目の handoff を
  作れることを検査している（pane を作り直したときの形）。**索引はそれを禁じる。**
  「作り直す前に古い handoff を完了させる」を手順にするなら両立するが、
  **その手順が守られなかったとき何が起きるかを決めていない**
- **`atct_task_claim` を消したあと、自分でやる場合の入口をどう呼ぶか。**
  `atct_handoff_request` を自分宛に呼ぶのは意味が通るが、**名前が委譲を示唆する**

## 9. やらないこと

**`projects.claimed_by` を消さない。**3 節のとおり。

**役割を claim 以外から導かない。**「受領済みの handoff を持つ」は claim の
置き換えであって、別系統の判定を足すのではない。**導出は 1 本のままにする。**
