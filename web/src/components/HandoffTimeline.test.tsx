import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Handoff, HandoffEntry } from "../lib/api";
import { HandoffTimeline } from "./HandoffTimeline";

const i18nMock = vi.hoisted(() => ({
  t: (key: string, options?: { count?: number; id?: number }) => options?.id !== undefined ? `${key}:${options.id}` : options?.count === undefined ? key : `${key}:${options.count}`,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

function entry(id: number, body: string, inReplyToID?: number): HandoffEntry {
  return {
    id,
    handoff_id: "handoff-1",
    kind: id === 1 ? "request" : "review_received",
    body,
    author_session_id: 1,
    in_reply_to_id: inReplyToID,
    created_at: "2026-08-20T00:00:00Z",
  };
}

function handoff(overrides: Partial<Handoff> = {}): Handoff {
  return {
    id: "handoff-1",
    scope: "goal",
    project_id: "project-1",
    goal_id: "goal-1",
    task_id: "",
    requested_by: "requester",
    received_by: "receiver",
    request_report: "legacy request report",
    complete_report: "",
    requested_at: "2026-08-20T00:00:00Z",
    received_at: "2026-08-20T00:00:01Z",
    completed_report_at: "",
    entries: [entry(1, "first request")],
    has_more: true,
    next_after_id: 1,
    ...overrides,
  };
}

describe("HandoffTimeline", () => {
  it("renders a read-only timeline and loads the next after_id page", async () => {
    const fetchHistory = vi.fn().mockResolvedValue(
      handoff({
        entries: [entry(2, "second review", 1)],
        has_more: false,
        next_after_id: 2,
      }),
    );

    render(<HandoffTimeline handoffs={[handoff()]} fetchHistory={fetchHistory} />);

    expect(screen.getByRole("heading", { name: "handoff.timeline.title" })).toBeTruthy();
    expect(screen.getByText("first request")).toBeTruthy();
    expect(screen.getByText("handoff.timeline.id:1")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "handoff.timeline.loadMore" }));

    await waitFor(() => expect(fetchHistory).toHaveBeenCalledWith("handoff-1", 1));
    expect(await screen.findByText("second review")).toBeTruthy();
    expect(screen.getByText("handoff.timeline.reply:1")).toBeTruthy();
    expect(screen.queryByText("legacy request report")).toBeNull();
    expect(screen.queryByRole("button", { name: "handoff.timeline.loadMore" })).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
  });
});
