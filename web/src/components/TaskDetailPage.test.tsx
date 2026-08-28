import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Decision, DecisionHistoryEntry, Task, TaskCommitDiff, TaskDetailResponse } from "../lib/api";
import { fetchTask, fetchTaskCommitDiff, snoozeTask, subscribeToDecisionEvents } from "../lib/api";
import { TaskDetailPage } from "./TaskDetailPage";

const i18nMock = vi.hoisted(() => ({
  t: (key: string, options?: { count?: number; until?: string }) => {
    if (options?.count !== undefined) return `${key}:${options.count}`;
    if (options?.until !== undefined) return `${key}:${options.until}`;
    return key;
  },
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

const apiMock = vi.hoisted(() => ({
  answerDecision: vi.fn(),
  fetchTask: vi.fn(),
  fetchTaskCommitDiff: vi.fn(),
  reviseDecision: vi.fn(),
  snoozeTask: vi.fn(),
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
  window.history.replaceState({}, "", "/");
  vi.clearAllMocks();
});

function taskDetailWithCommits(commits: unknown[]): TaskDetailResponse {
  return {
    task: task("task-1", "Commit task"),
    goal: {} as TaskDetailResponse["goal"],
    open_decisions: [],
    decision_history: [],
    decision_history_omitted: 0,
    commits,
  } as TaskDetailResponse;
}

async function renderTaskDetailWithCommits(commits: unknown[]) {
  window.history.replaceState({}, "", "/tasks/task-1");
  apiMock.fetchTask.mockResolvedValue(taskDetailWithCommits(commits));
  render(<TaskDetailPage id="task-1" />);
  await screen.findByRole("heading", { name: "Commit task" });
}

describe("TaskDetailPage commits", () => {
  it("does not render the commits section when commits are empty", async () => {
    await renderTaskDetailWithCommits([]);

    expect(screen.queryByRole("heading", { name: "task.detail.commits" })).toBeNull();
  });

  it("renders missing-history commits with subject and stats", async () => {
    await renderTaskDetailWithCommits([
      {
        sha: "abcdef1234567890",
        short_sha: "abcdef1",
        subject: "keep task detail history",
        files_changed: 3,
        insertions: 40,
        deletions: 12,
        in_history: false,
      },
    ]);

    expect(screen.getByText("keep task detail history")).toBeTruthy();
    expect(screen.getByText(/3 task\.detail\.commitFiles · \+40 −12/)).toBeTruthy();
    expect(screen.getByText("task.detail.commitMissing")).toBeTruthy();
  });

  it("does not mark commits that remain in history as missing", async () => {
    await renderTaskDetailWithCommits([
      {
        sha: "abcdef1234567890",
        short_sha: "abcdef1",
        subject: "keep task detail history",
        files_changed: 3,
        insertions: 40,
        deletions: 12,
        in_history: true,
      },
    ]);

    expect(screen.getByRole("heading", { name: "task.detail.commits" })).toBeTruthy();
    expect(screen.queryByText("task.detail.commitMissing")).toBeNull();
  });

  it("does not fetch a commit diff until its details are opened", async () => {
    await renderTaskDetailWithCommits([
      {
        sha: "abcdef1234567890",
        short_sha: "abcdef1",
        subject: "read commit diff",
        files_changed: 1,
        insertions: 2,
        deletions: 0,
        in_history: true,
      },
    ]);

    expect(fetchTaskCommitDiff).not.toHaveBeenCalled();
  });

  it("fetches a commit diff only once when details are reopened", async () => {
    const commitSHA = "abcdef1234567890";
    const diff: TaskCommitDiff = {
      sha: commitSHA,
      in_history: true,
      files: [{ path: "src/task.ts", insertions: 2, deletions: 0, binary: false }],
      body: "diff --git a/src/task.ts b/src/task.ts",
      omitted_lines: 0,
    };
    vi.mocked(fetchTaskCommitDiff).mockResolvedValue(diff);
    await renderTaskDetailWithCommits([
      {
        sha: commitSHA,
        short_sha: "abcdef1",
        subject: "read commit diff",
        files_changed: 1,
        insertions: 2,
        deletions: 0,
        in_history: true,
      },
    ]);

    const summary = screen.getByText("task.detail.commitDiff");
    fireEvent.click(summary);
    await screen.findByText(diff.body);
    fireEvent.click(summary);
    fireEvent.click(summary);
    await waitFor(() => expect(fetchTaskCommitDiff).toHaveBeenCalledTimes(1));
    expect(fetchTaskCommitDiff).toHaveBeenCalledWith("task-1", commitSHA);
  });

  it("does not show omitted lines when omitted_lines is zero", async () => {
    vi.mocked(fetchTaskCommitDiff).mockResolvedValue({
      sha: "abcdef1234567890",
      in_history: true,
      files: [{ path: "src/task.ts", insertions: 2, deletions: 0, binary: false }],
      body: "diff --git a/src/task.ts b/src/task.ts",
      omitted_lines: 0,
    });
    await renderTaskDetailWithCommits([
      {
        sha: "abcdef1234567890",
        short_sha: "abcdef1",
        subject: "read commit diff",
        files_changed: 1,
        insertions: 2,
        deletions: 0,
        in_history: true,
      },
    ]);

    fireEvent.click(screen.getByText("task.detail.commitDiff"));
    await screen.findByText("diff --git a/src/task.ts b/src/task.ts");

    expect(screen.queryByText("task.detail.commitDiffOmitted:0")).toBeNull();
    expect(screen.queryByText("task.detail.commitDiffOmitted")).toBeNull();
  });

  it("shows the omitted line count when omitted_lines is positive", async () => {
    vi.mocked(fetchTaskCommitDiff).mockResolvedValue({
      sha: "abcdef1234567890",
      in_history: true,
      files: [{ path: "src/task.ts", insertions: 2, deletions: 0, binary: false }],
      body: "diff --git a/src/task.ts b/src/task.ts",
      omitted_lines: 3,
    });
    await renderTaskDetailWithCommits([
      {
        sha: "abcdef1234567890",
        short_sha: "abcdef1",
        subject: "read commit diff",
        files_changed: 1,
        insertions: 2,
        deletions: 0,
        in_history: true,
      },
    ]);

    fireEvent.click(screen.getByText("task.detail.commitDiff"));

    expect(await screen.findByText("task.detail.commitDiffOmitted:3")).toBeTruthy();
  });

  it("shows an empty message instead of an error for commits outside history", async () => {
    vi.mocked(fetchTaskCommitDiff).mockResolvedValue({
      sha: "abcdef1234567890",
      in_history: false,
      files: [],
      body: "",
      omitted_lines: 0,
    });
    await renderTaskDetailWithCommits([
      {
        sha: "abcdef1234567890",
        short_sha: "abcdef1",
        subject: "read commit diff",
        files_changed: 1,
        insertions: 2,
        deletions: 0,
        in_history: false,
      },
    ]);

    fireEvent.click(screen.getByText("task.detail.commitDiff"));

    expect(await screen.findByText("task.detail.commitDiffEmpty")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

function task(id: string, title: string, description = "", snoozedUntil?: string): Task {
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
    snoozed_until: snoozedUntil,
  };
}

function openDecision(id: string, taskID: string): Decision {
  return {
    id,
    goal_id: "goal-1",
    goal_headline: "Fixture goal",
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

function detailResponse(
  taskData: Task,
  openDecisions: Decision[] = [],
  decisionHistory: DecisionHistoryEntry[] = [],
): TaskDetailResponse {
  return {
    task: taskData,
    goal: { id: taskData.goal_id, title: "Fixture goal", project_name: "Fixture project" },
    open_decisions: openDecisions,
    decision_history: decisionHistory,
    decision_history_omitted: 0,
    commits: [],
  };
}

async function renderTask(response: TaskDetailResponse) {
  vi.mocked(fetchTask).mockResolvedValue(response);
  render(<TaskDetailPage id={response.task.id} />);
  await screen.findByRole("heading", { name: response.task.title });
}

describe("TaskDetailPage", () => {
  it("shows the declare key as the only task file attribute", async () => {
    const taskData = task("task-declare-key", "Task declare key");
    await renderTask(detailResponse(taskData));

    expect(screen.getByText("task.detail.declareKey")).not.toBeNull();
    expect(screen.getByText("fixture-declare")).not.toBeNull();
  });

  it("uses the task ID from the URL when Astro passes the sentinel ID", async () => {
    const taskData = task("task-from-url", "Task from URL");
    window.history.replaceState({}, "", `/tasks/${encodeURIComponent(taskData.id)}`);
    vi.mocked(fetchTask).mockResolvedValue(detailResponse(taskData));

    render(<TaskDetailPage id="_" />);
    await screen.findByRole("heading", { name: taskData.title });

    expect(fetchTask).toHaveBeenCalledWith(taskData.id);
  });

  it("keeps a real task ID instead of using the URL", async () => {
    const taskData = task("task-from-props", "Task from props");
    window.history.replaceState({}, "", "/tasks/task-from-url");
    vi.mocked(fetchTask).mockResolvedValue(detailResponse(taskData));

    render(<TaskDetailPage id={taskData.id} />);
    await screen.findByRole("heading", { name: taskData.title });

    expect(fetchTask).toHaveBeenCalledWith(taskData.id);
  });

  it("shows decision history only for the task that has history", async () => {
    const taskWithHistory = task("task-with-history", "Task with history");
    const taskWithoutHistory = task("task-without-history", "Task without history");

    await renderTask(detailResponse(taskWithHistory, [], [historyEntry("decision-history", taskWithHistory.id)]));
    expect(screen.getByTestId("decision-history")).not.toBeNull();

    cleanup();

    await renderTask(detailResponse(taskWithoutHistory));
    expect(screen.queryByTestId("decision-history")).toBeNull();
  });

  it("shows the answer form only for the task with an open decision", async () => {
    const taskWithDecision = task("task-with-decision", "Task with decision");
    const taskWithoutDecision = task("task-without-decision", "Task without decision");

    await renderTask(detailResponse(taskWithDecision, [openDecision("decision-open", taskWithDecision.id)]));
    expect(screen.getByTestId("task-answer-form")).not.toBeNull();

    cleanup();

    await renderTask(detailResponse(taskWithoutDecision));
    expect(screen.queryByTestId("task-answer-form")).toBeNull();
  });

  it("exposes a change-assumption button for a history row", async () => {
    const taskData = task("task-history", "Task history");
    await renderTask(detailResponse(taskData, [], [historyEntry("decision-history", taskData.id)]));

    expect(screen.getByRole("button", { name: "goal.history.changeAssumption" })).not.toBeNull();
  });

  it("shows descriptions only when present and always shows task attributes", async () => {
    const taskWithDescription = task(
      "task-with-description",
      "Task with description",
      "Describe the work and its prerequisites.",
    );
    const taskWithoutDescription = task("task-without-description", "Task without description");

    await renderTask(detailResponse(taskWithDescription));
    expect(screen.getByText("task.detail.description")).not.toBeNull();
    expect(screen.getByText("Describe the work and its prerequisites.")).not.toBeNull();
    expect(screen.getByText("task.detail.attributes")).not.toBeNull();

    cleanup();

    await renderTask(detailResponse(taskWithoutDescription));
    expect(screen.queryByText("task.detail.description")).toBeNull();
    expect(screen.getByText("task.detail.attributes")).not.toBeNull();
  });

  it("omits the description section for whitespace-only descriptions", async () => {
    const taskData = task("task-whitespace-description", "Task with whitespace description", " \n\t ");
    await renderTask(detailResponse(taskData));

    expect(screen.queryByText("task.detail.description")).toBeNull();
    expect(screen.getByText("task.detail.attributes")).not.toBeNull();
  });

  it("keeps answer input when a decision event arrives while answering", async () => {
    let decisionEvent: Parameters<typeof subscribeToDecisionEvents>[0] | undefined;
    vi.mocked(subscribeToDecisionEvents).mockImplementation((callback) => {
      decisionEvent = callback;
      return () => undefined;
    });
    const taskData = task("task-answer", "Task answer");
    await renderTask(detailResponse(taskData, [openDecision("decision-open", taskData.id)]));

    const answer = screen.getByRole("textbox");
    fireEvent.change(answer, { target: { value: "keep this answer" } });
    act(() => decisionEvent?.("decision.created"));

    expect((answer as HTMLTextAreaElement).value).toBe("keep this answer");
    expect(fetchTask).toHaveBeenCalledTimes(1);
    expect(screen.getByText("state.updateAvailable")).not.toBeNull();
  });

  it("shows an update banner without reloading when an event arrives without answer input", async () => {
    let decisionEvent: Parameters<typeof subscribeToDecisionEvents>[0] | undefined;
    vi.mocked(subscribeToDecisionEvents).mockImplementation((callback) => {
      decisionEvent = callback;
      return () => undefined;
    });
    const taskData = task("task-no-answer", "Task without answer");
    await renderTask(detailResponse(taskData, [openDecision("decision-open", taskData.id)]));

    act(() => decisionEvent?.("decision.created"));
    expect(fetchTask).toHaveBeenCalledTimes(1);
    expect(screen.getByText("state.updateAvailable")).not.toBeNull();
  });

  it("shows an update banner without reloading when answer input contains whitespace", async () => {
    let decisionEvent: Parameters<typeof subscribeToDecisionEvents>[0] | undefined;
    vi.mocked(subscribeToDecisionEvents).mockImplementation((callback) => {
      decisionEvent = callback;
      return () => undefined;
    });
    const taskData = task("task-whitespace-answer", "Task whitespace answer");
    await renderTask(detailResponse(taskData, [openDecision("decision-open", taskData.id)]));

    const answer = screen.getByRole("textbox");
    fireEvent.change(answer, { target: { value: " \n\t " } });
    act(() => decisionEvent?.("decision.created"));
    expect(fetchTask).toHaveBeenCalledTimes(1);
    expect(screen.getByText("state.updateAvailable")).not.toBeNull();
  });

  it("snoozes for one day from the current time", async () => {
    const now = Date.parse("2026-08-27T00:00:00Z");
    const nowSpy = vi.spyOn(Date, "now").mockReturnValue(now);
    const taskData = task("task-one-day", "Task one day");
    vi.mocked(snoozeTask).mockResolvedValue(taskData as Awaited<ReturnType<typeof snoozeTask>>);
    await renderTask(detailResponse(taskData));

    fireEvent.click(screen.getByRole("button", { name: "task.snooze.oneDay" }));

    await waitFor(() => expect(snoozeTask).toHaveBeenCalledTimes(1));
    const until = vi.mocked(snoozeTask).mock.calls[0]?.[1];
    expect(until).not.toBeNull();
    expect(Date.parse(until as string)).toBe(now + 24 * 60 * 60 * 1000);
    nowSpy.mockRestore();
  });

  it("snoozes for one week from the current time", async () => {
    const now = Date.parse("2026-08-27T00:00:00Z");
    const nowSpy = vi.spyOn(Date, "now").mockReturnValue(now);
    const taskData = task("task-one-week", "Task one week");
    vi.mocked(snoozeTask).mockResolvedValue(taskData as Awaited<ReturnType<typeof snoozeTask>>);
    await renderTask(detailResponse(taskData));

    fireEvent.click(screen.getByRole("button", { name: "task.snooze.oneWeek" }));

    await waitFor(() => expect(snoozeTask).toHaveBeenCalledTimes(1));
    const until = vi.mocked(snoozeTask).mock.calls[0]?.[1];
    expect(until).not.toBeNull();
    expect(Date.parse(until as string)).toBe(now + 7 * 24 * 60 * 60 * 1000);
    nowSpy.mockRestore();
  });

  it("snoozes until the local end of a selected date", async () => {
    const taskData = task("task-date", "Task date");
    vi.mocked(snoozeTask).mockResolvedValue(taskData as Awaited<ReturnType<typeof snoozeTask>>);
    await renderTask(detailResponse(taskData));

    fireEvent.change(screen.getByLabelText("task.snooze.date"), { target: { value: "2026-09-03" } });
    fireEvent.click(screen.getByRole("button", { name: "task.snooze.submit" }));

    const expectedUntil = new Date("2026-09-03T23:59:59.999").toISOString();
    await waitFor(() => expect(snoozeTask).toHaveBeenCalledWith("task-date", expectedUntil));
  });

  it("clears snooze when release is pressed", async () => {
    const taskData = task("task-release-snooze", "Task release snooze");
    vi.mocked(snoozeTask).mockResolvedValue(taskData as Awaited<ReturnType<typeof snoozeTask>>);
    await renderTask(detailResponse(taskData));

    fireEvent.click(screen.getByRole("button", { name: "task.snooze.release" }));

    await waitFor(() => expect(snoozeTask).toHaveBeenCalledWith("task-release-snooze", null));
  });

  it("shows an active snooze with its deadline and hides expired snoozes", async () => {
    const future = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
    const past = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();
    const activeTask = task("task-active-snooze", "Task active snooze", "", future);
    const expiredTask = task("task-expired-snooze", "Task expired snooze", "", past);

    await renderTask(detailResponse(activeTask));
    expect(screen.getByText(/task\.snooze\.active:/)).toBeTruthy();

    cleanup();
    await renderTask(detailResponse(expiredTask));
    expect(screen.queryByText(/task\.snooze\.active:/)).toBeNull();
  });

  it("shows snooze errors in an alert", async () => {
    const taskData = task("task-snooze-error", "Task snooze error");
    vi.mocked(snoozeTask).mockRejectedValue(new Error("snooze failed"));
    await renderTask(detailResponse(taskData));

    fireEvent.click(screen.getByRole("button", { name: "task.snooze.oneDay" }));

    expect((await screen.findByRole("alert")).textContent).toContain("snooze failed");
  });
});
