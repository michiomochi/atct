# worktree `web/node_modules` sandbox 調査

測定日: 2026-08-28 JST  
sandbox: `workspace-write`  
worktree: `/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191`

## P0 前提の確認

### `ls -l web/node_modules`

exit code: `0`

```text
lrwxr-xr-x@ 1 masayoshi.michikawa@kanmu.co.jp  staff  87 Aug 28 02:14 web/node_modules -> /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules
exit=0
```

### `pnpm --version`

exit code: `0`

```text
10.33.0
exit=0
```

### `node --version`

exit code: `0`

```text
v22.17.0
exit=0
```

## P1 symlink 越しの読み取り

### `ls web/node_modules | head -5`

exit code: `0`

```text
@astrojs
@cloudflare
@git-diff-view
@tailwindcss
@testing-library
exit=0
```

### `cat web/node_modules/astro/package.json | head -3`

exit code: `0`

```text
{
  "name": "astro",
  "version": "5.18.2",
exit=0
```

### `node -e "console.log(require.resolve('astro', {paths: ['./web']}))"`

exit code: `0`

```text
/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/astro@5.18.2_jiti@2.7.0_lightningcss@1.32.0_rollup@4.62.4_typescript@5.9.3_yaml@2.9.0/node_modules/astro/dist/index.js
exit=0
```

判定: symlink 越しの読み取りは `yes`。3 コマンドとも exit 0。

## P2 symlink 越しの書き込み

### `touch web/node_modules/.atct-191-probe`

exit code: `1`

```text
touch: web/node_modules/.atct-191-probe: Operation not permitted
exit=1
```

### `mkdir -p web/node_modules/.atct-191-probe-dir`

exit code: `1`

```text
mkdir: web/node_modules/.atct-191-probe-dir: Operation not permitted
exit=1
```

判定: symlink 越しの書き込みは `no`。2 つの probe は作成に失敗したため残していない。
symlink 自体は外していない。

## P3 worktree 内への書き込み（対照）

### `touch web/.atct-191-probe-inside`

exit code: `0`

```text
exit=0
```

### `rm web/.atct-191-probe-inside`

exit code: `0`

```text
exit=0
```

worktree 内の probe 作成は成功し、指定された 1 ファイルだけ削除した。

## P4 `pnpm test`

### `cd web && pnpm test`

exit code: `1`

```text
> atct-web@ test /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web
> vitest run

failed to load config from /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web/vitest.config.ts

⎯⎯⎯⎯⎯⎯⎯ Startup Error ⎯⎯⎯⎯⎯⎯⎯⎯
Error: EPERM: operation not permitted, open '/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web/node_modules/.vite-temp/vitest.config.ts.timestamp-1787851460539-7a2e647175b9c.mjs'
    at async open (node:internal/fs/promises:639:25)
    at async Object.writeFile (node:internal/fs/promises:1213:14)
    at async loadConfigFromBundledFile (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vite@7.3.6_jiti@2.7.0_lightningcss@1.32.0_yaml@2.9.0/node_modules/vite/dist/node/chunks/config.js:35994:3)
    at async bundleAndLoadConfigFile (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vite@7.3.6_jiti@2.7.0_lightningcss@1.32.0_yaml@2.9.0/node_modules/vite/dist/node/chunks/config.js:35884:17)
    at async loadConfigFromFile (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vite@7.3.6_jiti@2.7.0_lightningcss@1.32.0_yaml@2.9.0/node_modules/vite/dist/node/chunks/config.js:35851:42)
    at async resolveConfig (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vite@7.3.6_jiti@2.7.0_lightningcss@1.32.0_yaml@2.9.0/node_modules/vite/dist/node/chunks/config.js:35500:22)
    at async _createServer (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vite@7.3.6_jiti@2.7.0_lightningcss@1.32.0_yaml@2.9.0/node_modules/vite/dist/node/chunks/config.js:25441:67)
    at async createViteServer (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vitest@3.2.7_@types+debug@4.1.13_jiti@2.7.0_jsdom@30.0.1_lightningcss@1.32.0_yaml@2.9.0/node_modules/vitest/dist/chunks/cli-api.DVe0nWUx.js:6921:17)
    at async createVitest (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vitest@3.2.7_@types+debug@4.1.13_jiti@2.7.0_jsdom@30.0.1_lightningcss@1.32.0_yaml@2.9.0/node_modules/vitest/dist/chunks/cli-api.DVe0nWUx.js:10212:17)
    at async prepareVitest (file:///Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/web/node_modules/.pnpm/vitest@3.2.7_@types+debug@4.1.13_jiti@2.7.0_jsdom@30.0.1_lightningcss@1.32.0_yaml@2.9.0/node_modules/vitest/dist/chunks/cli-api.DVe0nWUx.js:10551:14) {
  errno: -1,
  code: 'EPERM',
  syscall: 'open',
  path: '/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web/node_modules/.vite-temp/vitest.config.ts.timestamp-1787851460539-7a2e647175b9c.mjs'
}



 ELIFECYCLE  Test failed. See above for more details.
exit=1
```

実出力で拒否されたパス: `web/node_modules/.vite-temp/vitest.config.ts.timestamp-1787851460539-7a2e647175b9c.mjs`（`open`）。

## P5 `pnpm build`

### `cd web && pnpm build`

exit code: `1`

```text
> atct-web@ build /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web
> astro check && astro build

02:24:44 [vite] Re-optimizing dependencies because vite config has changed
EPERM: operation not permitted, unlink '/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web/node_modules/.vite/deps/@astrojs_react_client__js.js'
  Stack trace:

 ELIFECYCLE  Command failed with exit code 1.
exit=1
```

実出力で拒否されたパス: `web/node_modules/.vite/deps/@astrojs_react_client__js.js`（`unlink`）。この出力に示された拒否パスはこの 1 件で、ほかの拒否パスは確認できなかった。

## 結論

- P1 symlink 越しの read: `yes`（3/3、exit 0）。
- P2 symlink 越しの write: `no`（touch・mkdir とも exit 1、`Operation not permitted`）。残した probe はない。
- P3 worktree 内 write: 成功（exit 0）。probe は削除済み。
- P4 `pnpm test`: exit 1。`web/node_modules/.vite-temp/...` への `open` が EPERM。
- P5 `pnpm build`: exit 1。`web/node_modules/.vite/deps/@astrojs_react_client__js.js` への `unlink` が EPERM。

この worktree では、symlink 越しの依存ファイルは読めるが、依存ディレクトリ内の Vite probe/cache を書き換えられないため、test/build は sandbox の書き込み制限で失敗した。

## 2026-08-28 切り離し後の追測定（タスク 742）

前回の symlink 状態から変更され、`web/node_modules` は worktree 内の通常ディレクトリになっている状態で測定した。`attach` は実行していない。

### Q0 前提

#### `script/worktree-node-modules.sh status`

exit code: `0`

```text
detached: /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191
exit=0
```

#### `ls -ld web/node_modules`

exit code: `0`

```text
drwxr-xr-x@ 21 masayoshi.michikawa@kanmu.co.jp  staff  672 Aug 28 02:41 web/node_modules
exit=0
```

### Q1 `pnpm test`

#### `cd web && pnpm test`

exit code: `0`

```text
> atct-web@ test /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web
> vitest run


 RUN  v3.2.7 /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web

 ✓ src/lib/contrast.test.ts (8 tests) 4ms
 ✓ src/lib/api.test.ts (12 tests) 11ms
 ✓ src/lib/ui.test.ts (91 tests) 40ms
 ✓ src/i18n/i18n.test.ts (18 tests) 24ms
 ✓ src/lib/events.test.ts (2 tests) 39ms
 ✓ src/lib/colors.test.ts (4 tests) 5ms
 ✓ src/components/ProposedGoalTable.test.tsx (1 test) 197ms
 ✓ src/components/Dashboard.test.tsx (4 tests) 257ms
 ✓ src/components/TaskTable.test.tsx (5 tests) 208ms
stderr | src/components/DecisionTable.test.tsx
Not implemented: navigation to another Document

 ✓ src/components/DecisionTable.test.tsx (8 tests) 440ms
   ✓ DecisionTable > keeps the full question and options out of the collapsed details  311ms
 ✓ src/components/GoalTable.test.tsx (7 tests) 466ms
 ✓ src/components/GoalCreateForm.test.tsx (2 tests) 318ms
 ✓ src/components/GoalDiff.test.tsx (8 tests) 671ms
   ✓ GoalDiff > fetches and renders a patch when a file is opened  325ms
 ✓ src/components/TaskDetailPage.test.tsx (25 tests) 810ms
 ✓ src/components/GoalDetail.test.tsx (36 tests) 1082ms

 Test Files  15 passed (15)
      Tests  231 passed (231)
   Start at  02:42:54
   Duration  42.31s (transform 3.22s, setup 0ms, collect 260.73s, tests 4.57s, environment 175.11s, prepare 3.46s)

exit=0
```

判定: 15 test files、231 tests がすべて pass。失敗テストはない。

### Q2 `pnpm build`

#### `cd web && pnpm build`

exit code: `0`

```text
> atct-web@ build /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web
> astro check && astro build

02:44:02 [content] Syncing content
02:44:02 [content] Synced content
02:44:02 [types] Generated 2.05s
02:44:02 [check] Getting diagnostics for Astro files in /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web...
src/components/Dashboard.test.tsx:5:48 - warning ts(6133): 't' is declared but its value is never read.
5 const { fetchInbox, subscribeToDecisionEvents, t, i18nMock, goalCreateFormMock } = vi.hoisted(() => {
                                               ~
src/components/DecisionAnswerForm.tsx:40:38 - warning ts(6385): 'FormEvent' is deprecated.
40   async function handleSubmit(event: FormEvent<HTMLFormElement>) {
                                       ~~~~~~~~~~~~~~~~~~~~~~~~~~
src/components/DecisionAnswerForm.tsx:2:31 - warning ts(6385): 'FormEvent' is deprecated.
2 import { useEffect, useState, type FormEvent } from "react";
                              ~~~~~~~~~~~~~~
src/components/GoalCreateForm.tsx:66:38 - warning ts(6385): 'FormEvent' is deprecated.
66   const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
                                       ~~~~~~~~~~~~~~~~~~~~~~~~~~
src/components/GoalCreateForm.tsx:1:44 - warning ts(6385): 'FormEvent' is deprecated.
1 import { useCallback, useEffect, useState, type FormEvent } from "react";
                                           ~~~~~~~~~~~~~~
src/components/GoalDetail.test.tsx:172:10 - warning ts(6133): 'emptyInbox' is declared but its value is never read.
172 function emptyInbox(): InboxResponse {
             ~~~~~~~~~~
src/components/GoalDetail.test.tsx:15:10 - warning ts(6133): 'Dashboard' is declared but its value is never read.
15 import { Dashboard } from "./Dashboard";
         ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
src/components/GoalDetail.test.tsx:9:3 - warning ts(6133): 'fetchInbox' is declared but its value is never read.
9   fetchInbox,
  ~~~~~~~~~~
src/components/GoalDetail.tsx:375:38 - warning ts(6385): 'FormEvent' is deprecated.
375   async function handleSubmit(event: FormEvent<HTMLFormElement>) {
                                         ~~~~~~~~~~~~~~~~~~~~~~~~~~
src/components/GoalDetail.tsx:291:38 - warning ts(6385): 'FormEvent' is deprecated.
291   async function handleSubmit(event: FormEvent<HTMLFormElement>) {
                                         ~~~~~~~~~~~~~~~~~~~~~~~~~~
src/components/GoalDetail.tsx:233:32 - warning ts(6385): 'FormEvent' is deprecated.
233   function handleSubmit(event: FormEvent<HTMLFormElement>) {
                                   ~~~~~~~~~~~~~~~~~~~~~~~~~~
src/components/GoalDetail.tsx:129:32 - warning ts(6385): 'FormEvent' is deprecated.
129   function handleSubmit(event: FormEvent<HTMLFormElement>) {
                                   ~~~~~~~~~~~~~~~~~~~~~~~~~~
src/components/GoalDetail.tsx:3:52 - warning ts(6385): 'FormEvent' is deprecated.
3 import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
                                                   ~~~~~~~~~~~~~~

Result (45 files): 
- 0 errors
- 0 warnings
- 13 hints

02:45:38 [content] Syncing content
02:45:38 [content] Synced content
02:45:38 [types] Generated 52ms
02:45:38 [build] output: "static"
02:45:38 [build] mode: "static"
02:45:38 [build] directory: /Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/191/web/dist/
02:45:38 [build] Collecting build info...
02:45:38 [build] ✓ Completed in 88ms.
02:45:38 [build] Building static entrypoints...
02:45:40 [vite] ✓ built in 2.30s
02:45:40 [build] ✓ Completed in 2.34s.

 building client (vite) 
02:45:40 [vite] transforming...
02:46:03 [vite] ✓ 4913 modules transformed.
02:46:03 [vite] rendering chunks...
02:46:04 [vite] computing gzip size...
02:46:04 [vite] dist/_astro/LocaleSwitch.BSWWEFBm.js                 1.02 kB │ gzip:   0.57 kB
02:46:04 [vite] dist/_astro/DecisionAnswerForm.9x_VLBah.js           6.63 kB │ gzip:   2.13 kB
02:46:04 [vite] dist/_astro/TaskDetailPage.CuUnzTnK.js              12.00 kB │ gzip:   3.22 kB
02:46:04 [vite] dist/_astro/index.Bl8S_zse.js                       12.23 kB │ gzip:   4.40 kB
02:46:04 [vite] dist/_astro/Dashboard.CExAAiRG.js                   14.64 kB │ gzip:   3.84 kB
02:46:04 [vite] dist/_astro/GoalDetail.C1pzuTcg.js                  27.03 kB │ gzip:   6.16 kB
02:46:04 [vite] dist/_astro/dialog-b4r3dv8uvgl2pqem.Bpl3dQC5.js     27.61 kB │ gzip:  10.06 kB
02:46:04 [vite] dist/_astro/StateMessage.BM5YZA1Y.js                46.17 kB │ gzip:  15.24 kB
02:46:04 [vite] dist/_astro/client.B4KGge57.js                     182.70 kB │ gzip:  57.59 kB
02:46:04 [vite] dist/_astro/index.x6EhKAA_.js                      204.49 kB │ gzip:  68.05 kB
02:46:04 [vite] dist/_astro/index.CmvBflpU.js                    1,111.58 kB │ gzip: 340.53 kB
02:46:04 [WARN] [vite] 
(!) Some chunks are larger than 500 kB after minification. Consider:
- Using dynamic import() to code-split the application
- Use build.rollupOptions.output.manualChunks to improve chunking: https://rollupjs.org/configuration-options/#output-manualchunks
- Adjust chunk size limit for this warning via build.chunkSizeWarningLimit.
02:46:04 [vite] ✓ built in 23.35s

 generating static routes 
02:46:13 ▶ src/pages/goals/[id].astro
02:46:13   └─ /goals/_/index.html (+48ms) 
02:46:14 ▶ src/pages/index.astro
02:46:14   └─ /index.html (+28ms) 
02:46:14 ▶ src/pages/tasks/[id].astro
02:46:14   └─ /tasks/_/index.html (+28ms) 
02:46:14 ✓ Completed in 9.91s.

02:46:14 [build] 3 page(s) built in 35.71s
02:46:14 [build] Complete!
exit=0
```

判定: build は成功。実出力に拒否されたパスはない。`web/dist` は build により更新された。

### Q3 `pnpm install --frozen-lockfile`

#### `cd web && pnpm install --frozen-lockfile`

exit code: `1`

```text
 ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY  Aborted removal of modules directory due to no TTY

If you are running pnpm in CI, set the CI environment variable to "true", or set "confirmModulesPurge" to "false".
exit=1
```

実出力に拒否されたパスはない。pnpm は modules directory の削除確認を no TTY のため中断し、install 本体は実行できなかった。

#### `git status --porcelain -- web/`

exit code: `0`

```text
exit=0
```

出力は空で、`web/package.json` と `web/pnpm-lock.yaml` の変更は確認されなかった。

## 追測定の結論

- 切り離し後の `web/node_modules` は通常ディレクトリで、`pnpm test` は 15 files / 231 tests、exit 0。
- `pnpm build` は exit 0。Astro check は 0 errors / 0 warnings / 13 hints、3 ページを生成した。
- `pnpm install --frozen-lockfile` は exit 1。sandbox の拒否パスではなく no TTY で中断し、install 本体は実行できなかった。
- `git status --porcelain -- web/` は空で、package.json と pnpm-lock.yaml の変更はない。
