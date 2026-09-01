# Codex monitor baseline pagination

## Problem

The monitor currently performs App Server connect/initialize and every `thread/list`
page under one 10-second setup context.  A project with a large thread history can
therefore exhaust setup time in baseline pagination and fall back to ordinary Codex
before the monitored TUI starts.

## Decision

Split the baseline at the first `thread/list` response.  The first response remains
on the bounded setup context and establishes the pagination snapshot before the
monitor starts its TUI.  Its `nextCursor` and first-page IDs are handed to a
background baseline continuation.  The continuation follows every cursor to
completion using the monitor lifecycle context; only after it has collected every
ID may discovery start.

Cursor traversal is a snapshot traversal: pages reached through the cursor returned
by the first response describe the pre-TUI baseline.  Consequently, a thread created
by the TUI cannot enter the baseline merely because pagination finishes after the
TUI starts.  This preserves the existing ID-difference discovery rule without
accepting a partial baseline.

## Lifecycle and failure behavior

- Setup succeeds after connection, initialization, and first-page baseline capture;
  it no longer waits for the remaining pages.
- TUI, watch, bridge, record registration, strict thread status decoding, read limit,
  and notification handling retain their existing behavior.
- Discovery waits for successful full-baseline completion, then invokes the existing
  `DiscoverThread` with every baseline ID.  It continues to use full pagination per
  poll.
- If continuation fails or is cancelled, monitoring is disabled while the already
  launched Codex session remains active, matching other post-launch monitor failures.
- App Server absence or connect/initialize/first-page failure remains inside the
  short setup timeout and keeps the present automatic fallback / explicit failure
  policy.

## Tests

Use controllable channels in the lifecycle fake, not wall-clock sleeps or a changed
10-second constant.  Prove that a blocked second page does not prevent TUI launch,
that discovery does not run until that page is released, and that the baseline passed
to discovery contains old IDs from both pages while the new TUI thread is excluded.
Keep existing status-decode, remote-control no-op, and App Server fallback coverage.
