import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DecisionHistoryEntry } from "../lib/api";
import { DecisionHistoryTable } from "./DecisionHistoryTable";

const i18nMock = vi.hoisted(() => ({
  t: (key: string) => key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: i18nMock.t, i18n: i18nMock.i18n }),
  initReactI18next: i18nMock.initReactI18next,
}));

function entry(overrides: Partial<DecisionHistoryEntry>): DecisionHistoryEntry {
  return {
    decision_id: "1",
    task_id: "",
    question: "Which option?",
    answer_label: "A",
    answer_text: "",
    settled_by_default: false,
    answered_at: "2026-10-10T06:00:00Z",
    applied_at: "2026-10-10T06:00:01Z",
    ...overrides,
  };
}

describe("DecisionHistoryTable chat answers", () => {
  afterEach(() => {
    cleanup();
  });

  it("shows the chat badge, the quote and the full evidence for a chat answer", () => {
    render(
      <DecisionHistoryTable
        omittedCount={0}
        decisions={[
          entry({
            decision_id: "7",
            answered_via: "chat",
            answer_quote: "Aで進めてください",
            answer_evidence: "はい、Aで進めてください。\n理由も続けます。",
          }),
        ]}
      />,
    );

    const row = screen.getByText("Which option?").closest("tr");
    if (!row) throw new Error("row not found");
    expect(within(row).getByText("goal.history.answer.chat")).toBeTruthy();
    expect(within(row).getByText("Aで進めてください")).toBeTruthy();
    const evidence = within(row).getByText("はい、Aで進めてください。", { exact: false });
    expect(evidence.textContent).toBe("はい、Aで進めてください。\n理由も続けます。");
    expect(evidence.closest("details")).not.toBeNull();
  });

  it("keeps a dashboard answer exactly as before", () => {
    render(
      <DecisionHistoryTable
        omittedCount={0}
        decisions={[entry({ decision_id: "8", answered_via: "", answer_quote: "", answer_evidence: "" })]}
      />,
    );

    expect(screen.queryByText("goal.history.answer.chat")).toBeNull();
    expect(screen.queryByText("goal.history.answer.quote")).toBeNull();
    expect(screen.queryByText("goal.history.answer.evidence")).toBeNull();
  });
});
