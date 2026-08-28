# Web UI がイベントで勝手に再取得するのをやめる

ゴール 198「webがホットリロードされるのをやめて」の設計。

## 症状

ゴール本文は 1 行しかない。「ホットリロード」が何を指すのかを先に切り分けた。候補は 3 つあった。

| 候補 | 実態 |
|---|---|
| A. WebSocket のイベントで画面が再取得される | **これが症状。** 下記の経路が存在する |
| B. Astro dev サーバの HMR | `pnpm dev` は誰も常用していない。daemon は焼き込んだ `web/dist` を配る |
| C. daemon が配る静的ファイルの差し替えで再読み込み | **存在しない。** そんな機構は無い |

B と C を否定した根拠は、`internal` / `cmd` / `script` / `doc` に `hmr`・`fsnotify`・
`hot.reload`・`astro dev` の出現が 1 件も無いこと、`web/src` に `location.reload` が
1 件も無いこと。**ページ全体が再読み込みされる経路は、そもそもどこにも無かった。**

A の経路はこうだった。

1. `web/src/lib/api.ts` の `subscribeToDecisionEvents` が `/api/ws` を購読する
2. イベントが来ると 100ms のデバウンス後に `onEvent` が呼ばれる
3. 購読側 3 コンポーネント（`Dashboard` / `GoalDetail` / `TaskDetailPage`）の
   `handleDecisionEvent` が `load()` を呼ぶ
4. `load()` は `setState({ kind: "loading" })` を**先に**行う

4 が症状の正体である。`loading` 状態は画面の内容を消してローディング表示に差し替えるので、
イベントが来るたびに**読んでいた内容が消えて描き直され、スクロール位置も失われる。**
エージェントが多数走っている間はイベントが絶えないので、これは実質「ページが勝手に
リロードされ続ける」ように見える。ゴール 187 が足したゴール全体の差分表示のような
長いページで特に効く。

## 決定

### 1. WebSocket 購読は残す

ゴール 163 が SSE から WebSocket へ移した目的は「SSE の接続上限をブラウザに波及させない」
ことであって、自動更新そのものではない。**購読を消すと 163 を打ち消すので残す。**
変えるのはイベントを受けた後の挙動だけである。

### 2. イベントを受けても再取得しない。バナーを出すだけにする

`handleDecisionEvent` は無条件に `setUpdatePending(true)` だけを行い、`load()` を呼ばない。
再取得は人間が「最新を取得」を押したときだけ起きる。

**新しい UI も新しい i18n キーも要らなかった。**`updatePending` のバナー
（`state.updateAvailable`）と「最新を取得」ボタン（`state.fetchLatest`）は 3 コンポーネント
すべてに既に実装されていた。入力中だけ自動再取得を抑止する経路として置かれていたものを、
常時の経路に昇格させただけである。

### 3. dirty 追跡は撤去する

dirty 追跡は「自動再取得を入力中だけ抑止する」ためだけに存在していた。決定 2 で自動再取得が
無くなると write-only になる。**残すと、まだ機能しているように読める。**

撤去したのは次のとおり。いずれも自動再取得の抑止以外の利用者が無いことを確認した。

- `Dashboard` の `goalCreateDirtyRef` / `handleGoalCreateDirtyChange`
- `GoalCreateForm` の `onDirtyChange` prop と、それを呼ぶ 2 つの `useEffect`
- `GoalDetail` の `completionReasonRef` / `goalApprovalReasonRef`
- `TaskDetailPage` の `dirtyDecisionIDs` / `handleInputStateChange`
- `DecisionAnswerForm` の `onInputStateChange` prop と、それを呼ぶ `useEffect`

`GoalDetail` の `completionReason` / `goalApprovalReason` の `useState` は残した。子へ渡して
描画に使っており、手動再取得の後にフォームを空へ戻すために `load()` 内のリセットも必要である。

## 残る自動更新

`goal.completion.fetchLatest` と `form.answer.fetchLatest` の 2 つは残る。これらは
WebSocket イベントではなく、**回答の送信が 409 で衝突したとき**に出る別の経路であり、
この設計の対象外である。
