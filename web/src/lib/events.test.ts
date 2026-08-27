// @ts-expect-error Vitest runs this source audit in Node, but the app has no Node type dependency.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const sourceModules = import.meta.glob("../**/*.{ts,tsx,astro}", {
  eager: true,
  import: "default",
  query: "?raw",
}) as Record<string, string>;

const NON_SCREEN_EVENT_NAMES = [
  // wakeup events are agent signals, not reasons for a human dashboard refresh.
  "wakeup",
  "wakeup.discrepancy",
  // A failed evaluation changes no stored state, so refetching would redraw the
  // same screen. The failure reaches the human through atct watch instead.
  "wakeup.evaluate_failed",
  // handoff_yielded saves no state, so it is not a reason for a human dashboard refresh.
  "handoff_yielded",
] as const;

function readServerEventNames(): string[] {
  const nodeProcess = (globalThis as { process?: { cwd(): string } }).process;
  const repoRoot = nodeProcess?.cwd() ?? "";
  const wakeup = readFileSync(`${repoRoot}/../internal/store/wakeup.go`, "utf8");
  const serverSources = [
    wakeup,
    readFileSync(`${repoRoot}/../internal/store/goal.go`, "utf8"),
    readFileSync(`${repoRoot}/../internal/store/decision.go`, "utf8"),
  ];
  const names = [
    ...[...wakeup.matchAll(/\bEvent[A-Z][A-Za-z0-9_]*\s*=\s*"([^"]+)"/g)].map(
      (match) => match[1],
    ),
    ...serverSources.flatMap((source) =>
      [
        ...source.matchAll(
          /(?:publishEvent|PublishEvent)\((?:store\.)?(?:Event|DecisionEvent)\{Name:\s*"([^"]+)"/g,
        ),
      ].map((match) => match[1]),
    ),
  ];
  return [...new Set(names)];
}

function readScreenEventNames(): string[] {
  const uiSource = Object.entries(sourceModules).find(([path]) => path.endsWith("/ui.ts"))?.[1] ?? "";
  const decisionNames = uiSource.match(
    /export const DECISION_EVENT_NAMES\s*=\s*\[([\s\S]*?)\]\s*as const/,
  )?.[1] ?? "";
  const names = [...decisionNames.matchAll(/"([^"]+)"/g)].map((match) => match[1]);
  const keepaliveName = uiSource.match(
    /export const KEEPALIVE_EVENT_NAME\s*=\s*"([^"]+)"/,
  )?.[1];
  if (keepaliveName !== undefined) names.push(keepaliveName);
  return names;
}

describe("push event names", () => {
  it("finds event names in both server and screen sources", () => {
    expect(readServerEventNames().length).toBeGreaterThanOrEqual(10);
    expect(readScreenEventNames().length).toBeGreaterThan(0);
  });

  it("classifies every server event as subscribed or non-screen", () => {
    const subscribed = new Set(readScreenEventNames());
    const nonScreen = new Set<string>(NON_SCREEN_EVENT_NAMES);
    const unclassified = readServerEventNames().filter(
      (name) => !subscribed.has(name) && !nonScreen.has(name),
    );

    expect(unclassified).toEqual([]);
  });
});
