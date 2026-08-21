import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Dashboard } from "./Dashboard";

const { fetchInbox, subscribeToDecisionEvents, t, i18nMock } = vi.hoisted(() => {
  const t = (key: string) => key;
  return {
    fetchInbox: vi.fn(),
    subscribeToDecisionEvents: vi.fn(),
    t,
    i18nMock: {
      t,
      i18n: { language: "en" },
      initReactI18next: { type: "3rdParty", init: () => undefined },
    },
  };
});

vi.mock("../lib/api", () => ({
  fetchInbox,
  subscribeToDecisionEvents,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

vi.mock("./GoalCreateForm", () => ({
  GoalCreateForm: () => null,
}));

vi.mock("./DecisionTable", () => ({
  DecisionTable: () => null,
}));

vi.mock("./GoalTable", () => ({
  GoalTable: () => null,
}));

vi.mock("./ProposedGoalTable", () => ({
  ProposedGoalTable: () => null,
}));

describe("Dashboard goal creation refresh guard", () => {
  let decisionEvent: (() => void) | undefined;

  beforeEach(() => {
    fetchInbox.mockReset().mockResolvedValue({ open_decisions: [], active_goals: [] });
    subscribeToDecisionEvents.mockReset().mockImplementation((handler: () => void) => {
      decisionEvent = handler;
      return () => {
        decisionEvent = undefined;
      };
    });
  });

  it("does not render the proposed goals section when there are no proposed goals", async () => {
    fetchInbox.mockResolvedValue({ open_decisions: [], active_goals: [], proposed_goals: [] });

    render(<Dashboard />);
    await screen.findByRole("heading", { name: /projects|プロジェクト/i });

    expect(screen.queryByRole("region", { name: /proposed goals|提案中のゴール/i })).toBeNull();
  });

  it("renders proposed goals before open decisions", async () => {
    fetchInbox.mockResolvedValue({ open_decisions: [], active_goals: [], proposed_goals: [{}] });

    const { container } = render(<Dashboard />);
    await screen.findByRole("heading", { name: /dashboard\.proposed\.title/, level: 2 });

    const headings = Array.from(container.querySelectorAll("h2"));
    const proposedHeadingIndex = headings.findIndex((heading) => heading.id === "proposed-goals-heading");
    const openDecisionsHeadingIndex = headings.findIndex((heading) => heading.id === "open-decisions-heading");

    expect(proposedHeadingIndex).toBeGreaterThanOrEqual(0);
    expect(openDecisionsHeadingIndex).toBeGreaterThanOrEqual(0);
    expect(proposedHeadingIndex).toBeLessThan(openDecisionsHeadingIndex);
  });

  it("keeps the current data while the goal form is dirty and reloads after it is clean", async () => {
    render(<Dashboard />);
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(1));

    act(() => {
      window.dispatchEvent(new CustomEvent("atct:form-dirty", { detail: true }));
      decisionEvent?.();
    });

    expect(fetchInbox).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("status").textContent).toContain("state.updateAvailable");

    act(() => {
      window.dispatchEvent(new CustomEvent("atct:form-dirty", { detail: false }));
      decisionEvent?.();
    });

    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(2));
  });
});
