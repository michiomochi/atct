import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Dashboard } from "./Dashboard";

const { fetchInbox, subscribeToDecisionEvents, t } = vi.hoisted(() => ({
  fetchInbox: vi.fn(),
  subscribeToDecisionEvents: vi.fn(),
  t: (key: string) => key,
}));

vi.mock("../lib/api", () => ({
  fetchInbox,
  subscribeToDecisionEvents,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t }),
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
