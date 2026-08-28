import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GoalDiff } from "./GoalDiff";

const apiMock = vi.hoisted(() => ({
  approveDecision: vi.fn(),
  fetchGoal: vi.fn(),
  fetchGoalDiff: vi.fn(),
  fetchGoalDiffPatch: vi.fn(),
  rejectDecision: vi.fn(),
  subscribeToDecisionEvents: vi.fn(() => () => undefined),
  updateGoalContent: vi.fn(),
  withdrawGoal: vi.fn(),
}));

const i18nMock = vi.hoisted(() => ({
  t: (key: string, options?: { count?: number; sha?: string }) => {
    if (key === "goal.diff.unknown") return "不明";
    if (key === "goal.diff.merged" && options?.sha !== undefined) {
      return `マージ済み（${options.sha}）`;
    }
    if (key === "goal.diff.mergedUnresolved") {
      return "マージ済みですが、マージコミットを特定できませんでした。";
    }
    if (key === "goal.diff.omitted" && options?.count !== undefined) {
      return `差分が大きいので表示しません（${options.count}行）。`;
    }
    return key;
  },
  i18n: { language: "ja" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("../lib/api", () => ({
  ...apiMock,
  ApiError: class MockApiError extends Error {
    status = 0;
  },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

vi.mock("@git-diff-view/react", () => ({
  DiffView: ({ data }: { data: { hunks: string[] } }) => (
    <pre data-testid="diff-view">{data.hunks.join("")}</pre>
  ),
  DiffModeEnum: { Unified: 4 },
}));

vi.mock("@git-diff-view/react/styles/diff-view.css", () => ({}));

const availableDiff = {
  available: true,
  reason: "",
  base_ref: "main",
  branch: "wt/goal-1",
  source: "branch",
  merge_commit: "",
  files_changed: 2,
  insertions: 12,
  deletions: 4,
  files: [
    { path: "src/one.ts", insertions: 8, deletions: 2, binary: false },
    { path: "src/two.ts", insertions: 4, deletions: 2, binary: false },
  ],
};

function openDetails() {
  fireEvent.click(screen.getByText("src/one.ts"));
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("GoalDiff", () => {
  it("shows the changed-file list and totals", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue(availableDiff);

    render(<GoalDiff goalID="goal-1" />);

    const heading = await screen.findByRole("heading", { name: "goal.diff.title" });
    await screen.findByTestId("goal-diff-files-changed");
    expect(heading).not.toBeNull();
    expect(screen.getByTestId("goal-diff-files-changed").textContent).toBe("2");
    expect(screen.getByTestId("goal-diff-insertions").textContent).toBe("+12");
    expect(screen.getByTestId("goal-diff-deletions").textContent).toBe("−4");
    expect(screen.queryByText("src/one.ts")).not.toBeNull();
  });

  it("shows the merge commit marker with a shortened SHA", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({
      ...availableDiff,
      source: "merge_commit",
      merge_commit: "abcdef1234567890abcdef1234567890abcdef12",
    });

    render(<GoalDiff goalID="goal-1" />);

    const marker = await screen.findByTestId("goal-diff-merge-commit");
    expect(marker.textContent).toBe("マージ済み（abcdef1）");
  });

  it("does not show a merge commit marker for a branch diff", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue(availableDiff);

    render(<GoalDiff goalID="goal-1" />);

    await screen.findByTestId("goal-diff-files-changed");
    expect(screen.queryByTestId("goal-diff-merge-commit")).toBeNull();
  });

  it("fetches and renders a patch when a file is opened", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({ ...availableDiff, files: [availableDiff.files[0]] });
    apiMock.fetchGoalDiffPatch.mockResolvedValue({
      available: true,
      reason: "",
      base_ref: "main",
      branch: "wt/goal-1",
      path: "src/one.ts",
      patch: "diff --git a/src/one.ts b/src/one.ts\n@@ -1 +1 @@\n-old\n+new\n",
      omitted_lines: 0,
    });

    render(<GoalDiff goalID="goal-1" />);
    await screen.findByText("src/one.ts");

    openDetails();

    await waitFor(() => expect(apiMock.fetchGoalDiffPatch).toHaveBeenCalledWith("goal-1", "src/one.ts"));
    expect((await screen.findByTestId("diff-view")).textContent).toContain("+new");
  });

  it("shows an unknown state for a timed-out diff", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({
      available: false,
      reason: "timeout",
      base_ref: "main",
      branch: "wt/goal-1",
      files_changed: 0,
      insertions: 0,
      deletions: 0,
      files: [],
    });

    render(<GoalDiff goalID="goal-1" />);

    const heading = await screen.findByRole("heading", { name: "goal.diff.title" });
    const unknown = await screen.findByText("不明");
    expect(heading).not.toBeNull();
    expect(unknown).not.toBeNull();
  });

  it("does not pass an omitted patch to DiffView", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({ ...availableDiff, files: [availableDiff.files[0]] });
    apiMock.fetchGoalDiffPatch.mockResolvedValue({
      available: true,
      reason: "",
      base_ref: "main",
      branch: "wt/goal-1",
      path: "src/one.ts",
      patch: "",
      omitted_lines: 2001,
    });

    render(<GoalDiff goalID="goal-1" />);
    await screen.findByText("src/one.ts");
    openDetails();

    expect((await screen.findByText(/差分が大きいので表示しません/)).textContent).toContain("差分が大きいので表示しません");
    expect(screen.queryByTestId("diff-view")).toBeNull();
  });

  it("does not fetch a patch before its file is opened", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({ ...availableDiff, files: [availableDiff.files[0]] });

    render(<GoalDiff goalID="goal-1" />);
    await screen.findByText("src/one.ts");

    expect(apiMock.fetchGoalDiffPatch).not.toHaveBeenCalled();
  });

  it("does not render the section for a missing branch", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({
      available: false,
      reason: "no_branch",
      base_ref: "main",
      branch: "wt/goal-1",
      files_changed: 0,
      insertions: 0,
      deletions: 0,
      files: [],
    });

    render(<GoalDiff goalID="goal-1" />);

    await waitFor(() => expect(screen.queryByRole("heading", { name: "goal.diff.title" })).toBeNull());
  });

  it("keeps the section and shows a reason when a merged diff is unresolved", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({
      available: false,
      reason: "merged_unresolved",
      base_ref: "main",
      branch: "wt/goal-1",
      files_changed: 0,
      insertions: 0,
      deletions: 0,
      files: [],
    });

    render(<GoalDiff goalID="goal-1" />);

    expect(await screen.findByTestId("goal-diff")).not.toBeNull();
    expect(await screen.findByTestId("goal-diff-merged-unresolved")).not.toBeNull();
  });

  it("shows an error inside the section when loading the diff fails", async () => {
    apiMock.fetchGoalDiff.mockRejectedValue(new Error("diff failed"));

    render(<GoalDiff goalID="goal-1" />);

    const error = await screen.findByRole("alert");
    expect(error.textContent).toBe("diff failed");
    expect(screen.queryByTestId("goal-diff")).not.toBeNull();
  });

  it("fetches a file patch only once after repeated open and close", async () => {
    apiMock.fetchGoalDiff.mockResolvedValue({ ...availableDiff, files: [availableDiff.files[0]] });
    apiMock.fetchGoalDiffPatch.mockResolvedValue({
      available: true,
      reason: "",
      base_ref: "main",
      branch: "wt/goal-1",
      path: "src/one.ts",
      patch: "diff --git a/src/one.ts b/src/one.ts\n",
      omitted_lines: 0,
    });

    render(<GoalDiff goalID="goal-1" />);
    const summary = await screen.findByText("src/one.ts");

    fireEvent.click(summary);
    await waitFor(() => expect(apiMock.fetchGoalDiffPatch).toHaveBeenCalledTimes(1));
    fireEvent.click(summary);
    fireEvent.click(summary);

    await waitFor(() => expect(apiMock.fetchGoalDiffPatch).toHaveBeenCalledTimes(1));
  });
});
