# Server-owned UI locale

## Goal

Persist the single user's UI locale in the ATCT daemon and serve a complete
static document whose Astro shell and React islands start in that same locale.
This removes Goal 235's post-hydration locale race.

## Measured baseline

Goal 245 was fast-forwarded normally to current `main` at
`432b7d07d583212dda6c4438fead56580cf96de7`. The embedded migration sequence now
ends at `0043_runtime_heartbeat_lease.sql`, so this goal owns only
`0044_ui_settings.sql`. The pre-merge diff contains no Goal 245 locale
implementation; the current client still reads `localStorage` and
`navigator.language`, and applies the choice after a load-time delay. No prior
Goal 245 task produced a commit that can be linked without misattribution.

Before implementation, `main` advanced to
`d3817ade631b56a153a5304eebf74b658643b15c`, which adds
`0044_fixed_width_timestamps.sql`. The implementation worktree must merge that
current main normally and remeasure the sequence immediately before creating
the locale migration; the next available number is therefore
`0046_ui_settings.sql`.

## Data and HTTP contract

- Supported values are exactly `en` and `ja`.
- `ui_settings` is a singleton row (`id = 1`) constrained to those values and
  seeded with `en` by migration `0046_ui_settings.sql`.
- Store reads return `en` when the row is missing or contains an unsupported
  value. Store writes validate before changing the row.
- `GET /api/ui-settings` returns `{ "locale": "en" | "ja" }`.
- `PUT /api/ui-settings/locale` accepts that same object and returns the saved
  object. Malformed JSON, a missing locale, and unsupported values return 400;
  rejected writes leave the stored value unchanged.

The daemon database is the only persistent authority. There is no
per-project locale, authentication, Accept-Language negotiation, or general
preferences framework.

## Static document and hydration contract

The web build runs once per supported locale and emits complete trees at
`dist/en` and `dist/ja`. Each build receives `PUBLIC_ATCT_LOCALE`, uses a
matching `/<locale>/` asset base, sets `<html lang>` to that value, and
initializes i18next with the same compile-time value.

The daemon preserves literal files such as `/en/_astro/*` and `/ja/_astro/*`,
then maps `/`, dynamic goal/task documents, and unknown UI fallbacks to the
tree selected by the stored locale. API, MCP, WebSocket, SSE, and
reconciliation precedence remains unchanged. A static asset request does not
fall through to an HTML document.

`LocaleSwitch` sends the selected locale to the server, is disabled while the
request is pending, and reloads only after a successful response. On failure
it leaves i18next and the document unchanged and exposes an accessible error.
No initial-render path reads browser storage, browser language, waits for a
load event, patches the DOM, or calls `changeLanguage` after hydration.

## Scope and verification

Implementation is limited to the daemon store/API, Astro build and shell,
static web routing, i18n initialization, the locale switch, and focused tests.
Adjacent goals 273 (`next_goals`) and 282 (`KindCompletion`) remain outside
this goal; shared-file overlap is recorded for review rather than edited.

Acceptance requires focused store/API/daemon tests, the web locale test set,
`pnpm typecheck`, and a successful dual-locale `pnpm build`.
