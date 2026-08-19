import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Goal, GoalResponse } from "../lib/api";
import { fetchGoal } from "../lib/api";
import { GoalDetail } from "./GoalDetail";

const i18nMock = vi.hoisted(() => ({
  t: (key: string) => key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

const apiMock = vi.hoisted(() => ({
  approveCompletion: vi.fn(),
  answerDecision: vi.fn(),
  fetchGoal: vi.fn(),
  rejectCompletion: vi.fn(),
  reviseDecision: vi.fn(),
  subscribeToDecisionEvents: vi.fn(() => () => undefined),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

vi.mock("../lib/api", () => ({
  ...apiMock,
  ApiError: class MockApiError extends Error {
    status = 0;
  },
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function goal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: "goal-1",
    project_id: "project-1",
    project_name: "Fixture project",
    title: "Fixture goal",
    description: "",
    status: "active",
    awaiting_decision: false,
    result_summary: "",
    work_done: "",
    now_possible: "",
    how_to_verify: "",
    surprises: "",
    needs_review: "",
    next_steps: "",
    created_at: "2026-08-20T00:00:00Z",
    updated_at: "2026-08-20T00:00:00Z",
    tasks: [],
    ...overrides,
  };
}

function goalResponse(overrides: Partial<Goal> = {}): GoalResponse {
  return {
    goal: goal(overrides),
    now: [],
    needs_decision: [],
    unattached_decisions: [],
    next: [],
    decision_history: [],
    decision_history_omitted: 0,
  };
}

describe("GoalDetail", () => {
  it("renders a goal with null tasks without throwing", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(
      goalResponse({ tasks: null, work_done: "The goal was completed." }),
    );

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(fetchGoal).toHaveBeenCalledWith("goal-1"));
    await waitFor(() => expect(screen.getByTestId("completion-report")).not.toBeNull());
    expect(screen.getByRole("heading", { level: 1 })).not.toBeNull();
  });

  it("renders the completion report only when at least one field is filled", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(goalResponse());
    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(fetchGoal).toHaveBeenCalledWith("goal-1"));
    expect(screen.queryByTestId("completion-report")).toBeNull();

    cleanup();
    vi.mocked(fetchGoal).mockResolvedValueOnce(goalResponse({ next_steps: "Continue monitoring." }));
    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByTestId("completion-report")).not.toBeNull());
  });
});
