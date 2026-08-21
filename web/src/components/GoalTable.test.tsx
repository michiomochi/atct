import { within } from "@testing-library/dom";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Goal, TaskView } from "../lib/api";
import { GoalTable } from "./GoalTable";

const i18nMock = vi.hoisted(() => ({
  t: (key: string) => key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

afterEach(() => {
  cleanup();
});

function taskView(goalID: string, id: string, title: string, order: number): TaskView {
  return {
    id,
    goal_id: goalID,
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

function goal(id: string, content: string, tasks: TaskView[]): Goal {
  return {
    id,
    project_id: "project-1",
    project_name: "Fixture project",
    content,
    status: "active",
    awaiting_decision: false,
    result_summary: "",
    work_done: "",
    now_possible: "",
    how_to_verify: "",
    surprises: "",
    needs_review: "",
    next_steps: "",
    created_at: "",
    updated_at: "",
    tasks,
  };
}

function fixtureGoals(): Goal[] {
  return [
    goal("goal-1", "First goal", [
      taskView("goal-1", "task-one", "First task", 0),
      taskView("goal-1", "task-two", "Second task", 1),
    ]),
    goal("goal-2", "Second goal", [
      taskView("goal-2", "task-other", "Other goal task", 0),
    ]),
  ];
}

function openGoal(content: string) {
  const goalLink = screen.getByRole("link", { name: content });
  const goalRow = goalLink.closest("tr");
  if (!goalRow) throw new Error(`Goal ${content} has no table row`);
  fireEvent.click(
    within(goalRow).getByRole("button", { name: "goal.tasks.title" }),
  );
}

describe("GoalTable", () => {
  it("keeps task rows closed by default", () => {
    render(<GoalTable goals={fixtureGoals()} />);

    expect(screen.queryByRole("link", { name: "First task" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Other goal task" })).toBeNull();
  });

  it("updates the task toggle's aria-expanded state", () => {
    render(<GoalTable goals={fixtureGoals()} />);

    const goalRow = screen.getByRole("link", { name: "First goal" }).closest("tr");
    if (!goalRow) throw new Error("First goal has no table row");
    const toggle = within(goalRow).getByRole("button", {
      name: "goal.tasks.title",
    });

    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
  });

  it("shows the opened goal's task rows", () => {
    render(<GoalTable goals={fixtureGoals()} />);

    openGoal("First goal");

    expect(screen.getByRole("link", { name: "First task" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Second task" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "Other goal task" })).toBeNull();
  });

  it("keeps task visibility independent between goals", () => {
    render(<GoalTable goals={fixtureGoals()} />);

    openGoal("Second goal");

    expect(screen.getByRole("link", { name: "Other goal task" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "First task" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Second task" })).toBeNull();
  });

  it("shows every task when a goal with multiple tasks is opened", () => {
    render(<GoalTable goals={fixtureGoals()} />);

    openGoal("First goal");

    expect(screen.getByRole("link", { name: "First task" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Second task" })).toBeTruthy();
  });

  it("renders the task tree in ascending order without numbering it", () => {
    render(
      <GoalTable
        goals={[
          goal("goal-1", "Fixture goal", [
            taskView("goal-1", "task-two", "Third task", 2),
            taskView("goal-1", "task-zero", "First task", 0),
            taskView("goal-1", "task-one", "Second task", 1),
          ]),
        ]}
      />,
    );

    openGoal("Fixture goal");

    const taskRows = screen.getAllByRole("row").filter((row) =>
      ["First task", "Second task", "Third task"].some((title) => row.textContent?.includes(title)),
    );
    const taskCells = taskRows.map((row) => within(row).getAllByRole("cell")[0].textContent ?? "");

    expect(taskCells).toEqual(["├─First task", "├─Second task", "└─Third task"]);
    expect(taskCells.join(" ")).not.toMatch(/[0-9]/);
  });

  it("links task titles to their task detail pages without expanding to the row width", () => {
    render(
      <GoalTable
        goals={[goal("goal-1", "Fixture goal", [taskView("goal-1", "task-detail", "Linked task", 0)])]}
      />,
    );

    openGoal("Fixture goal");

    const taskLink = screen.getByRole("link", { name: "Linked task" });

    expect(taskLink.getAttribute("href")).toBe("/tasks/task-detail");
    expect(taskLink.className).toContain("w-fit");
    expect(taskLink.className).toContain("text-accent-700");
  });
});
