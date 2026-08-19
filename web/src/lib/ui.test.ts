import { describe, expect, it } from "vitest";
import decisionFormSource from "../components/DecisionAnswerForm.tsx?raw";
import decisionTableSource from "../components/DecisionTable.tsx?raw";
import goalCreateFormSource from "../components/GoalCreateForm.tsx?raw";
import goalDetailSource from "../components/GoalDetail.tsx?raw";
import goalTableSource from "../components/GoalTable.tsx?raw";
import dashboardSource from "../components/Dashboard.tsx?raw";
import localeSwitchSource from "../components/LocaleSwitch.tsx?raw";
import needsDecisionSource from "../components/NeedsDecisionList.tsx?raw";
import sectionSource from "../components/Section.tsx?raw";
import stateMessageSource from "../components/StateMessage.tsx?raw";
import taskTableSource from "../components/TaskTable.tsx?raw";
import taskDetailModalSource from "../components/TaskDetailModal.tsx?raw";
import attentionTaskSource from "../components/AttentionTaskTable.tsx?raw";
import { formatDateTime, formatDuration, type Locale } from "../i18n";
import type { Goal } from "./api";
import {
  DECISION_EVENT_NAMES,
  decisionAutoSettlementSeconds,
  decisionKindLabel,
  decisionRecommendationLabel,
  decisionSettlementLabel,
  filterDecisionsByTask,
  findOpenCompletion,
  hasCompletionReport,
  isDecisionEventName,
  resolveGoalID,
  statusLabel,
  validateAnswer,
  groupGoalsByProject,
} from "./ui";

function fixtureGoal(id: string, projectName: string): Goal {
  return {
    id,
    project_id: projectName.toLowerCase(),
    project_name: projectName,
    title: id,
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
    tasks: [],
  };
}

describe("groupGoalsByProject", () => {
  it("sorts project names and keeps each project's goals together", () => {
    const alphaGoal = fixtureGoal("alpha-1", "Alpha");
    const zetaGoal = fixtureGoal("zeta-1", "Zeta");
    const zetaFollowUp = fixtureGoal("zeta-2", "Zeta");

    expect(groupGoalsByProject([zetaGoal, alphaGoal, zetaFollowUp])).toEqual([
      ["Alpha", [alphaGoal]],
      ["Zeta", [zetaGoal, zetaFollowUp]],
    ]);
  });
});

describe("goal detail helpers", () => {
  it("reports whether any of the six completion fields is filled", () => {
    const emptyReport = {
      work_done: "",
      now_possible: "  ",
      how_to_verify: "",
      surprises: "",
      needs_review: "",
      next_steps: "",
    };

    expect(hasCompletionReport(emptyReport)).toBe(false);
    expect(hasCompletionReport({ ...emptyReport, needs_review: "Check the migration" })).toBe(true);
  });

  it("keeps only decisions belonging to the requested task", () => {
    const taskOneDecision = { id: "decision-1", task_id: "task-1" };
    const taskTwoDecision = { id: "decision-2", task_id: "task-2" };

    expect(filterDecisionsByTask([taskOneDecision, taskTwoDecision], "task-1")).toEqual([taskOneDecision]);
    expect(filterDecisionsByTask([taskOneDecision, taskTwoDecision], "task-2")).toEqual([taskTwoDecision]);
    expect(filterDecisionsByTask([taskOneDecision, taskTwoDecision], "task-3")).toEqual([]);
  });

  it("does not include other-task or unattached decisions", () => {
    const taskOneDecision = { id: "decision-1", task_id: "task-1" };
    const taskTwoDecision = { id: "decision-2", task_id: "task-2" };
    const unattachedDecision = { id: "decision-unattached", task_id: "" };
    const decisions = [taskOneDecision, taskTwoDecision, unattachedDecision];

    expect(filterDecisionsByTask(decisions, "task-1")).toEqual([taskOneDecision]);
    expect(filterDecisionsByTask(decisions, "")).toEqual([]);
  });
});

describe("formatDuration", () => {
  it.each([
    [0, "en", "-"],
    [45, "en", "45s"],
    [60, "en", "1m"],
    [135, "en", "2m"],
    [3600, "en", "1h 0m"],
    [8115, "en", "2h 15m"],
    [90061, "en", "25h 1m"],
    [42, "ja", "42\u79d2"],
    [135, "ja", "2\u5206"],
    [3600, "ja", "1\u6642\u95930\u5206"],
    [8115, "ja", "2\u6642\u959315\u5206"],
    [90061, "ja", "25\u6642\u95931\u5206"],
  ] as [number, Locale, string][]) ("formats %i seconds in %s as %s", (seconds, locale, expected) => {
    expect(formatDuration(locale, seconds)).toBe(expected);
  });
});

describe("validateAnswer", () => {
  it("requires at least one of label and text", () => {
    expect(validateAnswer({ answer_label: "  ", answer_text: "" })).toEqual({
      answer_label: "Enter a label or answer text.",
      answer_text: "Enter a label or answer text.",
    });
  });

  it("accepts a label or answer text", () => {
    expect(validateAnswer({ answer_label: "approve", answer_text: "" })).toEqual({});
    expect(validateAnswer({ answer_label: "", answer_text: "Reason" })).toEqual({});
  });
});

describe("localized UI labels", () => {
  it("formats dates for both supported locales", () => {
    const iso = "2026-08-15T00:00:00Z";
    expect(formatDateTime("en", iso)).toContain("Aug");
    expect(formatDateTime("ja", iso)).toContain("2026");
    expect(formatDateTime("en", iso)).not.toBe(formatDateTime("ja", iso));
  });

  it("returns invalid dates unchanged", () => {
    expect(formatDateTime("en", "not-a-date")).toBe("not-a-date");
  });

  it.each([
    ["en", "todo", "Not started"],
    ["en", "doing", "In progress"],
    ["en", "done", "Completed"],
    ["en", "blocked", "Blocked"],
    ["en", "open", "Awaiting answer"],
    ["en", "answered", "Answered"],
    ["en", "applied", "Applied"],
    ["en", "approved", "Approved"],
    ["en", "rejected", "Rejected"],
    ["en", "withdrawn", "Withdrawn"],
    ["en", "active", "In progress"],
    ["en", "completed", "Completed"],
    ["ja", "todo", "\u672a\u7740\u624b"],
    ["ja", "doing", "\u9032\u884c\u4e2d"],
    ["ja", "done", "\u5b8c\u4e86"],
    ["ja", "blocked", "\u30d6\u30ed\u30c3\u30af"],
    ["ja", "open", "\u56de\u7b54\u5f85\u3061"],
    ["ja", "answered", "\u56de\u7b54\u6e08\u307f"],
    ["ja", "applied", "\u9069\u7528\u6e08\u307f"],
    ["ja", "approved", "\u627f\u8a8d\u6e08\u307f"],
    ["ja", "rejected", "\u5374\u4e0b\u6e08\u307f"],
    ["ja", "withdrawn", "\u53d6\u308a\u4e0b\u3052\u6e08\u307f"],
    ["ja", "active", "\u9032\u884c\u4e2d"],
    ["ja", "completed", "\u5b8c\u4e86"],
  ])("labels %s/%s as %s", (locale, status, expected) => {
    expect(statusLabel(locale as Locale, status)).toBe(expected);
  });

  it("translates dropped task statuses", () => {
    expect(statusLabel("ja", "dropped")).toBe("取り下げ");
    expect(statusLabel("ja", "dropped")).not.toBe("dropped");
  });

  it.each([
    ["en", "decision", "Decision"],
    ["en", "completion", "Completion"],
    ["ja", "decision", "\u5224\u65ad"],
    ["ja", "completion", "\u5b8c\u4e86"],
  ])("labels %s kind %s as %s", (locale, kind, expected) => {
    expect(decisionKindLabel(locale as Locale, kind)).toBe(expected);
  });

  it("labels decisions settled by the default after a timeout", () => {
    expect(decisionSettlementLabel("en", true)).toBe("Settled by default after timeout");
    expect(decisionSettlementLabel("ja", true)).toBe("期限切れのため既定値で確定");
    expect(decisionSettlementLabel("en", false)).toBeUndefined();
  });

  it("marks the option matching the AI recommendation", () => {
    expect(decisionRecommendationLabel("en", "A", "A")).toBe("AI recommendation");
    expect(decisionFormSource).toContain("decisionRecommendationLabel");
  });

  it("does not mark an option when there is no recommendation", () => {
    expect(decisionRecommendationLabel("en", "", "A")).toBeUndefined();
    expect(decisionRecommendationLabel("en", "A", "B")).toBeUndefined();
  });

  it("formats an automatic settlement deadline in human-readable units", () => {
    const seconds = decisionAutoSettlementSeconds(1_800_000);
    expect(seconds).toBe(1_800);
    expect(formatDuration("en", seconds ?? 0)).toBe("30m");
    expect(formatDuration("ja", seconds ?? 0)).toBe("30分");
    expect(decisionAutoSettlementSeconds(undefined)).toBeUndefined();
    expect(decisionFormSource).toContain("decision.autoSettlesIn");
    expect(decisionTableSource).toContain("decisionAutoSettlementSeconds");
  });
});

describe("localized date and duration renderers", () => {
  it("routes timestamps and claims through the shared locale formatters", () => {
    const dateSources = [goalTableSource, decisionTableSource];
    const durationSources = [taskTableSource, attentionTaskSource, needsDecisionSource];

    for (const source of dateSources) {
      expect(source).toContain("formatDateTime");
      expect(source).not.toContain("formatDate(");
    }
    for (const source of durationSources) {
      expect(source).toContain("formatDuration");
      expect(source).not.toContain("formatHeldFor(");
    }
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
  it("renders all completion report fields and supports legacy summaries", () => {
    for (const field of [
      "work_done",
      "now_possible",
      "how_to_verify",
      "surprises",
      "needs_review",
      "next_steps",
    ]) {
      expect(goalDetailSource).toContain(`goal.${field}`);
    }

    for (const key of [
      "goal.completion.report.workDone",
      "goal.completion.report.nowPossible",
      "goal.completion.report.howToVerify",
      "goal.completion.report.surprises",
      "goal.completion.report.needsReview",
      "goal.completion.report.nextSteps",
    ]) {
      expect(goalDetailSource).toContain(key);
    }

    expect(goalDetailSource).toContain("result_summary");
    expect(goalDetailSource).toContain("min-w-0");
    expect(goalDetailSource).toContain("break-words");
  });

  it("finds only an open completion decision", () => {
    const completion = { id: "completion-1", kind: "completion", status: "open" };
    expect(findOpenCompletion([
      { id: "answered-1", kind: "completion", status: "answered" },
      completion,
      { id: "ordinary-1", kind: "decision", status: "open" },
    ])).toBe(completion);
    expect(findOpenCompletion([{ id: "done-1", kind: "completion", status: "applied" }])).toBeUndefined();
  });

  it("renders one ordered task list and moves decisions into task details", () => {
    expect(goalDetailSource).not.toContain("NeedsDecisionList");
    expect(goalDetailSource).not.toContain("xl:grid-cols-3");
    expect(goalDetailSource).toContain("const tasks = data?.goal.goal.tasks ?? [];");
    expect(goalDetailSource).toContain("tasks={tasks}");
    expect(goalDetailSource).toContain('mode="goal"');
    expect(goalDetailSource).toContain("decisionHistory={data.goal.decision_history}");
    expect(taskTableSource).toContain('mode: "now" | "needs_decision" | "next" | "goal"');
    expect(taskTableSource).toContain("left.order - right.order");
    expect(taskDetailModalSource).toContain("DialogRoot");
    expect(taskDetailModalSource).toContain("DialogTrigger");
    expect(taskDetailModalSource).toContain("DialogClose");
    expect(taskDetailModalSource).toContain("filterDecisionsByTask");
    expect(taskDetailModalSource).toContain("taskOpenDecisions.length > 0");
    expect(needsDecisionSource).toContain('data-testid="needs-decision-list"');
    expect(needsDecisionSource).not.toContain("<Table");
    expect(needsDecisionSource).not.toContain("table-scroll");
    expect(needsDecisionSource).not.toContain("bg-surface p-4");
  });

  it("exposes the completion approval API in Goal detail", () => {
    expect(goalDetailSource).toContain("fetchGoal");
    expect(goalDetailSource).toContain("unattached_decisions");
    expect(goalDetailSource).toContain("approveCompletion");
    expect(goalDetailSource).toContain("rejectCompletion");
    expect(goalDetailSource).toContain('t("goal.completion.title")');
    expect(goalDetailSource).toContain("result_summary");
  });

  it("keys the remaining Goal detail framing strings", () => {
    expect(goalDetailSource).toContain("useTranslation");
    expect(goalDetailSource).toContain('t("goal.tasks.title")');
    expect(goalDetailSource).toContain('t("goal.column.status")');
    expect(goalDetailSource).toContain('t("goal.column.updatedAt")');
    expect(taskTableSource).toContain('t("task.claim.release")');
    expect(taskTableSource).toContain('t("duration.none")');
    expect(decisionFormSource).toContain('t("form.answer.submit")');
  });

  it("localizes the Decision kind and header Dashboard link", () => {
    expect(decisionTableSource).toContain("decisionKindLabel");
    expect(localeSwitchSource).toContain('t("nav.dashboard")');
    expect(localeSwitchSource).toContain('href="/"');
  });

  it("applies the stored locale after the client islands hydrate", () => {
    expect(localeSwitchSource).toContain("readStoredLocale");
    expect(localeSwitchSource).toContain("resolveLocale");
    expect(localeSwitchSource).toContain("document.readyState");
    expect(localeSwitchSource).toContain("setTimeout");
    expect(dashboardSource).not.toContain("readStoredLocale");
    expect(dashboardSource).not.toContain("changeLanguage");
    expect(goalDetailSource).not.toContain("readStoredLocale");
    expect(goalDetailSource).not.toContain("changeLanguage");
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

  it("uses translated dashboard framing with stable section anchors", () => {
    expect(dashboardSource).toContain("useTranslation");
    expect(dashboardSource).toContain('id="open-decisions"');
    expect(dashboardSource).toContain('id="active-goals"');
    expect(dashboardSource).toContain('t("dashboard.projects.empty")');
    expect(sectionSource).toContain("id: string");
    expect(sectionSource).toContain("aria-labelledby={`${id}-heading`}");
  });
});
