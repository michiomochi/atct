# 通常 decision の Goal Detail 導線回帰調査

- 調査日: 2026-09-04 JST
- 対象: Task 1068 / Goal 225
- 範囲: `superpowers:systematic-debugging` Phase 1–3。調査のみ。
- 外部状態の変更: なし。approve / reject / submit / form fill は実行していない。

## 結論

Goal Detail で task に紐付かない通常 `kind: "decision"` を表示・回答できない状態は再現した。一方、API・daemon・store は通常 decision を保持しており、task に紐付く通常 decision の Task Detail 導線は既存の安全な状態で表示・回答フォームまで到達できた。

単一の根本原因仮説は、`33547a4` の Goal page 再構成で、既存の `UnattachedDecisionList` の表示導線だけが削除されたこと。最小の変更境界は、Goal Detail の既存 `unattached_decisions` から特殊 decision 以外を表示する既存導線の復元に限ること。daemon、store、API、判断基盤、migration、snapshot、Task Detail の task-linked 導線、全画面の再設計は境界外である。

## Phase 1: 再現と証拠収集

### Goal Detail

読み取りだけで `http://127.0.0.1:8787/goals/225` を開いた。

- `GET /api/goals/225`: HTTP 200。
- レスポンスの `unattached_decisions` に Decision #638 が存在した。`task_id` は未設定または 0、`kind` は `decision`、`status` は `open`。質問は「保全済みの旧 snapshot migration を削除するか」で、選択肢もレスポンスに含まれていた。
- accessible snapshot には質問、選択肢、回答文入力、回答送信ボタンが存在しなかった。「保全済み」および通常 decision の回答セクションも見つからなかった。
- 同ページの `GET /api/goals/225/diff` も HTTP 200。画面は通常どおり描画され、console error は 0 件だった。
- console には `/api/ws` の WebSocket 接続が確立前に閉じた warning が 1 件だけあった。GET が 200 でデータも描画されているため、今回の欠落の原因とはみなさない。

これは、判断データが API にないのではなく、Goal Detail が taskless 通常 decision を回答可能な UI に渡していないことを示す。

### Task Detail の対象 task と安全な既存状態

`http://127.0.0.1:8787/tasks/1068` も読み取りだけで開いた。

- `GET /api/tasks/1068`: HTTP 200。
- `open_decisions` は空配列。したがってこの task 自体に回答フォームがないのは、このレスポンスからは期待どおりであり、Task 1068 の画面だけでは task-linked 側の回帰とは判定できない。

task-linked 通常 decision が存在する既存の安全な状態として `http://127.0.0.1:8787/tasks/792` を開いた。accessible snapshot には「回答待ちの判断」、通常 decision 2 件、ラベル用 combobox、回答文 textbox、「回答を送信」ボタンが表示された。入力・送信・判断操作は行っていない。

この比較から、現 HEAD の Task Detail は task-linked 通常 decision を表示して回答フォームを構築できる。Goal 225 の taskless decision が Task Detail に現れないことは、task がないためのデータ経路上の差であり、Task Detail の回答フォーム実装が壊れている証拠ではない。

## 境界追跡: UI → API → daemon/store

### UI

- `web/src/components/GoalDetail.tsx:588` は Goal を取得し、`findOpenCompletion`、`findOpenGoalApproval`、`findOpenGoalReview` だけを state に抽出する。
- `web/src/components/GoalDetail.tsx:671` 以降は completion / goal approval / goal review の専用 UI と task table だけを描画し、通常の `unattached_decisions` を描画しない。
- `web/src/components/TaskDetailPage.tsx:74` は `open_decisions` を task ID で絞り、`web/src/components/TaskDetailPage.tsx:255` 以降で件数があれば `DecisionAnswerForm` を描画する。
- `web/src/lib/api.ts:345` の `answerDecision` は既存の `POST /api/decisions/{id}/answer` 導線である。今回、ブラウザから呼び出していない。
- `web/src/components/DecisionTable.tsx` は taskless decision の質問リンクを Goal Detail に向けるため、Goal Detail に通常回答 UI がない場合、その decision の既存回答先が実質的に閉じる。

### API

- `internal/httpapi/server.go:629` は goal 単位の open decisions を取得する。
- `internal/httpapi/server.go:644` 付近で `TaskID == 0` を `unattached_decisions` に分離し、`internal/httpapi/server.go:658` 付近で Goal response に返す。
- `internal/httpapi/server.go:876` 以降は task の open decisions を取得し、`internal/httpapi/server.go:881` 付近で task ID に一致するものだけを `open_decisions` として返す。
- Goal 225 と Task 1068 の実測がいずれも 200 だったこと、および Goal 225 の response に Decision #638 が含まれたことから、HTTP response 生成までのデータ経路は成立している。

### store / daemon

- `internal/store/decision.go:255` の `ListOpenDecisions` は生成 query を通じて open decision を取得する。`internal/store/queries/decision.sql:17` 以降は `goal_id` と `status = 'open'` で検索する。
- `internal/daemon/handler.go:1633` 以降の `decision.ask` は通常 decision を `KindDecision` で作成し、TaskID 0 の taskless と task ID 付きの両方を許容する。MCP の入口は `internal/mcpshim/tools.go:979` 以降である。
- したがって、作成・保存・Goal/Task API の境界で通常 decision が特殊 decision に変換されたり消えたりした証拠はない。

## Phase 2: 正常例と履歴の比較

- `f158d24`（`feat: show unattached goal decisions`）で Goal response の `unattached_decisions` と、Goal Detail の `UnattachedDecisionList` が導入された。履歴上の旧 `GoalDetail.tsx` は completion を除いた unattached decision を state に保持し、`UnattachedDecisionList` に `DecisionAnswerForm` を渡していた。
- `33547a4`（`Rebuild the goal page around one task list and a task modal`）では API 変更なしで、`GoalDetail.tsx` の `UnattachedDecisionList` import、`unattachedDecisions` state、render がまとめて削除された。代わりの Goal mode task table は task 単位の行だけを描画し、TaskID 0 の decision をどの task にも割り当てない。
- `493183d`（`Give a task a page instead of a modal...`）は task title の導線を Task Detail page に移し、Task Detail 側の `open_decisions` と `DecisionAnswerForm` を維持した。現在の `/tasks/792` の実測はこの導線と一致する。
- 現在の Goal Detail の特殊 decision 抽出は `web/src/lib/ui.ts:83` 以降の kind 限定検索である。通常 `kind: "decision"` を拾う回帰テストは現行 Goal Detail テストにない。Task Detail には task-linked open decision のフォーム有無を検証するテストがある。

## Phase 3: 仮説の検証と最小境界

仮説: `33547a4` が UI の taskless 通常 decision 一覧を削除したため、API に残る `unattached_decisions` が Goal Detail から回答不能になった。

最小検証は、(1) Goal 225 の API payload に open な通常 decision があること、(2) 同じ画面の accessible UI に回答要素がないこと、(3) task-linked の正常例 `/tasks/792` では同じ種類のフォームが表示されること、(4) API / daemon / store の既存導線と `f158d24` → `33547a4` の差分を突き合わせることだった。すべて読み取りで実施し、仮説を支持する結果になった。実装変更による検証はしていない。

よって変更するとすれば、既存の Goal Detail の taskless 通常 decision 表示・回答導線だけが最小境界となる。新 API、判断基盤、store query、daemon、migration、snapshot、Goal/Task の画面再設計は不要であり、今回の調査では変更していない。task-linked decision は現在の Task Detail 導線を維持する。

## 変更・検証記録

- 変更した path: `doc/investigations/2026-09-04-normal-decision-detail-regression.md` のみ。
- `internal/store/migrations/0026_goal_review_snapshots.sql`、Goal 231/232/236 の worktree/files、既存実装、spec、plan は変更していない。
- Decision #638 に対する approve / reject / answer は実行していない。
- 検証は本書に対する `git diff --check` のみとする。
