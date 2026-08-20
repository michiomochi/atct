import { within } from "@testing-library/dom";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Goal, GoalResponse, TaskView } from "../lib/api";
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

function taskView(id: string, title: string, order: number): TaskView {
  return {
    id,
    goal_id: "goal-1",
    title,
    description: "",
    status: "todo",
    agent: "fixture-agent",
    order,
    declare_key: "fixture-declare",
    claimed_by: "fixture-run",
    created_at: "",
    updated_at: "",
    held_for_seconds: 0,
    open_decisions: [],
    project_id: "project-1",
    project_name: "Fixture project",
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

  it("renders goal tasks in ascending order with one-based order labels", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(
      goalResponse({
        tasks: [
          taskView("task-two", "Third task", 5),
          taskView("task-zero", "First task", 3),
          taskView("task-one", "Second task", 4),
        ],
      }),
    );

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByText("First task")).not.toBeNull());
    const taskRows = screen.getAllByRole("row").filter((row) =>
      ["First task", "Second task", "Third task"].some((title) => row.textContent?.includes(title)),
    );

    expect(taskRows.map((row) => within(row).getAllByRole("cell")[0].textContent)).toEqual(["1", "2", "3"]);
    expect(taskRows.map((row) => within(row).getAllByRole("cell")[1].textContent)).toEqual([
      "First task",
      "Second task",
      "Third task",
    ]);
    expect(taskRows.map((row) => within(row).getAllByRole("cell")[0].textContent)).not.toContain("0");
  });

  it("renders one-based positions when task orders are duplicated", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(
      goalResponse({
        tasks: [
          taskView("task-third", "Third task", 0),
          taskView("task-first", "First task", 0),
          taskView("task-second", "Second task", 0),
        ],
      }),
    );

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByText("First task")).not.toBeNull());
    const taskRows = screen.getAllByRole("row").filter((row) =>
      ["First task", "Second task", "Third task"].some((title) => row.textContent?.includes(title)),
    );
    const orderLabels = taskRows.map((row) => within(row).getAllByRole("cell")[0].textContent);

    expect(orderLabels).toEqual(["1", "2", "3"]);
    expect(orderLabels).not.toEqual(["1", "1", "1"]);
  });
});
