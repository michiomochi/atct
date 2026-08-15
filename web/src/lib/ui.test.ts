import { describe, expect, it } from "vitest";
import {
  DECISION_EVENT_NAMES,
  formatHeldFor,
  isDecisionEventName,
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
      answered_by: "回答者を入力してください。",
    });
  });

  it("requires at least one of label and text", () => {
    expect(validateAnswer({ answer_label: "  ", answer_text: "", answered_by: "michio" })).toEqual({
      answer_label: "ラベルまたは回答文を入力してください。",
      answer_text: "ラベルまたは回答文を入力してください。",
    });
  });

  it("accepts a label or answer text", () => {
    expect(validateAnswer({ answer_label: "approve", answer_text: "", answered_by: "michio" })).toEqual({});
    expect(validateAnswer({ answer_label: "", answer_text: "理由", answered_by: "michio" })).toEqual({});
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
