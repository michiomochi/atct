import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import type { Decision } from "../lib/api";
import { DecisionTable } from "./DecisionTable";

vi.mock("react-i18next", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-i18next")>();
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string) => key,
      i18n: { language: "en" },
    }),
  };
});

afterEach(() => {
  cleanup();
});

function decision(overrides: Partial<Decision> = {}): Decision {
  return {
    id: "decision-1",
    goal_id: "goal-1",
    goal_headline: "Fixture goal",
    project_name: "Fixture project",
    kind: "scope",
    question: "Which option should we choose?",
    options: [],
    status: "open",
    agent_session_id: "session-1",
    created_at: "2026-08-20T00:00:00Z",
    ...overrides,
  };
}

describe("DecisionTable", () => {
  test("links a task decision question to the task detail", () => {
    render(<DecisionTable decisions={[decision({ task_id: "task-1" })]} emptyText="No decisions" />);

    const questionLink = screen.getByRole("link", { name: "Which option should we choose?" });
    expect(questionLink.getAttribute("href")).toBe("/tasks/task-1");
  });

  test("links a goal decision question to the goal detail", () => {
    render(<DecisionTable decisions={[decision()]} emptyText="No decisions" />);

    const questionLink = screen.getByRole("link", { name: "Which option should we choose?" });
    expect(questionLink.getAttribute("href")).toBe("/goals/goal-1");
  });
});
