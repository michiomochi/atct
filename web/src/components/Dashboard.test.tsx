import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Dashboard } from "./Dashboard";

const { fetchInbox, subscribeToDecisionEvents, t, i18nMock, goalCreateFormMock } = vi.hoisted(() => {
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
    goalCreateFormMock: vi.fn(),
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
  GoalCreateForm: (props: { onCreated?: () => void; onDirtyChange?: (dirty: boolean) => void }) => {
    goalCreateFormMock(props);
    return (
      <button type="button" data-testid="goal-create-form">
        Create goal
      </button>
    );
  },
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
    goalCreateFormMock.mockReset();
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
    await screen.findByRole("heading", { name: /dashboard\.goals\.title/ });

    expect(screen.queryByRole("region", { name: /proposed goals|提案中のゴール/i })).toBeNull();
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

    const formProps = () => {
      const calls = goalCreateFormMock.mock.calls;
      const props = calls[calls.length - 1]?.[0] as {
        onDirtyChange?: (dirty: boolean) => void;
      } | undefined;
      if (!props) {
        throw new Error("GoalCreateForm props were not captured");
      }
      return props;
    };

    act(() => {
      formProps().onDirtyChange?.(true);
      decisionEvent?.();
    });

    expect(fetchInbox).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("status").textContent).toContain("state.updateAvailable");

    act(() => {
      formProps().onDirtyChange?.(false);
      decisionEvent?.();
    });

    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(2));
  });
});
