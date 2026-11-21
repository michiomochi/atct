# `@git-diff-view/react` の導入と API

ゴール 187。**差分の描画を自前で書かないために使う。**調べたのは `0.1.7`。
根拠は npm から取った実物の `index.d.ts`（`npm pack @git-diff-view/react` で確認）。

## 1. 入れるもの

    cd web && pnpm add @git-diff-view/react

**`@git-diff-view/core` を別に足す必要は無い。**`react` 側の `dependencies` に
`@git-diff-view/core@^0.1.7` が入っていて、`export * from "@git-diff-view/core"` で再輸出される。

`peerDependencies` は `react` / `react-dom` の `^16.8 || ^17 || ^18 || ^19`。
このプロジェクトは React 19 なので通る。

**依存に `highlight.js` と `lowlight` が付いてくる。**これが後述の遅延読み込みの理由である。

## 2. CSS

    import "@git-diff-view/react/styles/diff-view.css";

`exports` に `"./styles/*": "./dist/css/*"` があり、実体は `diff-view.css` と
`diff-view-pure.css` の 2 つ。**色付きが要るなら `diff-view.css` のほう。**

CSS の import は JS を実行しないので、**静的 import のままでビルド時に壊れない。**

## 3. 生の diff 文字列から描く

**`data.hunks` は `string[]` で、unified diff の文字列をそのまま入れる。**
`oldFile` / `newFile` は省略できる。型は `index.d.ts:1127` の `DiffViewProps<T>`。

```tsx
import { DiffView, DiffModeEnum } from "@git-diff-view/react";
import "@git-diff-view/react/styles/diff-view.css";

// patch は GET /api/goals/{id}/diff?path=... が返す生の git diff 出力
<DiffView
  data={{ hunks: [patch] }}
  diffViewMode={DiffModeEnum.Unified}
  diffViewWrap
  diffViewHighlight={false}
/>
```

**`generateDiffFile` のようなヘルパは要らない。**`DiffFile.createInstance({hunks})` も
公開されているが（`index.d.ts:82`）、`data` を渡すだけで内部が同じことをする。

## 4. 表示モード

`DiffModeEnum`（`index.d.ts:1002`）は `SplitGitHub=1` / `SplitGitLab=2` / `Split=3` / `Unified=4`。
`diffViewMode` で渡す。他に `diffViewWrap` / `diffViewTheme:"light"|"dark"` /
`diffViewFontSize` / `diffViewHighlight` がある。

## 5. Astro（`output: "static"`）での制約

**`astro build` はビルド時に Node で `client:load` の島を 1 度描く。**
`web/src/pages/goals/[id].astro` は `<GoalDetail id={id} client:load />` である。

パッケージ本体は先頭に `'use client'` を持ち、`window` 参照は
`typeof window !== "undefined"` で守られている。`document.createElement` と
`window.getSelection` はハンドラの中にある。**そのまま静的 import してもビルドは通る見込み。**

**それでも遅延読み込みにする。**理由はビルドの安全ではなく**初期バンドルの大きさ**である——
`highlight.js` と `lowlight` が付いてくるのに、hunk はファイルを選んだときにしか要らない。

```tsx
const DiffView = lazy(() =>
  import("@git-diff-view/react").then((m) => ({ default: m.DiffView })),
);
// 使う側は <Suspense fallback={<AreaLoading .../>}> で包む
```

## 6. vitest（jsdom）

`web/vitest.config.ts` は `environment: "jsdom"`。
**テストでは `@git-diff-view/react` を `vi.mock` で差し替える。**

```tsx
vi.mock("@git-diff-view/react", () => ({
  DiffView: ({ data }: { data: { hunks: string[] } }) => (
    <pre data-testid="diff-view">{data.hunks.join("")}</pre>
  ),
  DiffModeEnum: { Unified: 4 },
}));
```

**hunk の描画そのものをテストしない。**それはライブラリの仕事である。
テストで見るのは「一覧が出るか」「ファイルを選んだときだけ取りに行くか」の 2 つ。

## 実装で読むファイルと関数

    web/src/components/GoalDetail.tsx          `GoalDetail` / `taskCommits`（504 行）/ 末尾のコミット節（640-664 行）
    web/src/components/TaskCommitList.tsx      `loadCommitDiff` — 選んだときだけ取りに行く形の前例
    web/src/lib/api.ts                         `fetchTaskCommitDiff`（299 行）/ `requestJson`（221 行）
    web/src/components/StateMessage.tsx        `AreaLoading` / `ErrorState`
    web/src/i18n/ja.ts, web/src/i18n/en.ts     文言の追加先
