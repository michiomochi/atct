import { cleanup, render } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { afterEach, describe, expect, it } from "vitest";
import { en } from "../i18n/en";
import { ja } from "../i18n/ja";
import type { NextAction as NextActionValue } from "../lib/api";
import { NextAction } from "./NextAction";

afterEach(() => {
  cleanup();
});

// Real strings from the locale files, so a wording change shows up here.
const states: { action: NextActionValue; expected: Record<"ja" | "en", string> }[] = [
  {
    action: { code: "subcommander_plan", actor: "subcommander" },
    expected: {
      en: "executor · Waiting for a plan (write the spec and plan)",
      ja: "executor · 計画待ち（spec と plan を書く）",
    },
  },
  {
    action: { code: "executor_implement", actor: "executor", task_count: 2 },
    expected: {
      en: "task · In progress (2 task(s))",
      ja: "task · 実装中 (2件)",
    },
  },
  {
    action: { code: "executor_work", actor: "subcommander" },
    expected: {
      en: "executor · Working on it (request the goal review when done)",
      ja: "executor · 実装中（終わったら goal の review を依頼する）",
    },
  },
  {
    action: { code: "subcommander_review_task", actor: "subcommander", task_count: 1 },
    expected: {
      en: "executor · Waiting for review (1 task(s))",
      ja: "executor · レビュー待ち (1件)",
    },
  },
  {
    action: { code: "human_review", actor: "human", decision_id: 7 },
    expected: {
      en: "Human · Waiting for human approval (decision 7)",
      ja: "人間 · 人間の承認待ち（decision 7）",
    },
  },
  {
    action: { code: "commander_complete", actor: "commander" },
    expected: {
      en: "commander · Approved. Merge and complete the goal",
      ja: "commander · 承認済み。merge して完了する",
    },
  },
  {
    action: { code: "commander_reject_handoff", actor: "commander" },
    expected: {
      en: "commander · Rejected. Send it back to the executor",
      ja: "commander · 却下済み。差し戻す",
    },
  },
  {
    action: { code: "done", actor: "" },
    expected: { en: "Done", ja: "完了" },
  },
];

for (const lang of ["ja", "en"] as const) {
  describe(`NextAction in ${lang}`, () => {
    for (const { action, expected } of states) {
      it(`renders ${action.code} as "${expected[lang]}"`, async () => {
        const inst = createInstance();
        await inst.init({
          lng: lang,
          resources: { en: { translation: en }, ja: { translation: ja } },
        });
        const { container } = render(
          <I18nextProvider i18n={inst}>
            <NextAction action={action} />
          </I18nextProvider>,
        );

        expect(container.textContent).toBe(expected[lang]);
      });
    }
  });
}
