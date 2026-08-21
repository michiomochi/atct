# 画面を 1 度は本物のブラウザで見る

日付: 2026-08-20
ゴール: 画面をブラウザで描画して確かめる手段が無い（7b7c7649）

## 解く問題

**同じ日に 2 回、テストが全部通ったまま画面だけが壊れた。**

| 版 | 症状 | 通っていた検査 |
|---|---|---|
| v0.21.0 | ゴール詳細が白紙（`goal.tasks` が null） | 型検査・単体テスト・Go の全テスト |
| v0.22.0 | 順序の列が「1 1 1 1 1 1 2 3 4 5 6」 | 同上 |

さらに同日、**タスク詳細ページが実 URL でダッシュボードの HTML を返していた。**
これも Go の全テストと web の 109 テストが通ったままだった。

**共通しているのは、描画を 1 度も見ていないことである。** 3 件目は executor が
実際に URL を叩いたので見つかった。

## 決定（2026-08-20 の決定 89570e0c）

**playwright を web の devDependency に入れ、同梱の Chromium を headless で使う。
ただし `pnpm test` には組み込まず、script から手で走らせる。**

### Chromium を使う。Google Chrome は使わない

`product-ui` スキルに「`channel: 'chrome'` は Google Chrome の起動に実測で失敗する」と
書いてある。**同日それを踏んだ。** 素で起動した playwright MCP は Chrome の起動で 180 秒
タイムアウトし、キャッシュの `chrome-headless-shell` を `--dump-dom` で直接叩く形も
6 分 40 秒で打ち切りになった。

Chromium はキャッシュ（`~/Library/Caches/ms-playwright/chromium-1234`）に既にある。
**ブラウザの新規ダウンロードは起きない。**

### テストには組み込まない

描画の確認は遅く、落ちたときに原因が画面かブラウザか分かりにくい。
**まず手で走らせて価値を確かめてから、組み込むかを別の決定にする。**

代償を書いておく。**忘れると走らない。** 今回の 3 件はどれも「走らせなかった」ので
起きた。手段があることと使うことは別である。

### 検証は実データのコピーで行う

実 HOME の daemon は止めない。`VACUUM INTO` のコピーと**短いパスの一時 HOME**を使う。
実測: パスが 100 バイトを超えると daemon が `bind: invalid argument` で落ちる
（macOS の unix ソケットの 104 バイト制限）。

## スクリプトは残さない（2026-08-20 の訂正）

**当初 `script/render-check.sh` を作らせたが、やめた。**

理由は 3 つある。

- **書式は既に `playwright` スキルにある。** 最小の実行例と「必ず集める 3 つ」
  （console と pageerror / requestfailed / response の Content-Type）が書いてある。
  **私はそれを読まずに 146 行のシェルを書かせた**
- **テストに組み込まない決定なので、走らせるかは人間の記憶に依存する。**
  忘れれば死んだファイルになる。同じ日に参照 0 のコンポーネント 3 つを削除している
- **効いたのは「描画したこと」でスクリプトではない。** 数コマンドで測って不具合を
  見つけた。**恒久的な守りはテストである**（下の sentinel の否定側）

残す知識はここに書く。

| 知識 | 内容 |
|---|---|
| playwright の入手 | **依存に足さない。** グローバルに入っている（`npm root -g`）。`NODE_PATH="$(npm root -g)" node <script>` で解決する |
| MCP | **設定済みのものは使える。**下の訂正を読むこと。`npx -y @playwright/mcp@latest` を**素で**起動すると既定が実物の Google Chrome かつ headed で、初期化が 180 秒でタイムアウトする |
| 使うブラウザ | playwright 同梱の Chromium。キャッシュにあり、起動は 1 秒（実測 151.0.7922.34） |
| 一時 HOME | **短いパスにする**（`/private/tmp/atct-vh` など）。100 バイトを超えると daemon が `bind: invalid argument` で落ちる（macOS の unix ソケット 104 バイト制限） |

## 最初の描画で見つかった不具合

**この手段を作った最初の実行で、タスク詳細ページが空だと分かった。**

```
/tasks/<本物の ID>  ->  h1 が空、表 0 行
画面の文字: ATCT Dashboard English 日本語 task not found: _ Retry
404: /api/tasks/_
console: React error #418（hydration の不一致）
```

`web/src/pages/tasks/[id].astro` は `getStaticPaths` で `id: "_"` を返すので、
**ビルド時の props は常に `_`** である。ゴール詳細は `resolveGoalID`
（`web/src/lib/ui.ts:96`）で URL から復元しているが、**タスクページには同じ仕組みが
無かった。**

すり抜けた理由を残す。**`TaskDetailPage.test.tsx` は `id="task-1"` を直接渡している。**
自分で作った props でしか検査していないので、ビルドが埋め込む値とのずれを
原理的に見つけられない。今日 3 回目の同じ形である（`goal.tasks` が null、
`decision_history.task_id` が無い、これ）。

**恒久的な守りは sentinel の否定側テストであり、手で走らせるスクリプトではない。**

## 測った結果（2026-08-20）

| 対象 | 捕まえられたか |
|---|---|
| v0.22.0 の順序表示 | **捕まえられた。** 描画された文字に `ORDER ... 1 ... 1 ... 1`、表の 1 列目が `["1","1","1"]` |
| v0.21.0 の白画面 | **再現しなかったため未検証** |
| タスクページの sentinel（今日の新規） | **捕まえた。** この手段の最初の 1 回で見つけた |

**白画面が再現しない理由は、直っているからである。** クライアントの `?? []` を外しても、
サーバが `tasks` を `[]` で返す（`server.go:456` の `nonNilTaskViews`）ので壊れない。
**「この手段では捕まえられない」ではなく「その編集ではもう壊れない」**であり、
サーバとクライアントの両方で守っているのが効いている。

順序表示の再現には、コピー側の DB で一意索引を落として `sort_order` を重複させた。

```sql
DROP INDEX idx_tasks_goal_sort_order;
UPDATE tasks SET sort_order = 1 WHERE goal_id = '...';
```

**実データを壊さずに、実データの形で測れる。** コピーだからできる。

いずれの測定でも `console` / `pageerror` / `requestfailed` は空で、`.js` は
`text/javascript`、`.css` は `text/css` だった。**`_` 始まりのディレクトリでも
Content-Type の取り違えは起きていない。**

## 検証

- タスク詳細ページを幅 1280px で描画し、**回答履歴の行数**とページの縦の高さが取れること
- **v0.21.0 の白画面を、この手段で捕まえられたかを測る**（`goal.tasks` を null に戻して
  試す。捕まえられない手段を入れても意味がない）
- **v0.22.0 の順序表示を、この手段で捕まえられたかを測る**（描画された文字列を読む。
  列見出しだけ見て中身を読まなかったのが当時の見落としである）

## 訂正: MCP が駄目なのではなく、素で起動したのが駄目だった（2026-08-21）

人間から「なんで claude 上の playwright MCP じゃだめなの？」と差し戻され、実際に叩いて測った。

**動く。** Claude Code に登録済みの `playwright` MCP でダッシュボードを開いたところ、
待ちなしで描画され、`Page Title: Dashboard | ATCT`、アクセシビリティのスナップショット
21 行、console のエラーと警告は 0 件だった。

差が出たのは**起動時の引数**である。`~/.claude.json` の登録はこうなっている。

    npx -y @playwright/mcp@latest --headless --isolated --output-dir ~/.cache/playwright-mcp

上の表に「MCP は使わない」と書いたとき、私が測ったのは `npx -y @playwright/mcp@latest` を
**引数なしで**起動したものだった。既定は実物の Google Chrome かつ headed なので 180 秒で
落ちる。**登録済みの MCP はその既定を踏んでいない。**「MCP が駄目」と一般化したのが誤りである。

### どちらを使うか

| 場面 | 手段 |
|---|---|
| Claude Code のセッションから 1 画面見る | **MCP を使う。**登録済みで、起動コストが無い |
| executor（Codex）から見る | **MCP を使う。**下の訂正を読むこと |
| 素の `npx @playwright/mcp` を自分で起動する | **やらない。**引数なしは 180 秒で落ちる |

### MCP を使うときの注意（実測）

`browser_navigate` が返した直後のスナップショットは、島がまだ
`Loading Waiting for answers` / `Loading Projects` の状態だった。**ATCT の画面はデータを
fetch してから描くので、遷移直後のスナップショットは中身を見ていない。**
`browser_wait_for` で本文の文字を待ってからスナップショットを取ること。

## 訂正: Codex からも MCP は使える（2026-08-21）

上の表に「Codex からは使えない」と書いたが、**人間が `~/.codex/config.toml` に
`[mcp_servers.playwright]` を足したので使える。**設定は Claude Code 側と同じ形である。

    npx -y @playwright/mcp@latest --headless --isolated --output-dir ~/.cache/playwright-mcp
    startup_timeout_sec = 120

executor に実際に叩かせた（2026-08-21）。

| 測ったこと | 結果 |
|---|---|
| 使えたツール | `browser_navigate` / `browser_wait_for` / `browser_evaluate` / `browser_console_messages` |
| `http://127.0.0.1:8787/` の遷移 | **約 6.1 秒で成功。**180 秒のタイムアウトには当たらない |
| タイトル | `Dashboard | ATCT` |
| console のエラー | 0 件 |
| 確認完了まで | 約 71 秒 |

**「MCP が使えるのは Claude Code だけ」という一般化が誤りだった。**設定した側から使える。

### 待つ文字はロケールに合わせる（実測）

executor は `Projects`（英語）で待って **35 秒でタイムアウト**し、`プロジェクト` に
変えて成功した。**画面のロケールは origin ごとに localStorage で保持される。**
待ち文字を決める前に、その origin がどちらで描かれているかを確かめること。
