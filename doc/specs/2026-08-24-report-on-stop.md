# 働く側が黙って終わっても、委譲した側に届く

2026-08-24。人間の指示:「executor が報告を送らないことが多いので stop hook で自分の
atct role を確認し、executor だったら subcommander に報告する」。決定は **A**。

## 問題

**executor は報告を忘れる。**2026-08-24 だけで繰り返し起きた。commander が
`herdr agent read` で画面を覗いて初めて「終わっている」と分かる、という形が常態化した。

**気づくのが遅れるだけではない。**executor が黙って終わったのか、まだ考えているのかを
**外から区別できない。**pane の状態は `idle` としか言わない。

### 忘れているのではない。送れないことがある（2026-08-24 追記）

**実測。**executor-16 が報告を送れなかった理由を本人に聞いた。

> Herdr の `atct-commander` 宛送信が、**未検証の外部宛先へのデータ送信**と判定されました。
> 本文に内部リポジトリの絶対パス、変更内容、テスト結果が含まれていたため、
> **情報送信リスクとして拒否**されました。ユーザーの依頼文は、その安全ゲートでは
> 送信許可として検証可能な承認情報とは扱われませんでした。

**executor 側の安全ゲートが止めている。**executor から見れば多重化ツールは外部のバイナリで、
そこへ絶対パスとコードの中身を渡す形になる。**依頼書に書き方を指示しても越えられない。**

**この事実が決定 3 を裏づける。**「終わった」だけを atct へ送る形は、
**本文を持たないので情報送信リスクにならない。**宛先もプラグイン内のバイナリである。

**偶然そうなったのではなく、これが本質だと分かった。**多重化ツール経由の報告は、
executor の安全ゲートに依存する。**atct 経由なら依存しない。**

**境界を実測した（2026-08-24）。**同じ executor に、長い報告と短い報告の両方を送らせた。

```
絶対パス・差分・テスト出力あり   拒否される（executor-16 / executor-17 の 2 例）
「終わった。検証は通った」だけ   通る（executor-17）
```

**内容の量が境界である。**書き方の指示では越えられないが、**書く量を減らせば越えられる。**

### ゲートは「守りを外す」検証も止める

**executor-17 は破壊検証を実行できなかった。**「claim 無しの拒否を一時的に無効化して
落ちることを確かめろ」という依頼が、ゲートに止められた。

**したがって破壊検証は commander が代行する。**依頼書に書いても実行されないことがある。

## 使えるものは既にある

```
CREATE TABLE task_handoffs (
  id, task_id,
  requested_by  → agent_sessions(id)     委譲した側
  received_by   → agent_sessions(id)     受け取った側
  requested_at, received_at,
  completed_report_at                    ← 「終わったと伝えた時刻」
)
```

`internal/store/task_handoff.go` に `RequestTaskHandoff` / `ReceiveTaskHandoff` /
`CompleteTaskHandoff` / `GetTaskHandoff` / `ListTaskHandoffs` が揃っている。

**足りないのは経路だけである。**

```
handler.go の handoff RPC     0 件
mcpshim の handoff ツール     0 件
task_handoffs の行            0 件
```

**検知だけが先に入っている。**

```
cmd/atct/watch.go:631
atct detection: task %s has no handoff request
```

**記録する手段が無いまま告発している。**これが `b01a92b8`「テーブルと関数はあるが、
そこへ届く経路が無い」の実体である。

## 決定 1: Stop hook を復活させる。ただし役割で分ける

`9c7df582` は 2026-08-22 に Stop hook を廃止した。理由は
**「commander と subcommander の両方で発火するので、3 層に分けられない」。**

**その理由はいま解ける。**`atct_role` が claim から役割を導く（`2f924bd` / `d9c3bce`）。
**誰の Stop かを区別できる。**

```
Stop hook
  atct_role を呼ぶ
  role != executor  → 何もしない。終わり
  role == executor  → 「このタスクを終えた」を daemon へ伝える
```

**廃止の決定を全面的に覆すのではない。**`9c7df582` が消したのは「10 種類の状況を
Stop のたびに数え直して催促する」振る舞いで、**それは wakeup に統一されたままにする。**
**新しい Stop は 1 つのことしかしない。**

`tests/wrapper_test.bash` の `test_hooks_json_has_no_stop_section` は書き換える。
**「Stop が無いこと」ではなく「Stop が報告以外のことをしないこと」を検査する。**

### 削除した理由が、復活を支持している

`aa6a9eb`（2026-08-22）のコミットメッセージ:

> **It can go because it never knew anything of its own:** it printed `atct pending`,
> and `pending` and the detection events read the same `WakeupState`.

**古い Stop は、他の経路で得られる情報を繰り返していただけだった。**だから消せた。

**新しい Stop は逆である。**「このセッションが終わる」は**フックにしか分からない。**
wakeup も検知も、セッションの終わりを知らない。**繰り返しではない。**

同じメッセージが当時の害も書いている:

> The Stop hook fired in every space the plugin reached, **including a subcommander's**,
> where a notice meant for the commander started design work instead.

**`atct_role` がこれを解く。**executor 以外では何もしない。

### 入力の形も、複数回発火も、既に分かっていた

削除前の `plugin/hooks/stop` にこうあった。

```bash
INPUT="$(/bin/cat)"
STOP_HOOK_ACTIVE_RE='"stop_hook_active"[[:space:]]*:[[:space:]]*true'
if [[ "$INPUT" =~ $STOP_HOOK_ACTIVE_RE ]]; then
  exit 0
fi
```

**stdin で JSON を受け取る。複数回発火する**ので `stop_hook_active` で早期に抜ける。
**この 2 つを「未検証」に挙げたが、答えは自分たちのコードにあった。**

## 決定 2: 宛先は `claimed_by` から引く。herdr を知らない

**atct は pane 名を知らないし、知る必要もない。**

```
tasks.claimed_by = 0c827a1e-…      委譲した側のセッション ID
~/.atct/watchers/<pid>             そのセッションへの生きた経路
```

daemon が `claimed_by` からセッションを引き、**その watch へイベントを流す。**

```
executor の Stop
  → RPC「タスク T を終えた」
  → daemon が tasks.claimed_by からセッション S を引く
  → task_handoffs.completed_report_at を埋める
  → S の watch へ流す
  → commander の Monitor が鳴る
```

**`2026-08-23-delegating-without-a-multiplexer.md` の決定 4 を守る。**個別ツールの名前も
コマンドの綴りも出てこない。

## 決定 3: 報告の中身は「終わった」だけにする

**何をしたかは書かせない。**理由は 2 つ。

1. **Stop hook は本文を持たない。**終了したという事実しか分からない
2. **中身のある報告は依然 executor が送る。**これは**その代わりではなく、
   届かなかったときの網である**

commander が受け取るのはこの 1 行でよい。

```
atct handoff: task <id> reported complete
```

**それだけで「画面を見に行け」と分かる。**

## 決定 4: 経路は 3 つ要る。順に入れる

```
1. handoff の RPC と MCP ツール        request / receive / complete
2. 委譲するときに request を書く        commander 側
3. Stop hook が complete を書く         executor 側
```

**2 が無いと 3 が書けない。**`completed_report_at` は `task_handoffs` の行に載るので、
**行が無ければ完了も記録できない。**

**`b01a92b8` の承認が要る。**あれが 1 と 2 を持っている。

## 決定 5: 働く側は task_id で引く。cwd で完了する（2026-08-24 追記）

人間の承認: 決定 `af25c691` の A。**ただし A だけでは片方しか解けない。**

```
A が解く      Stop hook が「どの handoff を完了させるか」を cwd で引ける
A が解かない  働く側が receive を呼ぶための handoff_id をどう知るか
```

**働く側が知っているのは `task_id` である。**依頼書に必ず書かれる。`handoff_id` は
commander が発行するもので、働く側は知らない。**そこを埋める。**

```
atct_handoff_receive(task_id)
  → その task の「依頼済み・未受領」の handoff を引く
  → received_by に shim のセッションを入れる
  → **働く側の cwd も記録する**（新しい列）

Stop hook
  → 自分の cwd で「受領済み・未完了」の handoff を引く
  → completed_report_at を埋める
```

**どちらも引数は 1 つ以下になる。**働く側は `task_id` だけ、Stop hook は何も渡さない
（cwd はフックの JSON に入っている）。

### なぜ task_id で引けるか

**1 タスクに 1 人**（`skills/atct/SKILL.md` の手順 5）。同じタスクに未受領の依頼が
2 つ並ぶことはない。**並んだらそれ自体が欠陥**なので、その場合は拒む。

### handoff_id は残す

RPC と CLI は `handoff_id` を受け取るままにする。**commander はソケットへ直接投げるので
明示できる**（MCP が起動時に凍るため、今日それをした）。**MCP の層だけを変える。**

## 決定 6: Stop は「手を空けた」を流すだけ。何も保存しない（2026-08-24 追記）

人間の指摘で 2 つの誤りが正された。

**誤り 1: プロセスの死では検知できない。**「executor の MCP プロセスが死んだら
daemon が気づけるので Stop hook は不要かもしれない」と書いたが、**pane は閉じず、
セッションは生き続ける。**閉じるのは委譲した側の仕事である。**pid は動いたままで、
daemon は何も知れない。**

**誤り 2: Stop は「完了」ではない。**削除したコミットの題がそう言っていた——
`Stop blocking the end of every turn`。**毎ターン鳴る。**`completed_report_at` を
書くと 1 回目の応答で「完了」になる。削除前の実装が `stop_hook_active` を見ていたのも
そのためである。

### 鳴る回数は問題にならない

人間の指摘: **「executor が止まった場合、終了したか subcommander の判断が必要な場合のみ」。**

**executor は作業の途中で理由なく止まらない。**手を空けるのは、やり切ったか、
判断を仰ぐかのどちらかである。**したがって Stop は毎回、委譲した側が見るべき瞬間**であり、
騒音にならない。

commander は「猶予を置くべきでは」と考えたが**誤りである。**待てば、**判断を仰いでいる
executor を放置する。**今日それが起きた（executor-11 と 12 が差し戻して止まり、
気づくのが遅れた）。

### 保存しない

commander は `yielded_at` を足す案を出したが、**人間が退けた。**

**即時に流すなら保存する必要が無い。**時刻を残しても誰も読まない。

```
Stop hook → daemon → 委譲した側の watch へ即座に流す
```

**代償**: イベントは流れて消えるので、**watch が落ちている間の通知は失われる。**
今日 watch を 6 回止めて張り直した（リリースのたび）。**その取りこぼしは許す。**

### どの handoff かをどう知るか

**未解決のまま残る。**cwd は使えない（executor は作業中に `cd` する）。
`received_by` で引きたいが、**Stop hook は自分の atct セッション ID を知らない。**
`agent_sessions` は `pid` を持つので、**フックから MCP プロセスの pid に辿り着けるか**を
先に測る。

## 未検証

- **Codex の Stop hook が何を渡してくるか。**`crit` が使っていることは確かめた
  （`hooks.json` に `Stop` がある）が、**入力の形は見ていない。**Claude と同じ JSON か。
  **Claude 側は分かった**（削除前の実装が stdin の JSON を読み、`stop_hook_active` を見ている）
- ~~**executor が Stop 時に atct へ届くか。**~~ **解決した（2026-08-24 追記）。**
  PATH は要らない。`hooks/session-start` が既に `ATCT_BIN="$HOOK_DIR/../bin/atct"` で
  **フック自身の位置から相対にバイナリを引いている。**Codex 側のプラグインにも
  `bin/atct` は入っており、`session-start` が Codex で動くことは実測済みである。
  **Stop hook も同じ形でよい。**commander はこの点を最大の未検証と書いたが、
  **既存の実装が既に解いていた。**
- **どのタスクを終えたかを Stop hook がどう知るか。**役割は claim から導けるが、
  **claim を持つのは commander であって executor ではない。**executor は自分が
  どのタスクを渡されたかを atct 上では持っていない。**`received_by` に自分が
  記録されていれば引ける**が、それは決定 4 の 2 が入ってからである

## 実装方針の追記（2026-08-25）

決定 6 により、Stop hook は毎ターン「手を空けた」事実を流すだけであり、
タスク本文の完了条件 (1) にあった `completed_report_at` の書き込みを否定する。
Stop hook はこの列を更新せず、受領済み・未完了の handoff が存在する場合だけ、
その task を指定した `handoff_yielded` イベントを daemon から watch へ流す。

どの task を対象にするかは、人間の決定 `d55c762c`（2026-08-25 適用）により、
pane 作成時に渡される環境変数 `ATCT_TASK_ID` から引くことに決まった。環境変数が
空なら何もしない。`atct role` は `--agent-session-id` を要求するため、Stop hook
から役割を引くことはできず、役割の代わりに daemon が task の handoff 状態を確認する。
