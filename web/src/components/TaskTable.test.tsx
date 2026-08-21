import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TaskView } from "../lib/api";
import { TaskTable } from "./TaskTable";

const i18nMock = vi.hoisted(() => ({
  t: (key: string) => key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

vi.mock("../lib/api", () => ({
  answerDecision: vi.fn(),
  releaseTask: vi.fn(),
  reviseDecision: vi.fn(),
  ApiError: class MockApiError extends Error {
    status = 0;
  },
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function taskView(): TaskView {
  return {
    id: "task-1",
    goal_id: "goal-1",
    title: "Task title",
    description: "Task description",
    status: "todo",
    agent: "fixture-agent",
    files: ["src/task.ts"],
    order: 0,
    declare_key: "fixture-declare",
    claimed_by: "",
    created_at: "2026-08-20T00:00:00Z",
    updated_at: "2026-08-20T00:00:00Z",
    held_for_seconds: 0,
    open_decisions: [],
    project_id: "project-1",
    project_name: "Fixture project",
  };
}

describe("TaskTable", () => {
  it("links only the task title cell to the task detail page", () => {
    render(<TaskTable tasks={[taskView()]} mode="goal" onRefresh={vi.fn()} />);

    const link = screen.getByRole("link", { name: "Task title" });
    expect(link.getAttribute("href")).toBe("/tasks/task-1");
    expect(link.classList.contains("w-fit")).toBe(true);
    expect(link.classList.contains("text-accent-700")).toBe(true);
    expect(link.closest("td")).not.toBeNull();
    expect(link.parentElement?.tagName).toBe("TD");
    expect(link.parentElement?.children).toHaveLength(1);
    expect(link.closest("tr")?.querySelectorAll("a")).toHaveLength(1);
  });
});
