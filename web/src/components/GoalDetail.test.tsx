import { within } from "@testing-library/dom";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Decision, Goal, GoalResponse, InboxResponse, TaskCommit, TaskView } from "../lib/api";
import {
  approveDecision,
  fetchGoal,
  fetchInbox,
  fetchTaskCommitDiff,
  subscribeToDecisionEvents,
  withdrawGoal,
} from "../lib/api";
import { Dashboard } from "./Dashboard";
import { GoalDetail } from "./GoalDetail";

const i18nMock = vi.hoisted(() => ({
  t: (key: string) => key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

const apiMock = vi.hoisted(() => ({
  approveDecision: vi.fn(),
  answerDecision: vi.fn(),
  createGoal: vi.fn(),
  fetchGoal: vi.fn(),
  fetchInbox: vi.fn(),
  fetchTaskCommitDiff: vi.fn(),
  rejectDecision: vi.fn(),
  reviseDecision: vi.fn(),
  subscribeToDecisionEvents: vi.fn(() => () => undefined),
  withdrawGoal: vi.fn(),
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
    content: "Fixture goal",
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
    task_commits: [],
  };
}

function taskCommit(sha: string, subject: string): TaskCommit {
  return {
    sha,
    short_sha: sha.slice(0, 7),
    subject,
    files_changed: 1,
    insertions: 2,
    deletions: 0,
    in_history: true,
    created_at: "2026-08-20T00:00:00Z",
  };
}

function goalResponseWithTaskCommits(
  task_commits: Array<{ task_id: string; task_title: string; commits: TaskCommit[] }>,
): GoalResponse {
  return { ...goalResponse(), task_commits } as GoalResponse;
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
    goal_headline: "Fixture goal",
    kind: "completion",
    question: "Review the completion",
    options: [],
    status: "open",
    agent_session_id: "fixture-run",
    created_at: "2026-08-20T00:00:00Z",
  };
}

function goalApprovalDecision(): Decision {
  return {
    id: "goal-approval-1",
    goal_id: "goal-1",
    goal_headline: "Fixture goal",
    kind: "goal_approval",
    question: "Approve the proposed goal",
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
    proposed_goals: [],
    attention_tasks: [],
  };
}

describe("GoalDetail", () => {
  it("does not render goal approval for an active goal", async () => {
    const response = goalResponse({ status: "active" });
    response.unattached_decisions = [goalApprovalDecision()];
    vi.mocked(fetchGoal).mockResolvedValueOnce(response);

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(fetchGoal).toHaveBeenCalledWith("goal-1"));
    expect(screen.queryByTestId("goal-approval")).toBeNull();
  });

  it("does not render goal approval for a done goal", async () => {
    const response = goalResponse({ status: "done" });
    response.unattached_decisions = [goalApprovalDecision()];
    vi.mocked(fetchGoal).mockResolvedValueOnce(response);

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(fetchGoal).toHaveBeenCalledWith("goal-1"));
    expect(screen.queryByTestId("goal-approval")).toBeNull();
  });

  it("disables goal approval rejection while the reason is empty", async () => {
    const response = goalResponse({ status: "proposed" });
    response.unattached_decisions = [goalApprovalDecision()];
    vi.mocked(fetchGoal).mockResolvedValue(response);

    render(<GoalDetail id="goal-1" />);

    const approval = () => within(screen.getByTestId("goal-approval"));
    await waitFor(() => expect(approval().getByRole("textbox")).not.toBeNull());
    expect((approval().getByRole("button", { name: "goal.approval.reject" }) as HTMLButtonElement).disabled).toBe(true);
    expect((approval().getByRole("button", { name: "goal.approval.approve" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("approves a goal with its decision ID", async () => {
    const response = goalResponse({ status: "proposed" });
    response.unattached_decisions = [goalApprovalDecision()];
    vi.mocked(fetchGoal).mockResolvedValue(response);
    vi.mocked(approveDecision).mockResolvedValueOnce(goal({ status: "active" }));

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByTestId("goal-approval")).not.toBeNull());
    fireEvent.click(screen.getByRole("button", { name: "goal.approval.approve" }));

    await waitFor(() => expect(approveDecision).toHaveBeenCalledWith("goal-approval-1"));
  });

  it("shows the server message when goal approval returns a conflict", async () => {
    const serverMessage = "goal goal-1 was already reviewed";
    const serverError = Object.assign(new Error(serverMessage), { status: 409 });
    const response = goalResponse({ status: "proposed" });
    response.unattached_decisions = [goalApprovalDecision()];
    vi.mocked(fetchGoal).mockResolvedValue(response);
    vi.mocked(approveDecision).mockRejectedValueOnce(serverError);

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByTestId("goal-approval")).not.toBeNull());
    fireEvent.click(screen.getByRole("button", { name: "goal.approval.approve" }));

    await waitFor(() => expect(screen.getByText(serverMessage)).not.toBeNull());
  });

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

    const completionApproval = () => within(screen.getByTestId("completion-approval"));
    await waitFor(() => expect(completionApproval().getByRole("textbox")).not.toBeNull());
    const reason = completionApproval().getByRole("textbox");
    fireEvent.change(reason, { target: { value: "keep this reason" } });
    act(() => decisionEvent?.("decision.created"));

    expect((reason as HTMLTextAreaElement).value).toBe("keep this reason");
    expect(fetchGoal).toHaveBeenCalledTimes(1);
    expect(screen.getByText("state.updateAvailable")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "state.fetchLatest" }));
    await waitFor(() => expect(fetchGoal).toHaveBeenCalledTimes(2));
    await waitFor(() => expect((completionApproval().getByRole("textbox") as HTMLTextAreaElement).value).toBe(""));

    act(() => decisionEvent?.("decision.created"));
    await waitFor(() => expect(fetchGoal).toHaveBeenCalledTimes(3));
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

  it("renders all commits from all goal tasks", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(
      goalResponseWithTaskCommits([
        {
          task_id: "task-1",
          task_title: "First task",
          commits: [taskCommit("111111111111111", "First task first commit"), taskCommit("111111122222222", "First task second commit")],
        },
        {
          task_id: "task-2",
          task_title: "Second task",
          commits: [taskCommit("222222211111111", "Second task first commit"), taskCommit("222222222222222", "Second task second commit")],
        },
      ]),
    );

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByRole("heading", { name: "goal.commits.title" })).not.toBeNull());
    expect(screen.getByRole("link", { name: "First task" })).not.toBeNull();
    expect(screen.getByRole("link", { name: "Second task" })).not.toBeNull();
    expect(screen.getByText("First task first commit")).not.toBeNull();
    expect(screen.getByText("First task second commit")).not.toBeNull();
    expect(screen.getByText("Second task first commit")).not.toBeNull();
    expect(screen.getByText("Second task second commit")).not.toBeNull();
  });

  it("does not render the goal commits section when task_commits is empty", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(goalResponseWithTaskCommits([]));

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(fetchGoal).toHaveBeenCalledWith("goal-1"));
    expect(screen.queryByRole("heading", { name: "goal.commits.title" })).toBeNull();
  });

  it("does not fetch a goal task commit diff until details are opened", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(
      goalResponseWithTaskCommits([
        {
          task_id: "task-1",
          task_title: "First task",
          commits: [taskCommit("111111111111111", "First task commit")],
        },
      ]),
    );

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByText("First task commit")).not.toBeNull());
    expect(fetchTaskCommitDiff).not.toHaveBeenCalled();
  });

  it("fetches a second goal task commit diff with its owning task ID", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(
      goalResponseWithTaskCommits([
        {
          task_id: "task-1",
          task_title: "First task",
          commits: [taskCommit("111111111111111", "First task commit")],
        },
        {
          task_id: "task-2",
          task_title: "Second task",
          commits: [taskCommit("222222222222222", "Second task commit")],
        },
      ]),
    );
    vi.mocked(fetchTaskCommitDiff).mockResolvedValueOnce({
      sha: "222222222222222",
      in_history: true,
      files: [],
      body: "second diff",
      omitted_lines: 0,
    });

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByText("Second task commit")).not.toBeNull());
    fireEvent.click(screen.getAllByText("task.detail.commitDiff")[1]);

    await waitFor(() => expect(fetchTaskCommitDiff).toHaveBeenCalledWith("task-2", "222222222222222"));
  });

  it("disables goal withdrawal while the reason is empty or whitespace", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(goalResponse());

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByTestId("goal-withdraw")).not.toBeNull());
    const reason = screen.getByRole("textbox", { name: "goal.withdraw.reason" });
    const submit = screen.getByRole("button", { name: "goal.withdraw.submit" });

    expect((submit as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(reason, { target: { value: "   " } });
    expect((submit as HTMLButtonElement).disabled).toBe(true);
  });

  it("does not render goal withdrawal for a proposed goal", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(goalResponse({ status: "proposed" }));

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(fetchGoal).toHaveBeenCalledWith("goal-1"));
    expect(screen.queryByTestId("goal-withdraw")).toBeNull();
  });

  it("does not render goal withdrawal for a done goal", async () => {
    vi.mocked(fetchGoal).mockResolvedValueOnce(goalResponse({ status: "done" }));

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(fetchGoal).toHaveBeenCalledWith("goal-1"));
    expect(screen.queryByTestId("goal-withdraw")).toBeNull();
  });

  it("shows the server message when goal withdrawal returns a conflict", async () => {
    const serverMessage = "goal goal-1 is dropped, not active";
    const serverError = Object.assign(new Error(serverMessage), { status: 409 });
    vi.mocked(fetchGoal).mockResolvedValueOnce(goalResponse());
    vi.mocked(withdrawGoal).mockRejectedValueOnce(serverError);

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByTestId("goal-withdraw")).not.toBeNull());
    fireEvent.change(screen.getByRole("textbox", { name: "goal.withdraw.reason" }), {
      target: { value: "No longer needed" },
    });
    fireEvent.click(screen.getByRole("button", { name: "goal.withdraw.submit" }));

    await waitFor(() => expect(screen.getByText(serverMessage)).not.toBeNull());
  });

  it("withdraws the goal with the provided reason and reloads after success", async () => {
    vi.mocked(fetchGoal)
      .mockResolvedValueOnce(goalResponse())
      .mockResolvedValueOnce(goalResponse({ status: "dropped" }));
    vi.mocked(withdrawGoal).mockResolvedValueOnce(goal());

    render(<GoalDetail id="goal-1" />);

    await waitFor(() => expect(screen.getByTestId("goal-withdraw")).not.toBeNull());
    fireEvent.change(screen.getByRole("textbox", { name: "goal.withdraw.reason" }), {
      target: { value: "  No longer needed  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "goal.withdraw.submit" }));

    await waitFor(() => expect(withdrawGoal).toHaveBeenCalledWith("goal-1", "No longer needed"));
    await waitFor(() => expect(fetchGoal).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByTestId("goal-withdraw")).toBeNull());
  });
});
