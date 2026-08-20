import { fireEvent, within } from "@testing-library/dom";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Decision, DecisionHistoryEntry, TaskView } from "../lib/api";
import { TaskDetailModal } from "./TaskDetailModal";

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
  reviseDecision: vi.fn(),
  ApiError: class MockApiError extends Error {
    status = 0;
  },
}));

afterEach(() => {
  cleanup();
});

function taskView(id: string, title: string, description = ""): TaskView {
  return {
    id,
    goal_id: "goal-1",
    title,
    description,
    status: "todo",
    agent: "fixture-agent",
    order: 0,
    declare_key: "fixture-declare",
    claimed_by: "fixture-run",
    created_at: "2026-08-20T00:00:00Z",
    updated_at: "2026-08-20T00:00:00Z",
    held_for_seconds: 0,
    open_decisions: [],
    project_id: "project-1",
    project_name: "Fixture project",
  };
}

function openDecision(id: string, taskID: string): Decision {
  return {
    id,
    goal_id: "goal-1",
    goal_title: "Fixture goal",
    task_id: taskID,
    kind: "question",
    question: "Which option should be used?",
    options: [{ label: "A", description: "", consequence: "" }],
    status: "open",
    agent_session_id: "fixture-run",
    created_at: "2026-08-20T00:00:00Z",
  };
}

function historyEntry(id: string, taskID: string): DecisionHistoryEntry {
  return {
    decision_id: id,
    task_id: taskID,
    question: "Which option was used?",
    answer_label: "A",
    answer_text: "",
    settled_by_default: false,
    answered_at: "2026-08-20T00:00:00Z",
    applied_at: "2026-08-20T00:00:00Z",
  };
}

function renderAndOpen(
  task: TaskView,
  openDecisions: Decision[],
  decisionHistory: DecisionHistoryEntry[],
) {
  render(
    <TaskDetailModal
      task={task}
      openDecisions={openDecisions}
      decisionHistory={decisionHistory}
      onUpdated={vi.fn()}
    >
      {task.title}
    </TaskDetailModal>,
  );
  fireEvent.click(screen.getByRole("button"));
  return screen.getByRole("dialog");
}

describe("TaskDetailModal", () => {
  it("shows decision history only for the task that has history", () => {
    const taskWithHistory = taskView("task-with-history", "Task with history");
    const taskWithoutHistory = taskView("task-without-history", "Task without history");

    const historyDialog = renderAndOpen(
      taskWithHistory,
      [],
      [historyEntry("decision-history", taskWithHistory.id)],
    );
    expect(within(historyDialog).getByTestId("decision-history")).not.toBeNull();

    cleanup();

    const emptyDialog = renderAndOpen(taskWithoutHistory, [], []);
    expect(within(emptyDialog).queryByTestId("decision-history")).toBeNull();
  });

  it("shows the answer form only for the task with an open decision", () => {
    const taskWithDecision = taskView("task-with-decision", "Task with decision");
    const taskWithoutDecision = taskView("task-without-decision", "Task without decision");

    const answerDialog = renderAndOpen(
      taskWithDecision,
      [openDecision("decision-open", taskWithDecision.id)],
      [],
    );
    expect(within(answerDialog).getByTestId("task-answer-form")).not.toBeNull();

    cleanup();

    const emptyDialog = renderAndOpen(taskWithoutDecision, [], []);
    expect(within(emptyDialog).queryByTestId("task-answer-form")).toBeNull();
  });

  it("exposes a change-assumption button for a history row", () => {
    const task = taskView("task-history", "Task history");
    const dialog = renderAndOpen(task, [], [historyEntry("decision-history", task.id)]);

    expect(
      within(dialog).getByRole("button", { name: "goal.history.changeAssumption" }),
    ).not.toBeNull();
  });

  it("shows descriptions only when present and always shows task attributes", () => {
    const taskWithDescription = taskView(
      "task-with-description",
      "Task with description",
      "Describe the work and its prerequisites.",
    );
    const taskWithoutDescription = taskView("task-without-description", "Task without description", "");

    const describedDialog = renderAndOpen(taskWithDescription, [], []);
    expect(within(describedDialog).getByText("task.detail.description")).not.toBeNull();
    expect(within(describedDialog).getByText("Describe the work and its prerequisites.")).not.toBeNull();
    expect(within(describedDialog).getByText("task.detail.attributes")).not.toBeNull();

    cleanup();

    const emptyDialog = renderAndOpen(taskWithoutDescription, [], []);
    expect(within(emptyDialog).queryByText("task.detail.description")).toBeNull();
    expect(within(emptyDialog).getByText("task.detail.attributes")).not.toBeNull();
  });

  it("omits the description section for whitespace-only descriptions", () => {
    const task = taskView("task-whitespace-description", "Task with whitespace description", " \n\t ");
    const dialog = renderAndOpen(task, [], []);

    expect(within(dialog).queryByText("task.detail.description")).toBeNull();
    expect(within(dialog).getByText("task.detail.attributes")).not.toBeNull();
  });
});
