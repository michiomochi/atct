import { describe, expect, it } from "vitest";
import {
  DECISION_EVENT_NAMES,
  formatDate,
  formatHeldFor,
  isDecisionEventName,
  statusLabel,
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
