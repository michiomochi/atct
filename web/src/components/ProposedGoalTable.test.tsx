import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ProposedGoalTable } from "./ProposedGoalTable";

const i18nMock = vi.hoisted(() => ({
  t: (key: string) => key,
  i18n: { language: "en" },
  initReactI18next: { type: "3rdParty", init: () => undefined },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => i18nMock,
  initReactI18next: i18nMock.initReactI18next,
}));

describe("ProposedGoalTable", () => {
  it("shows the approval hint without an approval button", () => {
    render(
      <ProposedGoalTable
        goals={[
          {
            id: "goal-1",
            project_id: "project-1",
            content: "Proposed goal\n\nA proposed description",
            created_at: "2026-08-21T00:00:00Z",
            project_name: "Project",
          },
        ]}
      />,
    );

    expect(screen.getByText("Proposed goal")).not.toBeNull();
    expect(screen.getByText("A proposed description")).not.toBeNull();
    expect(screen.getByText("Project")).not.toBeNull();
    expect(screen.getByText(/dashboard\.proposed\.approveHint|Approve from the answers table above|上の回答待ちの表から承認します/)).not.toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
