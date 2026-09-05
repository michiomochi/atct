import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Handoff, HandoffEntry } from "../lib/api";
import { HandoffTimeline } from "./HandoffTimeline";

const i18nMock = vi.hoisted(() => ({
  t: (key: string, options?: { count?: number }) => options?.count === undefined ? key : `${key}:${options.count}`,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

function entry(id: string, sequence: number, body: string): HandoffEntry {
  return {
    entry_id: id,
    handoff_id: "handoff-1",
    sequence,
    kind: "progress",
    body,
    author_session_id: "session-1",
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
    request_report: "request",
    complete_report: "",
    requested_at: "2026-08-20T00:00:00Z",
    received_at: "2026-08-20T00:00:01Z",
    completed_report_at: "",
    entries: [entry("entry-1", 1, "first progress")],
    has_more: true,
    next_cursor: 1,
    ...overrides,
  };
}

describe("HandoffTimeline", () => {
  it("renders a read-only timeline and loads the next cursor page", async () => {
    const fetchHistory = vi.fn().mockResolvedValue(
      handoff({
        entries: [entry("entry-2", 2, "second progress")],
        has_more: false,
        next_cursor: 2,
      }),
    );

    render(<HandoffTimeline handoffs={[handoff()]} fetchHistory={fetchHistory} />);

    expect(screen.getByRole("heading", { name: "handoff.timeline.title" })).toBeTruthy();
    expect(screen.getByText("first progress")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "handoff.timeline.loadMore" }));

    await waitFor(() => expect(fetchHistory).toHaveBeenCalledWith("handoff-1", 1));
    expect(await screen.findByText("second progress")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "handoff.timeline.loadMore" })).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
  });
});
