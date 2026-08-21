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
  it("shows the three goal columns without an action column", () => {
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
    expect(screen.getByRole("columnheader", { name: "form.goal.content.label" })).not.toBeNull();
    expect(screen.getByRole("columnheader", { name: "goal.project" })).not.toBeNull();
    expect(screen.getByRole("columnheader", { name: "task.detail.createdAt" })).not.toBeNull();
    expect(screen.queryByRole("columnheader", { name: "task.column.action" })).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
