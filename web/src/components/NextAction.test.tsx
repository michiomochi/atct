import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { NextAction as NextActionValue } from "../lib/api";
import { NextAction } from "./NextAction";

const i18nMock = vi.hoisted(() => ({
  t: (key: string, options?: { count?: number }) =>
    options?.count === undefined ? key : `${key}:${options.count}`,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

afterEach(() => {
  cleanup();
});

function renderText(action?: NextActionValue): string {
  const { container } = render(<NextAction action={action} />);
  return container.textContent ?? "";
}

describe("NextAction", () => {
  it("renders the actor label before the step label", () => {
    expect(renderText({ code: "executor_implement", actor: "executor" })).toBe(
      "nextAction.actor.executor · nextAction.code.executor_implement",
    );
  });

  it("renders the human actor for a human-review step", () => {
    expect(renderText({ code: "human_decision", actor: "human" })).toBe(
      "nextAction.actor.human · nextAction.code.human_decision",
    );
  });

  it("renders only the step label when the step has no actor", () => {
    expect(renderText({ code: "done", actor: "" })).toBe("nextAction.code.done");
  });

  it("appends the task count when the step reports one", () => {
    expect(renderText({ code: "subcommander_delegate_task", actor: "subcommander", task_count: 3 })).toBe(
      "nextAction.actor.subcommander · nextAction.code.subcommander_delegate_task (nextAction.taskCount:3)",
    );
  });

  it("shows a zero task count rather than hiding it", () => {
    expect(renderText({ code: "subcommander_create_tasks", actor: "subcommander", task_count: 0 })).toContain(
      "(nextAction.taskCount:0)",
    );
  });

  it("falls back to the raw code for a step with no label", () => {
    expect(renderText({ code: "future_step", actor: "" })).toBe("future_step");
  });

  it("renders nothing when the goal has no next action", () => {
    expect(renderText(undefined)).toBe("");
  });

  it("renders nothing when the next action has no code", () => {
    expect(renderText({ code: "", actor: "executor" })).toBe("");
  });

  it("renders plain text with no link or button, since it is read-only", () => {
    const { container } = render(
      <NextAction action={{ code: "commander_review_goal", actor: "commander" }} />,
    );

    expect(container.querySelector("a, button")).toBeNull();
  });
});
