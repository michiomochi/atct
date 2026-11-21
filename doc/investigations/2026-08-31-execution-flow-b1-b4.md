# Goal 202 B1〜B4 調査訂正

調査日: 2026-09-01

## 前回対応の破棄と今回の範囲

前回成果物が行った「B1〜B4 = `doc/execution-flow.md` の差分 0〜3」という対応付けは
破棄する。今回の B は次の Goal 202 の定義だけを使う。

- **B1**: 「順序は状態で守り、自力回復できる」原則と、人間却下後に commander が再 handoff
  必要となる状態の整合
- **B2**: 冒頭の「現状との差の最大」と、差分 0 が「他のすべての前提」とする主張の整合
- **B3**: 手順 25 の handoff 完了報告と手順 28 の 6 部完成報告の書き手・内容の関係
- **B4**: 手順 25〜29 の間に subcommander が持つ待機状態の定義

調査対象は `doc/execution-flow.md` の上記手順・図・原則と、それを確認するための必要最小限の
domain / store / daemon / HTTP API 実装である。Goal 216、launcher、shim、monitor、ユーザー設定、
dotfiles、ホーム、README、Claude/Codex manifests・skills は対象外とした。文書内に出てくる
skill 名は手順上の参照としてのみ扱い、対象外の skill 本体は調査していない。

`doc/execution-flow.md` は目標フローであり現状そのものではない
（`doc/execution-flow.md:3-6`、`doc/execution-flow.md:609-610`）。以下は、目標の主張と現行実装を
混同しないように分けて記録する。推奨は調査上の候補整理であり、実装方針の決定ではない。

## B1: 原則と、人間却下後の commander 再 handoff

### (1) 現状の文書・実装の事実と正確な file:line

#### 文書の手順・図・原則

- 「順序は状態で守る。間に合わない呼び出しが失敗し、失敗したら自力で回復できる」は
  `doc/execution-flow.md:8-16` の設計原則 2 にある（特に `:14`）。同じ節には「1 つの事実に
  1 つの書き手」もある（`:15`）。
- claim の判定は `doc/execution-flow.md:29-51` にあり、手順 25 の commander による
  `goal_handoff_complete` 後は claim の持ち主が「誰も持たない」とされる（`:43-47`）。
- handoff の状態表と責務は `doc/execution-flow.md:191-223` にある。レビュー差し戻しは
  handoff を閉じず、claim と役割を維持する（`:218-222`）。一方、人間のレビューには
  `review_receive` が無い（`:214-216`）。
- goal の目標経路は `doc/execution-flow.md:253-275`。手順 25a は
  `atct_goal_handoff_complete`、25b は `atct_goal_handoff_review_reject`、人間却下後は
  commander が新しい `atct_goal_handoff_request` を作ると記載されている（`:268-275`）。
- 「レビュー経路から再発行を消すが、人間却下だけは残る」という説明は
  `doc/execution-flow.md:280-304`。handoff review の差し戻しでは claim が維持されるが
  （`:290-291`）、手順 25 の完了で handoff が閉じるため、人間却下時は commander が
  再発行する、と明記されている（`:293-295`）。
- 人間却下の整理は `doc/execution-flow.md:345-375`。手順 25 で handoff を閉じ、却下時に
  commander が新しい handoff を作る理由は `:366-375` にある。

#### 現行実装

1. domain の状態型は `GoalStatus` が `proposed` / `active` / `done` / `dropped`、
   `TaskStatus` が `todo` / `doing` / `done` / `dropped` のみである
   （`internal/domain/status.go:5-21`）。`review` や「人間却下後の再 handoff 待ち」という
   専用状態はない。
2. `GoalHandoff` は request / receive / complete の3時点と request/complete report を
   持つだけで、review/reject の時点・理由・状態を持たない
   （`internal/store/goal_handoff.go:27-39`）。DB の現行 goal handoff 表も同じ列である
   （`internal/store/migrations/0019_integer_agent_session_ids.sql:100-109`）。
3. `CompleteGoalHandoff` は空でない scalar の `complete_report` を受け、
   `completed_report_at` と report を書く（`internal/store/goal_handoff.go:293-308`）。その後、
   委譲 handoff なら `handoff_reported` を通知するが、goal の状態や decision は変更しない
   （`internal/store/goal_handoff.go:323-346`）。SQL の条件は request 済みかつ未完了で、
   ID 指定経路では `received_at` を条件にしていない（`internal/store/queries/task.sql:248-251`）。
   goal ID から単一の受領済み未完了 handoff を選ぶ経路だけは receive 済みを確認する
   （`internal/store/goal_handoff.go:269-290`）。
4. `CompleteGoalWithReport` は6項目を検証し、goals の completion report 列を更新した上で、
   `kind='completion'` の open decision を作る（`internal/store/goal.go:361-377`、
   `internal/store/goal.go:412-470`）。これは handoff を閉じない。人間承認は
   `ApproveCompletion` が decision を applied にして goal を done にする
   （`internal/store/goal.go:472-512`）。
5. 現行の `RejectCompletion` は goal を active のまま decision を answered にし、条件が
   合えば同じ tx 内で別 ID の goal handoff を request して直ちに receive する
   （`internal/store/goal.go:514-600`）。再発行 ID は元 handoff ID と decision ID から作り、
   request report に却下理由を含める（`internal/store/goal.go:569-590`）。つまり現行コードは、
   文書の「commander が後で新規 request を呼ぶ」経路ではなく、Store の人間却下処理が条件付きで
   自動再発行する経路を持つ。
6. 自動再発行は常に起きるわけではない。既存の open handoff がある、完了済み handoff がない、
   あるいは decision の agent session が完了 handoff の receiver と違う場合は再発行しない条件が
   `internal/store/goal.go:544-600` にある。対応テストも、元 handoff の完了後に新しい open
   handoff ができる場合（`internal/store/goal_handoff_test.go:626-689`）、既存 open handoff が
   ある場合（`:691-730`）、別 session の場合（`:765-823`）を分けている。
7. 死んだ owner の回復は別経路である。`reclaimOpenGoalHandoff` は open owner が確実に停止
   している場合だけ既存 handoff を `セッションが停止した` という report で完了し、新しい request
   を許す（`internal/store/goal_handoff.go:122-159`）。live/不明 owner は再利用を拒否する。
   これは「失敗したら回復できる」ための現行の liveness 回復だが、人間却下の状態とは別である。
8. `goal.complete` の daemon dispatch は、project claim または open goal handoff holder を
   認可し（`internal/daemon/handler.go:355-384`）、6項目を Store に渡す
   （`internal/daemon/handler.go:1433-1470`）。人間の approve/reject は HTTP の decision
   endpoint から `ApproveCompletion` / `RejectCompletion` に分岐する
   （`internal/httpapi/server.go:1243-1318`）。この現行 API には、文書どおり commander が
   人間却下後に明示的に再 handoff を実行したことを表す専用状態はない。

### (2) B1 と他 B の依存

- **B2**: B2 が「差分 0 は全体の前提」と読むなら、順序を守るための状態モデルと人間却下の
  回復経路は B1 の確認対象になる。B2 が「最大」を別の軸と読むなら、B1 の実装順序を文書の
  最上位と同一視できない。
- **B3**: 手順 25 の handoff 完了が claim を消すかどうかが、人間却下後の再 handoff を必要に
  するか、また再発行された handoff がどの report を引き継ぐかを決める前提になる。
- **B4**: 手順 25〜29 の subcommander が claim 無しで待つのか、却下時に同じ handoff を
  再利用するのかは、B1 の「閉じる／開いたまま／自動再発行」の選択に依存する。

### (3) 人間が選べる案と各結果

#### 案 A: 文書どおり commander が人間却下後に新しい handoff を request する

- 結果: 手順 25 で handoff を閉じて claim を手放すという文書
  （`doc/execution-flow.md:43-47`、`:368-372`）と、却下後の commander の責務が一致する。
- 結果: 「自力回復」を通常の呼び出し失敗に限定し、人間却下は commander が実行する明示的な
  回復例外として定義する必要がある。commander が停止中なら subcommander だけでは作業に
  戻れない。
- 結果: 現行の `RejectCompletion` が行う自動再発行（`internal/store/goal.go:544-600`）を
  削除・限定・明示的な commander 操作へ移すか、互換経路として残すかの追加判断が必要になる。

#### 案 B: 人間却下を state transition として自動回復し、元の担当を再 handoff する

- 結果: 現行 `RejectCompletion` とテストに近く、人間の却下から同じ tx で active goal と
  open/received handoff を復元できる。commander の別 call が無くても subcommander が続行できる。
- 結果: `RejectCompletion` が再発行した handoff の requester/receiver、却下理由、再試行回数を
  状態として記録しないため、「自力回復」の観測可能な境界を追加で定義する必要がある。
- 結果: 文書の「人間却下だけは commander が新しく作る」という説明
  （`doc/execution-flow.md:366-375`）と、commander の責務表を更新しない限り不整合が残る。

#### 案 C: handoff を閉じず、`rejected` / `recovery_pending` のような中間状態を記録する

- 結果: human reject、回復待ち、再受領、再完了を一つの状態機械で追跡でき、順序違反と回復の
  検査条件を明示しやすい。
- 結果: 手順 25 の `completed_report_at` で claim が無くなるという現行文書と異なり、
  handoff の列・claim 判定・role 導出・通知を追加変更する必要がある。
- 結果: 自動回復を許すか commander の承認を挟むかは中間状態の次の遷移として人間が選べる。

### (4) 調査上の推奨と理由

実装方針の決定ではなく、調査上は **案 C のように「人間却下から回復まで」を明示的な状態
遷移として定義すること**を推奨する。理由は、文書の手動再 handoff（案 A）と現行コードの
条件付き自動再発行（案 B）がすでに異なる事実として存在し、どちらかを暗黙に採ると
「自力回復」の成否をテストできないためである。中間状態を自動で次へ進めるか commander の
操作に留めるかは、案 A/B/C の結果を踏まえて人間が決める事項であり、この調査では決めない。

### (5) 本文へ反映する exact sections

- `doc/execution-flow.md:8-16` の `## 設計の原則`: 「自力回復」の適用範囲と人間却下の
  例外扱いまたは状態遷移を明記する。
- `doc/execution-flow.md:29-51` の `## 役割はどこから決まるか`: 手順 25 後の claim 所有者と、
  再 handoff / 自動再受領後の role を実装と一致させる。
- `doc/execution-flow.md:53-119` の `## 全体フロー` Mermaid 図: human reject から新規
  handoff、同一 handoff の再開、または recovery state への分岐を実際の API に合わせる。
- `doc/execution-flow.md:191-223` の `## handoff は状態で動く`: complete/reject/recovery の
  writer、claim の維持・消失、状態遷移の順序を確定値で記載する。
- `doc/execution-flow.md:280-304` の `### これで再発行はレビューの経路から消える` と
  `doc/execution-flow.md:345-375` の人間レビュー節: handoff review reject と human reject
  の差、および commander の再 handoff の実体を更新する。
- `doc/execution-flow.md:625-640` の差分 0: review/recovery の状態と human reject の
  保存・通知・検査結果を未解決事項から確定事項へ反映する。

### 実装で読むべきファイル・関数

- `internal/domain/status.go:5-21`: `GoalStatus` / `TaskStatus` の許可値。
- `internal/store/goal_handoff.go:122-159,269-346`: `reclaimOpenGoalHandoff`、
  `CompleteGoalHandoffForGoal`、`CompleteGoalHandoff`。
- `internal/store/goal.go:412-470,472-613`: `CompleteGoalWithReport`、
  `ApproveCompletion`、`RejectCompletion`。
- `internal/store/queries/task.sql:215-255` と
  `internal/store/migrations/0019_integer_agent_session_ids.sql:100-120`: goal handoff の
  query 条件と schema。
- `internal/daemon/handler.go:355-384,1189-1223,1433-1470`: completion authorization と
  goal handoff / goal complete dispatch。
- `internal/httpapi/server.go:1243-1318`: 人間の approve/reject の dispatch。
- 検査の候補は `internal/store/goal_handoff_test.go:626-823` と
  `internal/daemon/goal_handoff_test.go:222-238`。

## B2: 「現状との差の最大」と差分 0 の「全ての前提」

### (1) 現状の文書・実装の事実と正確な file:line

#### 文書の主張

- 冒頭は文書が目標であり、現状との差を後段に列挙するとする
  （`doc/execution-flow.md:3-6`）。
- 原則・責務表の直後に「現状との最大の差は『ゴールの完了報告』が commander に移ること」
  とある（`doc/execution-flow.md:18-27`、特に `:26-27`）。同じ責務表は commander に設計/goal
  review/goal 完了報告を、subcommander に goal 設計・実装 review・人間への決定起票を置く
  （`:20-24`）。
- 現状との差分一覧は `doc/execution-flow.md:607-623`。差分 0 は review と human review、
  差分 1〜3 は handoff/status/session/role、差分 4 が「完了報告を commander が書く」であり、
  差分 4 の担当は Goal 192、差分 0〜3 は未着手である（`:611-617`）。
- 差分 0 の冒頭には「この差分が他のすべての前提になる」とある
  （`doc/execution-flow.md:625-640`、特に `:627`）。同節は review tools、plan handoff、
  status、通知、検査を差分 0 の必要変更として列挙する（`:632-640`）。
- 完了報告を commander が書く理由は `doc/execution-flow.md:467-489`。人間の指示は
  executor → subcommander → commander の順で、commander が goal 全体を review 後に完了報告を
  提出する（`:469-474`）。ただし commander の最終 review と6部報告の量は測定していない
  （`:485-489`）。

#### 現行実装の確認材料

1. 現行 role boundary は commander の `Does` に review/publish/cleanup を、subcommander の
   `Does` に `report completion for the goal` を置く（`internal/daemon/handler.go:62-65`）。これは
   文書の目標責務（commander が goal completion report）とは異なり、差分 4 が未実装である
   ことを示す。
2. ただし6項目の completion report 自体は既に domain にある
   （`internal/domain/model.go:23-30`）。Store は active goal に6項目を書き、completion
   decision を作る（`internal/store/goal.go:412-470`）。したがって差分 4 の「書く内容」は
   現行にあるが、誰が書くかの目標責務は固定されていない。
3. review の永続基盤は現行 handoff struct に無く、goal handoff は3 timestamp と2 reportだけで
   ある（`internal/store/goal_handoff.go:27-39`）。task も同じ構造である
   （`internal/store/task_handoff.go:28-40`）。少なくとも domain/store の永続モデルには、差分 0
   が列挙する `review_request` / `review_receive` / `review_reject` の一般的な handoff 状態はない。
4. 完了報告の実装も二層に分かれる。handoff の `CompleteGoalHandoff` は scalar report と
   completion timestamp だけを書く（`internal/store/goal_handoff.go:293-346`）一方、goal の
   `CompleteGoalWithReport` は6項目を goals に書いて decision を作る
   （`internal/store/goal.go:412-470`）。従って「最大の差」と「前提」は、少なくとも
   責務移動と状態基盤という異なる軸で観測できる。

### (2) B2 と他 B の依存

- **B1**: 差分 0 を全ての前提と呼ぶなら、順序・回復の例外を含む B1 が差分 4 より先に
  定義される。B1 が別の例外状態を選ぶと、差分 0 の「前提」の範囲も変わる。
- **B3**: 差分 4 の責務移動を評価するには、手順 25 の scalar handoff report と手順 28 の
  6部 report を別の事実として区別する必要がある。これを混同すると「最大の差」の対象が
  書き手なのか report schema なのか不明になる。
- **B4**: 差分 0 が review/wait state を前提にするか、差分 4 を先に実装して人間 review の
  待機を作るかで、手順 25〜29 の subcommander の待機定義が変わる。

### (3) 人間が選べる案と各結果

#### 案 A: 「最大」は責務・利用者から見た最大差、「全ての前提」は依存関係として併記する

- 結果: commander が最終 goal report の書き手になることを最大の役割差として残し、review
  state はその責務を安全に実行するための基盤前提と解釈できる。
- 結果: 実装順は差分 0 → 差分 4 になるが、差分一覧の番号と Goal 192 の担当は維持できる。
- 結果: 「最大」が実装量や優先順位を意味しないことを冒頭で明記しない限り、読者は差分 0
  が最大なのに差分番号が0なのではないかと再び解釈に迷う。

#### 案 B: 「最大」を実装・依存上の最大の差と読み、差分 0 を冒頭の最大差にする

- 結果: review state が他の全ての前提という記述と、冒頭の最大差を同じ軸に揃えられる。
- 結果: 「commander が完了報告を書く」という責務移動の強調が弱まり、差分 4 の Goal 192
  という担当や差分番号を再整理する必要がある。
- 結果: 現行に既に6項目 report の保存・decision 経路がある事実に対し、review 基盤の未実装を
  最大と呼ぶ根拠（依存数、変更量、事故影響など）を別途測定する必要がある。本文は最大差の
  指標を提示していない（`doc/execution-flow.md:485-489`）。

#### 案 C: 「最大の責務差」「前提となる基盤差」「実装順」を別の軸として図示する

- 結果: 冒頭の最大差（責務移動）、差分 0 の前提（状態・review 基盤）、差分 4 の実装担当
  （Goal 192）を同時に保持できる。
- 結果: 依存関係の図または短い表を追加するため文書量は増えるが、番号を意味の違う優先順位
  として読まれにくくなる。
- 結果: B1/B3/B4 の状態・report・待機の決定を反映する欄が必要になり、各差分の更新責任を
  明示できる。

### (4) 調査上の推奨と理由

調査上は **案 C** を推奨する。現行文書には、責務の大きさ（`:26`）、依存関係（`:627`）、
実装担当（`:617`）が同じ「差」の言葉で並んでいるが、最大性の測定値はない。三つの軸を
分けるだけなら、commander の責務移動を弱めず、差分 0 を基盤前提とする主張も維持できる。
どの軸を正式な優先順位とするか、また差分 4 の担当を差分 0 より先にするかは人間が決める
事項であり、この調査では決めない。

### (5) 本文へ反映する exact sections

- `doc/execution-flow.md:3-6` の冒頭: 文書が目標であることに加え、「最大」の意味（責務差、
  依存差、実装優先度のどれか）を定義する。
- `doc/execution-flow.md:18-27` の `## 層とその責務`: commander の責務移動を最大と呼ぶ
  基準、または差分 0 を最大基盤と呼ぶ基準を選択結果に合わせる。
- `doc/execution-flow.md:467-489` の `## なぜ完了報告を commander が書くのか`: 6部 report の
  writer、最終 review、負担測定の関係を B3 の結果と揃える。
- `doc/execution-flow.md:607-623` の差分一覧: 番号が依存順・影響度・担当のどれを表すかを
  明示し、必要なら差分 0/4 の説明を分離する。
- `doc/execution-flow.md:625-640` の差分 0: 「全ての前提」の範囲を、B1/B3/B4 の状態・
  report・待機との依存関係を含む確定値に更新する。

### 実装で読むべきファイル・関数

- `internal/daemon/handler.go:62-65`: 現行 role boundary の `Does` / `DoesNot`。
- `internal/domain/model.go:23-30`:6項目 `CompletionReport`。
- `internal/store/goal.go:345-377,412-470`: report validation と
  `CompleteGoalWithReport`。
- `internal/store/goal_handoff.go:27-39,293-346` と
  `internal/store/task_handoff.go:28-40`: 現行 handoff report/state の粒度。
- `internal/daemon/handler.go:355-384,1433-1470`: completion を呼べる session と dispatch。
- 数量や負担を実装から推測せず、本文の未測定箇所
  `doc/execution-flow.md:485-489` を人間の判断材料として扱う。

## B3: 手順25の handoff 完了報告と手順28の6部完成報告

### (1) 現状の文書・実装の事実と正確な file:line

#### 文書の手順・図

- 全体図の commander 節は手順 25 を `atct_goal_handoff_complete` と「完了報告を出す」、
  手順 28 を `atct_goal_complete（6 部）` と書き分けている
  （`doc/execution-flow.md:59-74`、特に `:69-73`）。
- Mermaid の遷移は handoff review 受理後、25 で handoff complete、26 で human review request、
  人間の complete 後に27で merge、28で `atct_goal_complete`、29で cleanup としている
  （`doc/execution-flow.md:112-119`）。
- 詳細手順でも、25a は `atct_goal_handoff_complete` で完了報告、続いて26、27、28の6部報告、
  25b は handoff review reject と記載されている（`doc/execution-flow.md:253-275`）。
- 人間 review の説明は 25 の handoff 完了報告と 28 の `goal_complete` を同じ列に並べる
  （`doc/execution-flow.md:345-355`）。一方、役割表は commander に goal 完了報告を割り当てる
  （`doc/execution-flow.md:18-27`）、人間の指示も commander が goal 全体を review 後に goal
  完了報告を提出するとする（`doc/execution-flow.md:467-474`）。
- `complete_report` は受理時に書き、差し戻し理由とは別にするという説明がある
  （`doc/execution-flow.md:306-310`）。差分 0 も `complete_report` は受理のときに書かれる
  としている（`doc/execution-flow.md:637`）。

#### 現行実装

1. handoff の report は scalar 文字列である。`GoalHandoff.CompleteReport` と
   `CompletedReportAt` は handoff row の属性である（`internal/store/goal_handoff.go:27-39`）。
   `complete_report` は request/receive と別に、handoff 完了時に保存される
   （`internal/store/goal_handoff.go:293-346`）。空文字は拒否され、エラーメッセージは「何を
   したか、何を検証したか、変更 path」を説明する report を要求する
   （`internal/store/goal_handoff.go:19-20,296-298`）。
2. DB/query も scalar report である。goal handoff schema は
   `requested_at` / `received_at` / `completed_report_at` / `request_report` /
   `complete_report` だけで（`internal/store/migrations/0019_integer_agent_session_ids.sql:100-109`）、
   query は `complete_report` と完了時刻を更新する（`internal/store/queries/task.sql:248-255`）。
3. goal 完了 report は別の型・保存先で、`CompletionReport` に
   `work_done`、`now_possible`、`how_to_verify`、`surprises`、`needs_review`、`next_steps` の
   6項目がある（`internal/domain/model.go:23-30`）。Store は6項目を全て非空・長さ検証し
   （`internal/store/goal.go:345-377`）、goals row と `result_summary` を更新してから
   `kind='completion'` decision を作る（`internal/store/goal.go:412-470`）。
4. daemon の `goal.handoff.complete` は scalar `complete_report` のみを受け、
   `CompleteGoalHandoff` を呼ぶ（`internal/daemon/handler.go:1207-1223`）。`goal.complete` は
   6項目を受け、`CompleteGoalWithReport` を呼ぶ（`internal/daemon/handler.go:1433-1470`）。
   したがって現行 wire/Store 経路には二つの report schema がある。
5. 文書の目標フロー上の手順25と28の書き手は、どちらも commander 節に置かれている
   （`doc/execution-flow.md:59-74`）。ただし現行 role boundary は subcommander に
   `report completion for the goal` を置き、commander の `Does` にはそれを置かない
   （`internal/daemon/handler.go:62-65`）。`goal.complete` の authorization は project claim
   または open goal handoff holder を許すため、現行コード上は commander/subcommander のどちらも
   条件を満たし得る（`internal/daemon/handler.go:355-384`）。
6. 文書の時系列と現行 `goal.complete` の意味にも差がある。現行 `CompleteGoalWithReport` は
   open completion decision を作り、goal を active のままにする
   （`internal/store/goal.go:412-470`）。goal が done になるのは human approve の
   `ApproveCompletion` 後（`internal/store/goal.go:472-512`）。よって現行 `goal.complete` は
   「人間承認後の6部確定報告」ではなく、少なくとも decision を作る completion 提出経路である。
7. 現行テストも二つを別概念として扱う。6項目が goal に保存されることと goal がまだ active
   であることは `internal/store/goal_complete_test.go:113-148`、approve/reject が別処理である
   ことは `internal/store/goal_complete_test.go:168-230`、handoff の scalar report は
   `internal/store/goal_handoff_test.go:626-689` 周辺の handoff 完了・再発行テストで確認対象に
   されている（今回テスト実行はしていない）。

### (2) B3 と他 B の依存

- **B1**: 手順 25 の report が handoff を閉じるなら claim が無くなり、human reject の回復は
  B1 の再 handoff/自動再発行の問題になる。6部 report がいつ書かれるかも、人間却下前後の
  回復可能性に影響する。
- **B2**: 「最大の差」が writer の移動を指すなら、B3 は手順25の writer と手順28の writer を
  混同せず、責務差として記述するための材料になる。差分 0 を前提とするなら、B3の二つの
  report schema は review state の前提と別に扱う必要がある。
- **B4**: 手順25の後に subcommander が待つ状態を定義するには、25で scalar report と handoff
  を確定した後、6部 report/decision をいつ作るかを確定する必要がある。

### (3) 人間が選べる案と各結果

#### 案 A: 2つの report を別の事実として残し、同じ commander が順に書く

- 手順25: commander が `goal_handoff_complete` の scalar `complete_report` を書き、handoff
  の受理・closure を記録する。
- 手順28: commander が `goal.complete` の6項目を goals に書き、goal completion report の
  source of truth とする。
- 結果: 現行の二つの保存先・schemaを大きく変えずに、handoff 受理報告と goal 最終報告を区別
  できる。本文の「同じ commander 節」という書き手も保てる。
- 結果: 同じ事実を scalar と6項目に二重入力する場合は内容の不一致が起きるため、25の
  `complete_report` を受理理由/検証要約に限定する契約が必要になる。

#### 案 B: 手順25で6項目 report を canonical に作り、手順28は同じ内容を promote/mirror する

- 結果: report schema を一つに揃えられ、6部の欠落を手順25で検出できる。
- 結果: handoff 完了前に6項目を確定することになり、文書が手順28に置く人間 approve・merge
  後の6部報告と時系列が変わる。handoff row に6列を置くか、手順28で goals に移す処理が必要。
- 結果: handoff と goal の二つの保存場所を mirror するなら「1つの事実に1つの書き手」と
  「二重保存」の境界を追加で決めなければならない。

#### 案 C: 手順25は handoff 受理の短い証跡だけ、手順28を6部 report の唯一の書き込みにする

- 結果: 手順25と28の内容重複を最小にし、6部 report の source of truth を goals に固定できる。
- 結果: 現行 `CompleteGoalHandoff` は空でない `complete_report` を要求し、何をしたか/検証/path
  を求めるため（`internal/store/goal_handoff.go:19-20,293-298`）、短い受理証跡に契約を変える
  か、要求を分ける必要がある。
- 結果: 手順25後の human review 中は6部 report がまだ無い状態になり、B4 の待機状態に
  「handoff は閉じたが final report は未作成」という組合せを追加する。

### (4) 調査上の推奨と理由

調査上は **案 A を「report の意味を別名で明示する」形で推奨する**。現行実装が handoff scalar
と goal 6項目を別表・別関数・別 API として既に分け、6項目 report は `goal.complete` から
decision を作るため、無理に手順25へ移すより保存の責務を区別する方が現在の事実に近い。
ただし、手順25の scalar がどの程度の詳細を持つか、手順28の6項目が human approve 前か後か、
同じ内容の重複を許すかは人間の選択であり、最終的な設計判断はしていない。

### (5) 本文へ反映する exact sections

- `doc/execution-flow.md:18-27` の `## 層とその責務`: handoff の受理報告と goal 6部完了報告の
  書き手を区別し、commander/subcommander の現行・目標差を明記する。
- `doc/execution-flow.md:53-74` の `## 全体フロー` Mermaid 図: 手順25の report の目的と
  手順28の6部 report の目的・時点を別ラベルにする。
- `doc/execution-flow.md:112-119` と `doc/execution-flow.md:253-275` の遷移説明: 人間 review、
  merge、6部 report、handoff closure の前後関係を選択結果に揃える。
- `doc/execution-flow.md:306-310` と `doc/execution-flow.md:345-375`: `complete_report` と
  6部 report、reject reason の保存先・書き手を確定値で書く。
- `doc/execution-flow.md:467-489` の完了報告理由: 「レビューした者が書く」の対象が手順25か
  手順28か、6部 report の負担測定を何に使うかを更新する。
- `doc/execution-flow.md:669-676` の差分4: commander が書く report の schema・call順を現行
  API と実装後の契約に置き換える。

### 実装で読むべきファイル・関数

- `internal/domain/model.go:23-30`: `CompletionReport` の6項目。
- `internal/store/goal_handoff.go:19-20,27-39,293-346`: scalar handoff report、完了条件、
  `CompleteGoalHandoff`。
- `internal/store/queries/task.sql:215-255` と
  `internal/store/migrations/0014_handoff_reports.sql:1-4`、
  `internal/store/migrations/0019_integer_agent_session_ids.sql:100-120`: handoff report の
  schema/query。
- `internal/store/goal.go:345-377,412-512`:6項目検証、`CompleteGoalWithReport`、approve。
- `internal/daemon/handler.go:62-65,355-384,1207-1223,1433-1470`: role boundary、認可、
  handoff complete/goal complete dispatch。
- `internal/httpapi/server.go:1243-1318`: human approve/reject が6部 report の decision と
  どう接続するか。
- 検査の候補は `internal/store/goal_complete_test.go:113-230` と
  `internal/store/goal_handoff_test.go:626-689`。

## B4: 手順25〜29の間の subcommander 待機状態

### (1) 現状の文書・実装の事実と正確な file:line

#### 文書上の経路

- 全体図は、subcommander の手順22 `atct_goal_handoff_review_request`、commander の23/24、
  25 `atct_goal_handoff_complete`、26 human review request、27 merge、28 `atct_goal_complete`、
  29 subcommander close/cleanup を示す（`doc/execution-flow.md:77-90`、`:59-74`）。
- 詳細手順でも、25a の受理後は26へ、human complete 後に27/28/29へ、human reject 後は
  commander が新しい handoff を作り subcommander が5から受け直す
  （`doc/execution-flow.md:266-275`）。
- claim 表は手順25後に誰も goal claim を持たないとする
  （`doc/execution-flow.md:39-47`）。subcommander を閉じる時点は手順29で、human review complete
  の後である（`doc/execution-flow.md:315-323`）。したがって文書の手順25〜29には、
  「プロセスはまだ生きているが goal claim は無い」という待機候補があるが、固有の状態名・
  状態フィールドは書かれていない。
- 通知節は review と review 後の再開が通知で始まり、`goal_handoff_complete` は subcommander
  に「完了報告が出たと分かる」とする（`doc/execution-flow.md:545-581`、`:560-580`）。人間の
  `atct_goal_review_complete` / `_reject` の宛先は commander である（`:580`）。
- `doc/execution-flow.md:218-222` は review reject なら handoff/claim/role を維持するとするが、
  human reject は別で、25で閉じた handoff のため新規 handoff を作るとする
  （`doc/execution-flow.md:366-375`）。

#### 現行実装で観測できる状態

1. `GoalStatus` に待機専用値はなく、active のまま completion decision を持つか、done/dropped
   になるかである（`internal/domain/status.go:5-12`）。
2. `ListOpenGoalHandoffs` は `completed_report_at IS NULL` の行だけを open とする
   （`internal/store/queries/goal_handoff.sql:1-7`）。`deriveSessionRole` も received/open
   goal handoff の holder を subcommander として導出する
   （`internal/daemon/handler.go:156-190`）。したがって handoff 完了後は、元の subcommander
   session がプロセスとして生きていても、この handoff から subcommander role は導出されない。
3. 現行 `CompleteGoalWithReport` は goals の6項目を更新し、open completion decision を作る
   （`internal/store/goal.go:412-470`）。`ApproveCompletion` は decision applied と goal done
   を一 tx で行う（`internal/store/goal.go:472-512`）。`RejectCompletion` は goal active を
   維持し、条件付きで元 receiver の新しい open/received handoff を作る
   （`internal/store/goal.go:514-600`）。
4. decision には open / answered / applied の状態がある。人間却下などの回答は
   open → answered になり、answered decision は `PollDecisions` が agent session に返した時に
   applied にする（`internal/store/decision.go:402-455`）。completion の approve は
   `ApproveCompletion` が open から applied までを一 tx で行う
   （`internal/store/goal.go:472-512`）。つまり現行には「人間回答を待つ decision」の永続状態は
   あるが、「subcommander が手順25〜29で待機中」という process/lifecycle 状態はない。
5. daemon の goal list は active goal のうち open completion decision があるものを
   `awaitingApproval` として通常の task 表示から除外する（`internal/daemon/handler.go:707-728`）。
   HTTP inbox は open decisions と active goals を別に返す
   （`internal/httpapi/server.go:421-550`）。これは human approval の表示上の待機を表すが、
   subcommander の process が alive/closed か、手順25後に claim が無いかは表さない。
6. `goal.sessions` は handoff を受けた session の role と `HandoffOpen` を返す
   （`internal/store/queries/goal_handoff.sql:24-46`、`internal/store/goal_handoff.go:404-419`、
   `internal/daemon/handler.go:821-832`）。`HandoffOpen=false` は分かるが、human review 待ち、
   cleanup 待ち、再 handoff 待ちの区別はない。
7. 現行の human reject 自動回復テストは、rejection 後に subcommander role が復元されることを
   `internal/daemon/goal_handoff_test.go:222-238` で確認している。これは文書の「25後は誰も
   claim を持たない」待機モデルではなく、reject 処理後に新しい handoff が受領済みになる
   実装モデルを示す（今回テスト実行はしていない）。

### (2) B4 と他 B の依存

- **B1**: human reject 後に commander が新規 handoff を作るなら、手順25〜29の待機は
  `handoff closed + no claim` となる。自動再発行または handoff 維持なら待機中も role/claim が
  残り、B4 の定義が変わる。
- **B2**: 差分 0 を全ての前提とするなら、待機状態は review state と通知の一部として先に
  定義される。最大差を責務移動とだけ読むなら、B4 は差分4の完了報告時系列を補足する。
- **B3**: 手順25の scalar report と手順28の6部 report の間に human decision があるかどうかが、
  B4 の待機開始・終了条件になる。現行コードの `goal.complete` が decision を作る事実も
  待機区間の解釈を変える。

### (3) 人間が選べる案と各結果

#### 案 A: 待機を複合状態として定義し、DBの既存事実から導出する

手順25〜29の待機を次の組合せとする。

`goal_handoff.completed_report_at != NULL`、`goal.status = active`、human review 用の
completion decision が未解決、subcommander のプロセス/watch は生存、goal claim は無し。

- 結果: 文書の「25後は claim を誰も持たない」と「subcommander は29まで閉じない」を同時に
  表現できる。human approve なら commander が27/28/29へ進み、reject なら新しい handoff を
  request して subcommander が5へ戻るという分岐になる。
- 結果: 既存 DB の handoff、goal、decision から判断できるため新しい status 値を増やさずに
  定義できるが、process が alive かどうかは DB だけでは判定できない。
- 結果: どの event が subcommander の待機を終わらせるか、commander が停止した場合に誰が
  recovery を開始するかを別途定義する必要がある。

#### 案 B: `awaiting_human_review` などの goal/review 状態を永続化する

handoff の完了とは別に、goal review request、human decision pending、approved、rejected/reopen
を明示する state/row を持たせる案。

- 結果: handoff が閉じて claim が無い状態でも、subcommander の待機理由と再開条件を DB/API で
  直接取得できる。プロセス再起動後も「29まで待つ」か「再 handoff を待つ」かを復元しやすい。
- 結果: `GoalStatus`、decision、handoff のどれを source of truth にするか、現行の active-only
  list/inbox と `goal.sessions.HandoffOpen` をどう更新するかを追加で決める必要がある。
- 結果: B1の自動再発行、B3の6部 report 時点、通知イベントの完了条件を同じ state machine に
  接続する設計・migration・検査が増える。

#### 案 C: handoff を手順29まで開いたまま subcommander claim を維持する

25では report だけを記録して handoff を閉じず、human approve 後の29で commander が complete
する。human reject は同じ handoff の受領側へ戻す案。

- 結果: subcommander role/claim を既存の open handoff から導出でき、待機中の actor と通知先を
  単純に表現できる。新しい handoff の再発行も不要になる。
- 結果: 文書の claim 表（手順25後は誰も持たない）、`作業した者は自分の handoff を閉じない`、
  human reject だけは新規 handoff という記述（`doc/execution-flow.md:43-47,218-222,366-375`）
  と直接矛盾する。
- 結果: commander が handoff complete を呼ぶ時点と、手順25の「完了報告」を何と呼ぶかを
  変更し、B1/B3の再定義が必要になる。

### (4) 調査上の推奨と理由

調査上は **案 A を「待機の意味論」として先に明文化すること**を推奨する。理由は、現在の
本文が手順25で claim を手放し、29で subcommander を閉じるという二つの事実を既に置いており、
それらを壊さずに待機開始・終了・human reject 分岐を定義できるためである。プロセス再起動後も
待機を復元する要件がある場合は案 B を比較対象にする。claim を維持する案 C は既存文書の
明示的な状態と衝突するため、採用可否は人間が B1/B3 と合わせて判断する。

### (5) 本文へ反映する exact sections

- `doc/execution-flow.md:39-51` の claim 表: 手順25後の claim 無しと、human reject で再受領
  するまでの role を待機状態の定義に合わせる。
- `doc/execution-flow.md:53-119` の全体図: 25→26→27→28→29の待機区間、approve/reject の
  終了条件、再 handoff の通知経路を図示する。
- `doc/execution-flow.md:191-223` の handoff 状態: handoff closed と process waiting の
  同時成立、または handoff-open 案を選んだ場合の状態を明記する。
- `doc/execution-flow.md:266-275` の goal 手順、`doc/execution-flow.md:315-323` の close
  ルール: 29まで subcommander が何をしないで待つか、どの event で次へ進むかを書く。
- `doc/execution-flow.md:345-375` の human review: approve/reject が待機をどう終わらせるか、
  reject の新規 handoff と受領位置を確定する。
- `doc/execution-flow.md:545-581` の通知先: `goal_handoff_complete`、human decision、
  新規 handoff request の各宛先と、subcommander が待機中に受け取る event を更新する。
- `doc/execution-flow.md:669-676` の差分4: 6部 report と human approval 後の待機終了を
  B3/B4の選択結果と一致させる。

### 実装で読むべきファイル・関数

- `internal/domain/status.go:5-12`: goal status に待機値があるか。
- `internal/store/goal_handoff.go:27-39,162-183,269-346,386-437`: handoff の open/complete
  判定、`ListGoalSessions`、`ListOpenGoalHandoffs`。
- `internal/store/queries/goal_handoff.sql:1-7,24-46`: open handoff と session/role/
  `handoff_open` の SQL。
- `internal/daemon/handler.go:156-190,707-728,821-832`: role 導出、goal list の
  `awaitingApproval`、goal sessions dispatch。
- `internal/store/goal.go:412-613` と `internal/store/decision.go:402-485`: completion decision
  の open/answered/applied と human reject 後の handoff recovery。
- `internal/httpapi/server.go:421-550,553-679`: inbox/goal detail が現在どの複合状態を表示するか。
- 検査の候補は `internal/store/goal_handoff_test.go:626-823` と
  `internal/daemon/goal_handoff_test.go:222-238`。

## B 間の依存まとめ

```text
B2: 「最大の差」と「前提」の軸を分離する
 └─ B1: 順序・回復の状態を確定する
     ├─ B3: 手順25 scalar report と手順28 6部 report の意味・writer
     └─ B4: 手順25〜29の subcommander 待機と終了イベント
```

これは実装順を決定する図ではない。B1〜B4 の定義が相互に参照する箇所を調査上整理しただけ
であり、実装計画の決定は人間に残す。

## 検証境界

今回の訂正では、前回成果物の誤った B 対応を削除し、指定された B1〜B4 の定義で同じ Markdown
成果物を全面更新した。コード・本文（`doc/execution-flow.md`）・設定・skills 本体の変更、commit、
テスト実行はしていない。確認したのは静的な行番号付き読解と、実装の call/query/schema 関係で
ある。DB migration の適用、MCP wire、SSE/通知の実動作、process の生死、既存テストの green/red
は未検証である。

変更 path は次の1件だけである。

- `doc/investigations/2026-08-31-execution-flow-b1-b4.md`
