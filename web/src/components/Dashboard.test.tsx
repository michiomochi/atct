import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

describe("Dashboard stalled notice", () => {
  const staleDecision = {
    id: "3",
    goal_id: "11",
    goal_headline: "Ship the stalled goal",
    project_name: "atct",
    kind: "decision",
    question: "q",
    options: [],
    status: "answered",
    answered_at: new Date(Date.now() - 11 * 60 * 1000).toISOString(),
    stale_unapplied: true,
    agent_session_id: "1",
    created_at: "2026-10-20T00:00:00Z",
  };

  beforeEach(() => {
    fetchInbox.mockReset();
    subscribeToDecisionEvents.mockReset().mockImplementation(() => () => undefined);
  });

  afterEach(() => {
    cleanup();
  });

  it("does not render the alert when nothing is stale or stalled", async () => {
    fetchInbox.mockResolvedValue({ open_decisions: [], unapplied_decisions: [], active_goals: [], stalled_projects: [] });
    render(<Dashboard />);
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("renders the stale unapplied decision with its project and goal", async () => {
    fetchInbox.mockResolvedValue({
      open_decisions: [],
      unapplied_decisions: [staleDecision],
      active_goals: [],
      stalled_projects: [],
    });
    render(<Dashboard />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("atct");
    expect(alert.textContent).toContain("Ship the stalled goal");
    expect(alert.textContent).toContain("dashboard.stalled.noHeartbeat");
  });

  it("renders a silent project by name", async () => {
    fetchInbox.mockResolvedValue({
      open_decisions: [],
      unapplied_decisions: [],
      active_goals: [],
      stalled_projects: [{ project_id: 9, project_name: "quiet-project", last_seen_at: null }],
    });
    render(<Dashboard />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("quiet-project");
  });

  it("renders both a stale decision and a silent project together", async () => {
    fetchInbox.mockResolvedValue({
      open_decisions: [],
      unapplied_decisions: [staleDecision],
      active_goals: [],
      stalled_projects: [{ project_id: 9, project_name: "quiet-project", last_seen_at: "2026-10-20T00:00:00Z" }],
    });
    render(<Dashboard />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Ship the stalled goal");
    expect(alert.textContent).toContain("quiet-project");
  });

  it("leaves out an unapplied decision that is not stale", async () => {
    fetchInbox.mockResolvedValue({
      open_decisions: [],
      unapplied_decisions: [{ ...staleDecision, id: "4", stale_unapplied: false }],
      active_goals: [],
      stalled_projects: [],
    });
    render(<Dashboard />);
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
