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
  GoalCreateForm: () => (
    <button type="button" data-testid="goal-create-form">
      Create goal
    </button>
  ),
}));

vi.mock("./ProjectList", () => ({
  ProjectList: ({ onChanged }: { onChanged?: () => void }) => (
    <button type="button" data-testid="project-list" onClick={onChanged}>
      Projects
    </button>
  ),
}));

vi.mock("./DecisionTable", () => ({
  DecisionTable: () => null,
}));

vi.mock("./GoalTable", () => ({
  GoalTable: () => null,
}));

describe("Dashboard", () => {
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

  it("renders the goal create form in the active goals section heading action", async () => {
    render(<Dashboard />);

    await screen.findAllByRole("heading", { name: /dashboard\.goals\.title/ });
    const sections = document.querySelectorAll('section[aria-labelledby="active-goals-heading"]');
    const section = sections[sections.length - 1];
    const heading = section?.querySelector("h2");
    const form = section?.querySelector('[data-testid="goal-create-form"]');

    if (!section || !heading || !form) {
      throw new Error("The active goals section action was not rendered");
    }
    expect(heading.parentElement?.contains(form)).toBe(true);
  });

  it("shows an update banner without reloading when a decision event arrives", async () => {
    render(<Dashboard />);
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(1));

    act(() => {
      decisionEvent?.();
    });

    expect(fetchInbox).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("status").textContent).toContain("state.updateAvailable");
  });

  it("reloads the inbox when a project is archived or unarchived", async () => {
    const { fireEvent } = await import("@testing-library/react");
    render(<Dashboard />);
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(1));

    const buttons = await screen.findAllByTestId("project-list");
    fireEvent.click(buttons[buttons.length - 1]);

    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(2));
  });
});
