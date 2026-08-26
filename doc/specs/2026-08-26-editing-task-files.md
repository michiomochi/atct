# 宣言したタスクの files を直す

ゴール goal 134。実測は 2026-08-25 の 1 日（7 space が 1 つの作業ツリーを共有した日）。

## 問題

**`files` は commander が直列化を突き合わせる材料である。**

    sqlite3 -readonly ~/.atct/atct.db \
      "select group_concat(substr(id,1,8)||'/'||substr(goal_id,1,8),' ')
       from tasks where status in ('todo','doing') and files like '%<path>%';"

宣言の時点で決まるが、**依頼書を書く段になって範囲が変わる。**2026-08-25 に宣言外の
編集が 6 件出た。

    task 606  task_handoff_test.go / goal_handoff_test.go / goal.go / sqlcgen/task.sql.go
    task 614  internal/domain/model.go
    task 632  internal/store/decision_test.go

**どれも中身としては妥当である。**検査の置き場、`:exec` を `:execresult` に変えた
呼び出し側の追随、生成物。**依頼書を書く時点で決まることだが、`files` は宣言で固定される。**

結果として **commander が直列化を 2 回誤った。**

    task 604  「SKILL.md を触らない」と判断して待たせた -> 実際は触った
    decision 277  「tools.go が競合する」と伝えた         -> 実際は競合していなかった

## ゴール本文の前提を 1 つ訂正する

本文は「`atct_task_update_content` が受けるのは title と description だけ。**files は無い**」
と書いているが、**HEAD では 3 段とも `files` を通している。**

    internal/mcpshim/tools.go   TaskUpdateContentIn.Files *[]string
    internal/daemon/handler.go  case "task.update_content" の Files *[]string
    internal/store/task.go      UpdateTaskContent(ctx, taskID, title, description, files *[]string)
    internal/store/task_test.go TestUpdateTaskContentUpdatesFilesOnly

本文が引いているのは `Description` の**文字列**である。説明文に `files` と書いていない
だけで、入力型には入っている。**説明文を型の代わりに読んだ結果の誤りである。**

**したがって完了条件 1 は HEAD で満たされている。**観測できないのは稼働 daemon が
0.56.0 で、ツール自体が入っていないためである。**リリースで解ける。**

残る実作業は 3 つ。

    条件 2  done / dropped を拒む   判定は済。**files を渡す版の検査が無い**
    条件 3  保持者以外を拒む        **門番が無い**
    条件 4  突き合わせが動く        未実測

## 決めたこと

### 1. 門番は「ゴールの保持者」と「タスクの保持者」の or

    通す  タスクの handoff の received_by       executor。実装中に範囲が変わったとき
    通す  そのタスクのゴールの handoff の received_by  subcommander。分解した本人
    拒む  それ以外

**「保持者のみ」を字義どおり実装すると、このゴールが直そうとしている場面を塞ぐ。**
2026-08-25 の実測: ゴール goal 109 の 5 タスクは全部 `agent` が
`atct-goal 109-subcommander` で、**5 件とも handoff の保持者は executor だった。**
`files` を直したかったのは subcommander である。**保持者だけに絞ると、その 5 件すべてで
拒まれる。**

既存の `authorizeTaskStatusRelease` を写すのも駄目である。

    isHolder       -> 通す
    isProjectBound -> 通す   <- プロジェクトに属する全セッションが通る

**これでは条件 3 を満たさない。**他 space の subcommander も通ってしまう。

導出は既にある。`internal/store/task_handoff.go` の `requireGoalHandoffForTask` が
「そのタスクのゴールの handoff を保持しているか」を判定しており、`handoff.request` が
使っている。**`files` の門番はこれと `isHolder` の or である。**

`goal 127`（`goal.complete` の門番）の導出は使えない。**いま `goal.complete` に門番は
無く、`ensureAgentSessionProject` だけである。**逆に、こちらの導出が先に入れば写せる。

### 2. `request_report` と `complete_report` はまとめない

**まとめない。`files` だけにする。**

    files            直列化の材料。**機械が読んで判断する**
    request_report   依頼書。**人間と受け手が読む**
    complete_report  完了報告。同上

2026-08-25 の実測で両方踏んでいる。`request_report` にはソケット探り打ちの `probe` が
残り、正しい依頼書で上書きできなかった。**しかし執行は止まらなかった**——依頼書のパスを
prompt で直接渡せたためである。**`files` のずれは commander の直列化を 2 回誤らせた。**

**害の大きさが違う。**残り 2 つは `next_steps` に「判断して残した」と書く。

### 3. 条件 2 は `files` を渡す版を別に置く

既存の `TestUpdateTaskContentRejectsDone` / `RejectsDropped` は `title` を渡して拒否を見る。
**同じ判定を通るが、同じ検査ではない。**`files` だけを渡したときに拒まれることを別に置く。

## 検査

- **門番は肯定側と否定側を同数置く。**通す 2 経路（タスクの保持者・ゴールの保持者）と、
  拒む 2 経路（他 space の subcommander・無関係なセッション）
- **フィクスチャは経路ごとに 2 件以上。**1 件だと「常に通す」形でも通る
- 条件 4 は**実際に SQL を流す。**`files` は JSON 配列文字列で保存されるので、
  書き換えた値で `like '%<path>%'` が当たることを見る。**構造体の中身を見るだけでは足りない**
- **確かめるコマンドは `-run` で絞った形をそのまま報告に書く。**全体の色に依存させない

## 範囲外

- `request_report` と `complete_report`（決定 2）
- `goal.complete` の門番（`goal 127`）。導出はこちらが先に置く
- web UI からの編集経路
