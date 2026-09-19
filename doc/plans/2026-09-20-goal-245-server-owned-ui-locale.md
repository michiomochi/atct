# Server-owned UI locale Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store the UI locale in the daemon and make the server-selected static document, Astro shell, and React first render agree.

**Architecture:** Add one constrained singleton row and two small HTTP endpoints. Build the static UI into independent `en` and `ja` trees; the daemon serves literal files directly and chooses the persisted tree only for HTML fallback requests. The client uses the build locale at module initialization and persists a switch through the server before reloading.

**Tech Stack:** Go, SQLite migrations, sqlc, `net/http`, Astro 5, React 19, i18next, Vitest, and the existing embedded `embed.FS` server.

## Global Constraints

- The current migration maximum is `0043_runtime_heartbeat_lease.sql`; add only `0044_ui_settings.sql`.
- Supported locales are exactly `en` and `ja`; invalid or missing stored values read as `en`.
- `localStorage`, `navigator.language`, load-time locale recovery, DOM patching, and post-hydration `changeLanguage` are forbidden.
- API, MCP, WebSocket, SSE, reconciliation, and literal static asset routes keep their current precedence and meaning.
- Do not edit existing migrations, rebase, reset, amend, publish, or touch Goals 273, 282, or another worktree.
- Do not delegate or edit implementation source until this plan handoff is accepted.

---

## Task 1: Persist and expose the locale

**Files:**

- Create: `internal/store/migrations/0044_ui_settings.sql`
- Create: `internal/store/queries/ui_settings.sql`
- Create: `internal/store/ui_settings.go`
- Create: `internal/store/ui_settings_test.go`
- Modify: `schema.sql`
- Regenerate: `internal/store/sqlcgen/`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/server_test.go`

**Interfaces:**

- `store.GetUILocale(ctx) (string, error)` returns `"en"` or `"ja"` and
  normalizes missing/unsupported stored values to `"en"`.
- `store.SetUILocale(ctx, locale string) error` validates before writing and
  rejects every value other than `"en"` and `"ja"`.
- HTTP uses `GET /api/ui-settings` and
  `PUT /api/ui-settings/locale` with `{ "locale": "en" | "ja" }`.

- [ ] **Step 1: Write the failing store tests.** Cover a fresh database
  returning `en`, a successful `en -> ja` write/readback, an invalid write
  returning an error without changing `ja`, and a deleted singleton row
  returning `en`.

  ```go
  func TestGetUILocaleDefaultsToEnglish(t *testing.T) {
      ctx := context.Background()
      got, err := newTestStore(t).GetUILocale(ctx)
      if err != nil {
          t.Fatal(err)
      }
      if got != "en" {
          t.Fatalf("locale = %q, want en", got)
      }
  }

  func TestSetUILocaleRejectsUnsupportedWithoutChangingValue(t *testing.T) {
      ctx := context.Background()
      s := newTestStore(t)
      if err := s.SetUILocale(ctx, "ja"); err != nil {
          t.Fatal(err)
      }
      if err := s.SetUILocale(ctx, "fr"); err == nil {
          t.Fatal("SetUILocale accepted unsupported locale")
      }
      got, err := s.GetUILocale(ctx)
      if err != nil {
          t.Fatal(err)
      }
      if got != "ja" {
          t.Fatalf("locale after rejected write = %q, want ja", got)
      }
  }
  ```

- [ ] **Step 2: Add the schema and sqlc queries.** Create `ui_settings` with
  `id INTEGER PRIMARY KEY CHECK (id = 1)`, a non-null locale, and a
  `CHECK (locale IN ('en', 'ja'))`; seed row 1 with `en` in the migration and
  add the same table shape to `schema.sql`. Add generated queries for reading
  row 1 and replacing its locale, then run `go tool sqlc generate`.

- [ ] **Step 3: Implement the smallest store API.** Validate the input before
  opening a write transaction. Treat `sql.ErrNoRows` and an unsupported value
  returned from storage as the English fallback. Return the write validation
  error without executing an update.

- [ ] **Step 4: Add the HTTP routes and tests.** Register the exact paths in
  `ServeHTTP`, use the existing JSON/error helpers, and test default GET,
  successful PUT/readback, malformed JSON, missing locale, unsupported locale,
  and unchanged state after each rejected PUT. Keep method errors consistent
  with the existing API.

- [ ] **Step 5: Verify and commit.** Run:

  ```sh
  go test ./internal/store -run 'Test(UISettings|SchemaParity|EmptyDatabaseAppliesBaselineMigration)'
  go test ./internal/httpapi -run 'TestHTTPUISettings'
  ./script/schema-check.sh
  git add internal/store/migrations/0044_ui_settings.sql internal/store/queries/ui_settings.sql internal/store/ui_settings.go internal/store/ui_settings_test.go internal/store/sqlcgen schema.sql internal/httpapi/server.go internal/httpapi/server_test.go
  git commit -m "feat: persist server-owned UI locale"
  ```

## Task 2: Build and serve locale-specific static documents

**Files:**

- Modify: `web/astro.config.mjs`
- Modify: `web/package.json`
- Modify: `web/src/layouts/Shell.astro`
- Modify: `web/src/i18n/index.ts`
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/web_test.go`
- Modify: `web/embed_test.go`

**Interfaces:**

- The build receives `PUBLIC_ATCT_LOCALE` and writes only to `dist/en` or
  `dist/ja`; `pnpm build` runs `astro check` and both locale builds.
- `serveEmbeddedWeb` receives a validated server locale for HTML fallbacks;
  existing embedded files are served directly before fallback selection.

- [ ] **Step 1: Make the build deterministic per locale.** Validate the
  environment value in `astro.config.mjs`, set `base` to `/<locale>/`, set
  `outDir` to `dist/<locale>/`, and retain the root `.gitkeep` sentinel. Make
  the package `build` script run the check plus one build for `en` and one for
  `ja`; do not add a build dependency.

- [ ] **Step 2: Bind the static shell and i18next to the build locale.** In
  `Shell.astro`, derive the validated compile-time locale and render
  `<html lang={locale}>`. In `i18n/index.ts`, initialize `lng` from the same
  `PUBLIC_ATCT_LOCALE` value and remove the storage/browser resolution helpers;
  keep the existing translation resources and date/duration formatters.

- [ ] **Step 3: Select the server tree only for HTML fallbacks.** In the daemon
  handler, preserve `/api`, `/mcp`, and every exact embedded file first. For a
  fallback document, read `GetUILocale`, route `/`, `/goals/<id>`,
  `/tasks/<id>`, and unknown UI paths to `/<locale>/` or the matching
  `/<locale>/<route>/_/` template, and return an error if the store cannot be
  read. Strip an optional locale prefix only when resolving an HTML route; do
  not remap `/en/_astro/*` or `/ja/_astro/*` assets.

- [ ] **Step 4: Add routing/build coverage.** Assert that the two locale trees
  exist, that the root and dynamic documents switch when the stored locale is
  changed, that exact locale asset requests return their own non-HTML file,
  and that `/api/inbox`, `/mcp`, and `/api/events/reconcile` do not receive an
  HTML fallback.

- [ ] **Step 5: Verify and commit.** Run:

  ```sh
  (cd web && pnpm build)
  go test ./web -run 'TestDistEmbedMatchesSourceFiles'
  go test ./internal/daemon -run 'TestHTTPHandler(ServesLocale|RoutesAPI|MCP|KeepsSSE)'
  git add web/astro.config.mjs web/package.json web/src/layouts/Shell.astro web/src/i18n/index.ts internal/daemon/server.go internal/daemon/web_test.go web/embed_test.go
  git commit -m "feat: serve locale-specific web documents"
  ```

## Task 3: Persist locale switches and keep React hydration stable

**Files:**

- Modify: `web/src/lib/api.ts`
- Modify: `web/src/components/LocaleSwitch.tsx`
- Modify: `web/src/i18n/en.ts`
- Modify: `web/src/i18n/ja.ts`
- Create: `web/src/components/LocaleSwitch.test.tsx`
- Modify: `web/src/i18n/i18n.test.ts`
- Modify: `web/src/lib/api.test.ts`
- Modify: `web/src/lib/ui.test.ts`

**Interfaces:**

- `updateUILocale(locale: Locale): Promise<{ locale: Locale }>` sends the
  exact JSON shape to `PUT /api/ui-settings/locale` through `requestJson`.
- `LocaleSwitch` renders both locale buttons from the compile-time i18next
  state; it does not change language optimistically.

- [ ] **Step 1: Add the client API test and implementation.** Test that
  `updateUILocale("ja")` sends `Content-Type: application/json` and
  `{locale:"ja"}`, and that a non-2xx response remains an `ApiError`.

- [ ] **Step 2: Replace the switch's recovery effect.** Remove `useEffect`,
  `readStoredLocale`, `resolveLocale`, `storeLocale`, `localStorage`, and
  `navigator.language`. On selection, clear the error, disable both buttons,
  await `updateUILocale`, call `window.location.reload()` only on success, and
  otherwise leave the current i18next language/document unchanged.

- [ ] **Step 3: Add accessible failure behavior and tests.** Add the same
  non-empty locale-update error key to both translation resources. Test pending
  disabled state, successful reload, failed update with `role="alert"`, and
  no `changeLanguage` call. Update the source-contract test to assert the
  absence of browser locale sources and load-time recovery while retaining the
  shared formatting assertions.

- [ ] **Step 4: Verify and commit.** Run:

  ```sh
  (cd web && pnpm test -- src/i18n/i18n.test.ts src/components/LocaleSwitch.test.tsx src/lib/api.test.ts src/lib/ui.test.ts)
  (cd web && pnpm typecheck)
  git add web/src/lib/api.ts web/src/components/LocaleSwitch.tsx web/src/i18n/en.ts web/src/i18n/ja.ts web/src/components/LocaleSwitch.test.tsx web/src/i18n/i18n.test.ts web/src/lib/api.test.ts web/src/lib/ui.test.ts
  git commit -m "feat: persist locale switches through the daemon"
  ```

## Integration gate

After all three executor handoffs are accepted, the subcommander stages only
accepted paths and runs the focused Go tests above, `go test ./internal/store
./internal/httpapi ./internal/daemon ./web`, `(cd web && pnpm test)`,
`(cd web && pnpm typecheck)`, and `(cd web && pnpm build)`. The goal review
request must report every command result, the three task commit SHAs, and the
remaining boundary that adjacent goals were not edited.
