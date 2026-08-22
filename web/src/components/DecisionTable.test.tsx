import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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
  const longQuestion =
    "Which deployment strategy should we use for the next release when the migration, rollback, and monitoring plans all need to be agreed on before the scheduled maintenance window?";
  const options = [
    {
      label: "Deploy gradually",
      description: "Release to a small group first and observe the result.",
      consequence: "The rollout takes longer but limits the blast radius.",
    },
    {
      label: "Deploy all at once",
      description: "Release the change to every target at the same time.",
      consequence: "The rollout is faster but a failure affects every target.",
    },
  ];

  test("keeps the full question and options out of the collapsed details", () => {
    render(
      <DecisionTable
        decisions={[decision({ question: longQuestion, options })]}
        emptyText="No decisions"
      />,
    );

    expect(screen.getByRole("button", { name: "decision.column.question" }).getAttribute("aria-expanded")).toBe(
      "false",
    );
    expect(screen.getByRole("link", { name: longQuestion }).classList.contains("text-clamp-2")).toBe(true);
    expect(screen.queryByText(longQuestion, { selector: "p" })).toBeNull();
    expect(screen.queryByText(options[0].description)).toBeNull();
    expect(screen.queryByText(options[0].consequence)).toBeNull();
  });

  test("shows the full question when the row is opened", () => {
    render(
      <DecisionTable
        decisions={[decision({ question: longQuestion, options })]}
        emptyText="No decisions"
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "decision.column.question" }));

    expect(screen.getByText(longQuestion, { selector: "p" })).toBeTruthy();
  });

  test("shows option explanations and consequences when the row is opened", () => {
    render(<DecisionTable decisions={[decision({ options })]} emptyText="No decisions" />);

    fireEvent.click(screen.getByRole("button", { name: "decision.column.question" }));

    expect(screen.getByText(options[0].description)).toBeTruthy();
    expect(screen.getByText(options[0].consequence)).toBeTruthy();
    expect(screen.getByText(options[1].description)).toBeTruthy();
    expect(screen.getByText(options[1].consequence)).toBeTruthy();
  });

  test("closes the first row when another row is opened", () => {
    render(
      <DecisionTable
        decisions={[
          decision({ id: "decision-1", options: [{ ...options[0], label: "First row" }] }),
          decision({
            id: "decision-2",
            question: "Second row question",
            options: [
              {
                ...options[0],
                label: "Second row",
                consequence: "The second row has its own consequence.",
              },
            ],
          }),
        ]}
        emptyText="No decisions"
      />,
    );

    const buttons = screen.getAllByRole("button", { name: "decision.column.question" });
    fireEvent.click(buttons[0]);
    fireEvent.click(buttons[1]);

    expect(buttons[0].getAttribute("aria-expanded")).toBe("false");
    expect(buttons[1].getAttribute("aria-expanded")).toBe("true");
    expect(screen.queryByText(options[0].consequence)).toBeNull();
    expect(screen.getByText("The second row has its own consequence.")).toBeTruthy();
  });

  test("updates aria-expanded when the row is toggled", () => {
    render(<DecisionTable decisions={[decision()]} emptyText="No decisions" />);

    const button = screen.getByRole("button", { name: "decision.column.question" });
    expect(button.getAttribute("aria-expanded")).toBe("false");

    fireEvent.click(button);

    expect(button.getAttribute("aria-expanded")).toBe("true");
  });

  test("does not expand when the question link is clicked", () => {
    render(<DecisionTable decisions={[decision()]} emptyText="No decisions" />);

    fireEvent.click(screen.getByRole("link", { name: "Which option should we choose?" }));

    expect(screen.getByRole("button", { name: "decision.column.question" }).getAttribute("aria-expanded")).toBe(
      "false",
    );
  });

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
