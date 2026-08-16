import { describe, expect, it } from "vitest";
import decisionFormSource from "../components/DecisionAnswerForm.tsx?raw";
import goalCreateFormSource from "../components/GoalCreateForm.tsx?raw";
import goalDetailSource from "../components/GoalDetail.tsx?raw";
import inboxSource from "../components/Inbox.tsx?raw";
import needsDecisionSource from "../components/NeedsDecisionList.tsx?raw";
import sectionSource from "../components/Section.tsx?raw";
import stateMessageSource from "../components/StateMessage.tsx?raw";
import taskTableSource from "../components/TaskTable.tsx?raw";
import {
  DECISION_EVENT_NAMES,
  formatDate,
  formatHeldFor,
  findOpenCompletion,
  isDecisionEventName,
  resolveGoalID,
  statusLabel,
  validateCompletion,
  validateAnswer,
} from "./ui";

describe("formatHeldFor", () => {
  it.each([
    [0, "0s"],
    [45, "45s"],
    [60, "1m"],
    [135, "2m 15s"],
    [3600, "1h"],
    [8115, "2h 15m"],
    [90061, "1d 1h"],
  ])("formats %i seconds as %s", (seconds, expected) => {
    expect(formatHeldFor(seconds)).toBe(expected);
  });
});

describe("validateAnswer", () => {
  it("requires answered_by", () => {
    expect(validateAnswer({ answer_label: "approve", answer_text: "", answered_by: "" })).toEqual({
      answered_by: "Enter the person answering this decision.",
    });
  });

  it("requires at least one of label and text", () => {
    expect(validateAnswer({ answer_label: "  ", answer_text: "", answered_by: "michio" })).toEqual({
      answer_label: "Enter a label or answer text.",
      answer_text: "Enter a label or answer text.",
    });
  });

  it("accepts a label or answer text", () => {
    expect(validateAnswer({ answer_label: "approve", answer_text: "", answered_by: "michio" })).toEqual({});
    expect(validateAnswer({ answer_label: "", answer_text: "Reason", answered_by: "michio" })).toEqual({});
  });
});

describe("English UI labels", () => {
  it("formats dates with the English locale", () => {
    expect(formatDate("2026-08-15T00:00:00Z")).toContain("Aug");
  });

  it.each([
    ["todo", "Not started"],
    ["doing", "In progress"],
    ["done", "Completed"],
    ["blocked", "Blocked"],
    ["open", "Awaiting answer"],
    ["answered", "Answered"],
    ["applied", "Applied"],
    ["approved", "Approved"],
    ["rejected", "Rejected"],
    ["withdrawn", "Withdrawn"],
    ["active", "In progress"],
    ["completed", "Completed"],
  ])("labels %s as %s", (status, expected) => {
    expect(statusLabel(status)).toBe(expected);
  });
});

describe("decision SSE events", () => {
  it("keeps the six event names exact", () => {
    expect(DECISION_EVENT_NAMES).toEqual([
      "decision.created",
      "decision.answered",
      "decision.withdrawn",
      "decision.applied",
      "decision.approved",
      "decision.rejected",
    ]);
  });

  it("ignores unrelated event names", () => {
    expect(isDecisionEventName("decision.applied")).toBe(true);
    expect(isDecisionEventName("task.claimed")).toBe(false);
  });
});

describe("goal history IDs", () => {
  it.each([
    ["_", "/goals/abc123", "abc123"],
    ["_", "/goals/abc123/", "abc123"],
    ["_", "/goals/a%2Fb", "a/b"],
    ["known", "/goals/other", "known"],
  ])("resolves %s at %s to %s", (id, pathname, expected) => {
    expect(resolveGoalID(id, pathname)).toBe(expected);
  });
});

describe("Kumo buttons", () => {
  it("keeps button semantics and the shared focus ring", () => {
    const componentSources = [decisionFormSource, stateMessageSource, taskTableSource];

    for (const source of componentSources) {
      expect(source).toContain('from "@cloudflare/kumo/components/button"');
      expect(source).not.toContain("@base-ui/react");
      expect(source).toContain("focus-ring");
      expect(source).toContain('type="button"');
      expect(source).toContain("onClick=");
    }

    expect(decisionFormSource).toContain('type="submit"');
    expect(decisionFormSource).toContain("disabled={submitting}");
    expect(stateMessageSource).toContain("disabled:cursor-wait disabled:opacity-60");
    expect(taskTableSource).toContain("disabled={releasing}");
    expect(taskTableSource).toContain("if (!task.claimed_by)");
  });
});

describe("goal detail answer flows", () => {
  it("finds only an open completion decision", () => {
    const completion = { id: "completion-1", kind: "completion", status: "open" };
    expect(findOpenCompletion([
      { id: "answered-1", kind: "completion", status: "answered" },
      completion,
      { id: "ordinary-1", kind: "decision", status: "open" },
    ])).toBe(completion);
    expect(findOpenCompletion([{ id: "done-1", kind: "completion", status: "applied" }])).toBeUndefined();
  });

  it("requires a human name before approving or rejecting completion", () => {
    expect(validateCompletion({ answered_by: "  " })).toEqual({
      answered_by: "Enter the person approving or rejecting this completion.",
    });
    expect(validateCompletion({ answered_by: "michio" })).toEqual({});
  });

  it("keeps decision answers vertical while Now and Next stay tabular", () => {
    expect(goalDetailSource).toContain("NeedsDecisionList");
    expect(goalDetailSource).not.toContain("<TaskTable tasks={data.needs_decision}");
    expect(needsDecisionSource).toContain('data-testid="needs-decision-list"');
    expect(needsDecisionSource).not.toContain("<Table");
    expect(needsDecisionSource).not.toContain("table-scroll");
    expect(needsDecisionSource).not.toContain("bg-surface p-4");
    expect(goalDetailSource).toContain('<TaskTable tasks={data.goal.now} mode="now"');
    expect(goalDetailSource).toContain('<TaskTable tasks={data.goal.next} mode="next"');
  });

  it("exposes the completion approval API in Goal detail", () => {
    expect(goalDetailSource).toContain("fetchInbox");
    expect(goalDetailSource).toContain("approveCompletion");
    expect(goalDetailSource).toContain("rejectCompletion");
    expect(goalDetailSource).toContain('t("goal.completion.title")');
    expect(goalDetailSource).toContain("result_summary");
  });

  it("keys the remaining Goal detail framing strings", () => {
    expect(goalDetailSource).toContain("useTranslation");
    expect(goalDetailSource).toContain('t("goal.column.now")');
    expect(taskTableSource).toContain('t("task.claim.release")');
    expect(taskTableSource).toContain('t("duration.none")');
    expect(decisionFormSource).toContain('t("form.answer.submit")');
  });

  it("keeps goal creation in Active goals with all required states", () => {
    expect(goalCreateFormSource).toContain("fetchProjects");
    expect(goalCreateFormSource).toContain("createGoal");
    expect(goalCreateFormSource).toContain('t("form.goal.noProject")');
    expect(goalCreateFormSource).toContain('t("form.goal.project.placeholder")');
    expect(goalCreateFormSource).toContain('name="title"');
    expect(goalCreateFormSource).toContain('name="description"');
    expect(goalCreateFormSource).toContain("status === 409");
    expect(goalCreateFormSource).toContain('t("form.goal.action.creating")');
    expect(goalCreateFormSource).toContain("role=\"alert\"");
  });

  it("uses translated inbox framing with stable section anchors", () => {
    expect(inboxSource).toContain("useTranslation");
    expect(inboxSource).toContain('id="open-decisions"');
    expect(inboxSource).toContain('id="unapplied-decisions"');
    expect(inboxSource).toContain('id="attention-tasks"');
    expect(inboxSource).toContain('id="active-goals"');
    expect(sectionSource).toContain("id: string");
    expect(sectionSource).toContain("aria-labelledby={`${id}-heading`}");
  });
});
