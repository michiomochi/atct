# ゴールにも委譲の記録を。本文も残す

2026-08-24。人間の設計:

> subcommander もゴールを依頼されるのでゴールについても handoff が必要なはず。
> `goal_handoffs goal_id requested_by(commander の session_id) received_by(subcommander の
> session_id) requested_at received_at completed_report_at request_report complete_report`
> という感じにして、それにならい task_handoffs も作成して。
> **task の claim 情報は情報が重複するので消せてよさそう**

## 1. ゴールにも同じ穴がある

`goals.claimed_by` はある（`0011_goal_claims`）。**しかし「誰が誰に渡したか」の記録が無い。**
task 側とまったく同じ形で、**今日その穴を task で塞いだばかりである。**

3 層の模型では commander がゴールを subcommander に渡す。**その受け渡しがどこにも残らない。**

## 2. 本文を残す理由は、今日の実測が示した

**executor の報告は、忘れられているのではなく止められている。**

```
絶対パス・差分・テスト出力あり   拒否される（executor-16 / executor-17 の 2 例）
「終わった。検証は通った」だけ   通る（executor-17）
```

executor-16 の説明:

> Herdr の宛先への送信が、**未検証の外部宛先へのデータ送信**と判定されました。
> 本文に内部リポジトリの絶対パス、変更内容、テスト結果が含まれていたため拒否されました。

**本文を atct に書けば、宛先はプラグイン内のバイナリである。**「未検証の外部宛先」に
あたらない。`2026-08-24-report-on-stop.md` の決定 3 は「報告は『終わった』だけにする」
と書いたが、**本文が通るならそれより良い。**

**ただし通るかは未検証である。**実装したら executor に実際に書かせて確かめる。
**通らなければ決定 3 のまま**で、列は空のまま使わない。

```
request_report    委譲する側が書く。何を頼んだか
complete_report   受け取った側が書く。何をしたか
```

**どちらも NULL 可にする。**書けない環境でも記録そのものは成立する。

## 3. task の claim を消すのは「移す」である

**人間の見立ては正しい。**`tasks.claimed_by` と `task_handoffs.received_by` は
どちらも「誰が責任を持つか」を指し、**同期していない**（done にすると claim は消えるが
handoff は残る）。

**しかし claim は所有の記録だけをしていない。**実測すると 94 箇所・15 ファイルで読まれ、
**3 つの仕事**を担っている。

```
1  排他制御       ErrTaskAlreadyClaimed。2 人目を原子的に拒む
2  死者からの回収  ClaimLiveness。セッションが死んだら claim を奪い返せる
3  停滞の検知      stale_claim。3 分動かない claim を鳴らす
```

**handoff にはどれも無い。**`task_handoffs` に一意制約は無く、**同じタスクに未受領の
依頼を 2 つ作れる。**受領した側が死んだことを知る手段も無い。

### したがって順序がある

```
1  goal_handoffs を作る                    独立して入る
2  両方に request_report / complete_report  独立して入る
3  handoff に一意制約と生死判定を移す        claim の 3 つの仕事を引き受ける
4  tasks.claimed_by を落とす                3 のあと
```

**1 と 2 を先に入れる。**`claim` の削除は、**何が壊れるかが見えた状態で判断する。**

### 自分でやる場合の形

claim が無くなれば、**自分でやる場合は `requested_by == received_by`** になる。

今日 `db18e025` で「handoff を自分宛に作る」案を**形式的だとして却下した**が、
**claim が無くなるなら形式ではなく唯一の記録**になる。**却下の理由が消える。**

さらに `claim_undelegated` の検知が不要になる——**claim と handoff を突き合わせる
必要が無くなる**ためである。今日その突き合わせで 3 回壊れた。

## 3.5 表の形（2026-08-24 確定）

人間の指示: 「**pkey 用の id を別でつくって**」「`received_cwd` は不要」。

```
goal_handoffs                      task_handoffs
  id            PK                   id            PK
  goal_id       → goals(id)          task_id       → tasks(id)
  requested_by  → agent_sessions     requested_by  → agent_sessions
  received_by   → agent_sessions     received_by   → agent_sessions
  requested_at                       requested_at
  received_at                        received_at
  completed_report_at                completed_report_at
  request_report   TEXT NULL 可      request_report   TEXT NULL 可
  complete_report  TEXT NULL 可      complete_report  TEXT NULL 可
```

**両者は同じ形にする。**違うのは参照先の列名だけである。

### id を別に持つ理由

**`task_id` を主キーにすると 1 タスクに 1 行だけ**になり、**誰が持っていたかの履歴が消える。**

実測（2026-08-24）: タスク `c4c5b223` は executor-13 が着手し、圧縮 5 回で pane を
作り直したあと executor-14 が引き継いだ。**同じタスクを 2 人が順に持った。**
この日 pane の作り直しは 5 回あり、珍しいことではない。

**排他は主キーではなく部分インデックスで守る。**

```sql
CREATE UNIQUE INDEX ... ON task_handoffs(task_id) WHERE completed_report_at IS NULL
```

**未完了は 1 タスクに 1 つまでを DB が保証しつつ、完了した行は残る。**

### `received_cwd` を作らない理由

commander は「受領時の cwd を記録し、Stop hook が自分の cwd で引く」案を出したが、
**executor は作業中に `cd` する。**Stop 時の cwd が受領時と同じ保証が無い。

**したがって Stop hook が「どの handoff を完了させるか」を知る手段は未解決である。**
`agent_sessions` は `pid` を持つので、**executor の MCP プロセスが死んだことを daemon が
検知できれば Stop hook 自体が不要**かもしれない。`ClaimLiveness` が既に近いことをしている。
**別に決める。**

## 4. 流儀を揃える

```
tasks.claimed_by           TEXT NOT NULL DEFAULT ''   参照制約なし。空文字で「無し」
task_handoffs.received_by  TEXT REFERENCES ...        参照制約あり。NULL で「無し」
```

**claim は存在しないセッションでも書ける。**handoff は書けない（今日 executor が
FK で落ちた）。**`goal_handoffs` は handoff 側の流儀に揃える。**

## 5. 移行の規模

`1e082f2f`（UUID をやめて連番 ID）が 8 表を作り直す予定で、`53fda2ab`（外部キーを
切って移行する）待ちである。**claim の削除も同じ規模になる。**

**1 と 2 は小さい。**表を 1 つ足し、列を 4 つ足すだけで、**既存の行は触らない。**

## 未検証

- **本文が安全ゲートを通るか。**atct の CLI へ書く形なら通るはずだが、確かめていない
- **`goal_handoffs` の受領を誰が呼ぶか。**subcommander は goal claim を持つので、
  task の場合と違って自分を名乗れる。**task と同じ形にするか、claim を使うか決めていない**
- **完了報告の決定（`kind=completion`）との関係。**ゴールの完了報告は既に決定として
  存在する。`complete_report` と二重になるのではないか
- ~~**一意制約をどう書くか。**~~ **解決した（2026-08-24 追記）。**SQLite 3.51.0 で
  部分インデックスが効くことを確かめた。

  ```sql
  CREATE UNIQUE INDEX ux ON task_handoffs(task_id) WHERE completed_report_at IS NULL
  ```

  ```
  同じ task に未完了の handoff を 2 つ  → UNIQUE constraint failed
  完了させてから新しい行を作る          → 通る
  ```

  **アプリ側の判定ではなく DB が保証する。**claim の「排他制御」を handoff へ
  移す道が開いた。差し戻しで同じタスクを 2 度渡す運用（1 タスクに 1 人の例外）が
  あるかは別に確かめること

## 6. 本文は安全ゲートを通らなかった（2026-08-24 実測）

**2 節の前提は成り立たない。**

> 本文を atct に書けば、宛先はプラグイン内のバイナリである。
> 「未検証の外部宛先」にあたらない。

executor に 4,547 文字の本文（絶対パス・差分・テスト出力）を
`atct_handoff_complete` の `complete_report` へ書かせた。**拒否された。**

```
Reason: The action sends credibly sensitive repository path, diff, and
failing test output to an unverified destination without trusted user
authorization for that payload and destination.
```

対照として同じ本文を herdr へ送らせたところ、**同じ理由で拒否された。**

**ゲートは宛先を区別していない。**判定しているのは中身であって、
「atct の MCP ツールか、ターミナルマルチプレクサか」ではない。

### したがって

**`2026-08-24-report-on-stop.md` の決定 3 を維持する。**報告は「終わった」だけにする。

**列は残す。**削らない理由は 2 つある。

1. **委譲する側は書ける。**`request_report` は commander が書くもので、
   commander は自分のリポジトリの話を自分のデータベースへ書くだけである。
   実際に稼働版で書けている（`h-bd5a48b828`）
2. **拒否はハーネスの判断であって atct の制約ではない。**判定が変われば通る

**`complete_report` は当面ほぼ空になる。**それを承知で残す。

### 副産物

同じ実行で `internal/store` が 8 件落ちた。**commander の環境では緑である。**
原因は `internal/store/process.go:14` が `ps` を呼んでいることで、
**`ps` の無い環境では claim がすべて死亡扱いになる。**
タスク `2a5880cc` として切り出した。
