import { within } from "@testing-library/dom";
import { cleanup, render, screen } from "@testing-library/react";
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

function taskView(id: string, title: string, order: number): TaskView {
  return {
    id,
    goal_id: "goal-1",
    title,
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

function goal(tasks: TaskView[]): Goal {
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
    created_at: "",
    updated_at: "",
    tasks,
  };
}

describe("GoalTable", () => {
  it("renders the task tree in ascending order without numbering it", () => {
    render(
      <GoalTable
        goals={[
          goal([
            taskView("task-two", "Third task", 2),
            taskView("task-zero", "First task", 0),
            taskView("task-one", "Second task", 1),
          ]),
        ]}
      />,
    );

    const taskRows = screen.getAllByRole("row").filter((row) =>
      ["First task", "Second task", "Third task"].some((title) => row.textContent?.includes(title)),
    );
    const taskCells = taskRows.map((row) => within(row).getAllByRole("cell")[0].textContent ?? "");

    expect(taskCells).toEqual(["├─First task", "├─Second task", "└─Third task"]);
    expect(taskCells.join(" ")).not.toMatch(/[0-9]/);
  });
});
