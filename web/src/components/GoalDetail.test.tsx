import { within } from "@testing-library/dom";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Decision, Goal, GoalResponse, InboxResponse, Project, TaskView } from "../lib/api";
import { fetchGoal, fetchInbox, fetchProjects, subscribeToDecisionEvents } from "../lib/api";
import { Dashboard } from "./Dashboard";
import { GoalDetail } from "./GoalDetail";

const i18nMock = vi.hoisted(() => ({
  t: (key: string) => key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

const apiMock = vi.hoisted(() => ({
  approveCompletion: vi.fn(),
  answerDecision: vi.fn(),
  createGoal: vi.fn(),
  fetchGoal: vi.fn(),
  fetchInbox: vi.fn(),
  fetchProjects: vi.fn(),
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

function completionDecision(): Decision {
  return {
    id: "completion-1",
    goal_id: "goal-1",
    goal_title: "Fixture goal",
    kind: "completion",
    question: "Review the completion",
    options: [],
    status: "open",
    agent_session_id: "fixture-run",
    created_at: "2026-08-20T00:00:00Z",
  };
}

function emptyInbox(): InboxResponse {
  return {
    open_decisions: [],
    unapplied_decisions: [],
    active_goals: [],
    attention_tasks: [],
  };
}

function fixtureProject(): Project {
  return {
    id: "project-1",
    name: "Fixture project",
    root_path: "/tmp/fixture",
    created_at: "2026-08-20T00:00:00Z",
  };
}

describe("GoalDetail", () => {
  it("defers GoalDetail reload while completion reason is dirty and reloads after explicit refresh", async () => {
    let decisionEvent: Parameters<typeof subscribeToDecisionEvents>[0] | undefined;
    vi.mocked(subscribeToDecisionEvents).mockImplementation((callback) => {
      decisionEvent = callback;
      return () => undefined;
    });
    const response = goalResponse({});
    response.unattached_decisions = [completionDecision()];
    vi.mocked(fetchGoal).mockResolvedValue(response);

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByRole("textbox")).not.toBeNull());
    const reason = screen.getByRole("textbox");
    fireEvent.change(reason, { target: { value: "keep this reason" } });
    act(() => decisionEvent?.("decision.created"));

    expect((reason as HTMLTextAreaElement).value).toBe("keep this reason");
    expect(fetchGoal).toHaveBeenCalledTimes(1);
    expect(screen.getByText("state.updateAvailable")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "state.fetchLatest" }));
    await waitFor(() => expect(fetchGoal).toHaveBeenCalledTimes(2));
    await waitFor(() => expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe(""));

    act(() => decisionEvent?.("decision.created"));
    await waitFor(() => expect(fetchGoal).toHaveBeenCalledTimes(3));
    expect(screen.queryByText("state.updateAvailable")).toBeNull();
  });

  it("defers Dashboard reload while GoalCreateForm is dirty and reloads after explicit refresh", async () => {
    let decisionEvent: Parameters<typeof subscribeToDecisionEvents>[0] | undefined;
    vi.mocked(subscribeToDecisionEvents).mockImplementation((callback) => {
      decisionEvent = callback;
      return () => undefined;
    });
    vi.mocked(fetchInbox).mockResolvedValue(emptyInbox());
    vi.mocked(fetchProjects).mockResolvedValue([fixtureProject()]);

    render(<Dashboard />);

    await waitFor(() => expect(screen.getByRole("button", { name: "form.goal.action.new" })).not.toBeNull());
    fireEvent.click(screen.getByRole("button", { name: "form.goal.action.new" }));
    const title = screen.getByLabelText("form.goal.title.label");
    fireEvent.change(title, { target: { value: "typed title" } });
    act(() => decisionEvent?.("decision.created"));

    expect((title as HTMLInputElement).value).toBe("typed title");
    expect(fetchInbox).toHaveBeenCalledTimes(1);
    expect(screen.getByText("state.updateAvailable")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "state.fetchLatest" }));
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(2));

    await waitFor(() => expect(screen.getByRole("button", { name: "form.goal.action.new" })).not.toBeNull());
    fireEvent.click(screen.getByRole("button", { name: "form.goal.action.new" }));
    act(() => decisionEvent?.("decision.created"));
    await waitFor(() => expect(fetchInbox).toHaveBeenCalledTimes(3));
    expect(screen.queryByText("state.updateAvailable")).toBeNull();
  });

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
