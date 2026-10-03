import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReviewExchange, ReviewExchangeHistory } from "../lib/api";
import { ReviewExchanges } from "./ReviewExchanges";

const i18nMock = vi.hoisted(() => ({
  t: (key: string, options?: { id?: number; total?: number }) => options?.id !== undefined ? `${key}:${options.id}` : options?.total !== undefined ? `${key}:${options.total}` : key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

afterEach(cleanup);

function exchange(overrides: Partial<ReviewExchange> = {}): ReviewExchange {
  return {
    scope: "goal",
    handoff_id: "goal-h1",
    rejection: { source: "handoff", at: "2026-10-01T00:00:01Z", actor_session_id: 3, reason: "line one\nline two" },
    response: { at: "2026-10-01T00:00:05Z", author_session_id: 4, handoff_id: "goal-h2", report: "fixed it" },
    ...overrides,
  };
}

async function renderHistory(history: ReviewExchangeHistory, showTaskBadge?: boolean) {
  render(<ReviewExchanges fetchHistory={() => Promise.resolve(history)} showTaskBadge={showTaskBadge} />);
  await screen.findByTestId("review-exchanges");
  await waitFor(() => expect(screen.queryByLabelText("state.loadingLabel")).toBeNull());
}

// Cards and gap notes in document order, as "early"/"late" or "gap:<count>".
function timelineOrder(): string[] {
  const nodes = screen.getByTestId("review-exchanges").querySelectorAll("article, details");
  return [...nodes].map((node) => {
    const text = node.textContent ?? "";
    if (node.tagName === "DETAILS") return `gap:${(node.querySelectorAll("li")).length}`;
    return text.includes("early") ? "early" : text.includes("late") ? "late" : text.includes("after") ? "after" : "card";
  });
}

describe("ReviewExchanges", () => {
  it("shows a handoff rejection with its response", async () => {
    await renderHistory({ exchanges: [exchange()], gaps: [] });

    const card = (await screen.findByText("reviewExchange.source.handoff")).closest("article")!;
    expect(within(card).getByText("reviewExchange.actor.session:3")).not.toBeNull();
    expect(within(card).getByText(/line one\s+line two/).className).toContain("whitespace-pre-wrap");
    expect(within(card).getByText("reviewExchange.response")).not.toBeNull();
    expect(within(card).getByText("reviewExchange.actor.session:4")).not.toBeNull();
    expect(within(card).getByText("goal-h2")).not.toBeNull();
    expect(within(card).getByText("fixed it")).not.toBeNull();
    expect(within(card).queryByText("reviewExchange.pending")).toBeNull();
  });

  it("labels human rejections and withdrawals without a session actor", async () => {
    await renderHistory({
      exchanges: [
        exchange({ handoff_id: undefined, rejection: { source: "human", at: "2026-10-01T00:00:02Z", decision_id: 9, reason: "not good" }, response: null }),
        exchange({ handoff_id: undefined, rejection: { source: "withdrawn", at: "2026-10-01T00:00:03Z", decision_id: 10, reason: "dropped" }, response: null }),
      ],
      gaps: [],
    });

    const human = (await screen.findByText("reviewExchange.source.human")).closest("article")!;
    expect(within(human).getByText("reviewExchange.actor.human")).not.toBeNull();
    expect(within(human).getByText("not good")).not.toBeNull();
    const withdrawn = screen.getByText("reviewExchange.source.withdrawn").closest("article")!;
    expect(within(withdrawn).queryByText(/reviewExchange\.actor/)).toBeNull();
    expect(within(withdrawn).getByText("dropped")).not.toBeNull();
  });

  it("shows awaiting-response when there is no response", async () => {
    await renderHistory({ exchanges: [exchange({ response: null })], gaps: [] });

    expect(await screen.findByText("reviewExchange.pending")).not.toBeNull();
    expect(screen.queryByText("reviewExchange.response")).toBeNull();
  });

  it("badges plan and task cards, and drops the task badge on the task page", async () => {
    const history: ReviewExchangeHistory = {
      exchanges: [exchange({ scope: "plan" }), exchange({ scope: "task", task_id: 7, rejection: { source: "handoff", at: "2026-10-01T00:00:02Z", reason: "t" } })],
      gaps: [],
    };
    await renderHistory(history);
    expect(await screen.findByText("reviewExchange.scope.plan")).not.toBeNull();
    expect(screen.getByText("reviewExchange.scope.task:7")).not.toBeNull();

    cleanup();
    await renderHistory(history, false);
    expect(await screen.findByText("reviewExchange.scope.plan")).not.toBeNull();
    expect(screen.queryByText("reviewExchange.scope.task:7")).toBeNull();
  });

  it("merges gaps into the timeline and notes each run of gaps once", async () => {
    await renderHistory({
      exchanges: [
        exchange({ rejection: { source: "handoff", at: "2026-10-01T00:00:09Z", reason: "late" } }),
        exchange({ rejection: { source: "handoff", at: "2026-10-01T00:00:01Z", reason: "early" } }),
      ],
      gaps: [
        { scope: "goal", handoff_id: "old-h", before_at: "2026-10-01T00:00:00Z" },
        { scope: "task", handoff_id: "mid-h", task_id: 2, before_at: "2026-10-01T00:00:05Z" },
      ],
    });

    await screen.findByText("early");
    expect(timelineOrder()).toEqual(["gap:1", "early", "gap:1", "late"]);
  });

  it("collapses consecutive gaps into one note with a count", async () => {
    await renderHistory({
      exchanges: [exchange({ rejection: { source: "handoff", at: "2026-10-01T00:00:09Z", reason: "after" } })],
      gaps: [
        { scope: "goal", handoff_id: "g-1", before_at: "2026-10-01T00:00:01Z" },
        { scope: "goal", handoff_id: "g-2", before_at: "2026-10-01T00:00:02Z" },
        { scope: "plan", handoff_id: "g-3", before_at: "2026-10-01T00:00:03Z" },
      ],
    });

    await screen.findByText("after");
    expect(timelineOrder()).toEqual(["gap:3", "after"]);
    expect(screen.getAllByText(/reviewExchange\.gapCount:3/)).toHaveLength(1);
  });

  it("lists each gap's handoff and time inside the opened details", async () => {
    await renderHistory({
      exchanges: [],
      gaps: [
        { scope: "goal", handoff_id: "g-1", before_at: "2026-10-01T00:00:01Z" },
        { scope: "task", handoff_id: "g-2", task_id: 4, before_at: "2026-10-01T00:00:02Z" },
      ],
    });

    const note = (await screen.findByText(/reviewExchange\.gap/)).closest("details")!;
    expect(note.hasAttribute("open")).toBe(false);
    fireEvent.click(within(note).getByText(/reviewExchange\.gap/));
    expect(note.hasAttribute("open")).toBe(true);
    expect(within(note).getByText("g-1")).not.toBeNull();
    expect(within(note).getByText("g-2")).not.toBeNull();
    expect(within(note).getAllByRole("listitem")).toHaveLength(2);
  });

  it("shows the empty state when there is nothing", async () => {
    await renderHistory({ exchanges: [], gaps: [] });

    expect(await screen.findByText("reviewExchange.empty")).not.toBeNull();
  });

  it("shows only this section's error and retries", async () => {
    const fetchHistory = vi.fn()
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce({ exchanges: [exchange()], gaps: [] });
    render(<ReviewExchanges fetchHistory={fetchHistory} />);

    expect((await screen.findByRole("alert")).textContent).toContain("boom");
    fireEvent.click(screen.getByRole("button", { name: "state.retry" }));
    expect(await screen.findByText("fixed it")).not.toBeNull();
    expect(fetchHistory).toHaveBeenCalledTimes(2);
  });
});
