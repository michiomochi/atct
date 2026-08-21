import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GoalCreateForm } from "./GoalCreateForm";

const { createGoal, fetchProjects, t } = vi.hoisted(() => ({
  createGoal: vi.fn(),
  fetchProjects: vi.fn(),
  t: (key: string) => key,
}));

vi.mock("../lib/api", () => ({
  createGoal,
  fetchProjects,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t }),
}));

describe("GoalCreateForm header dialog", () => {
  beforeEach(() => {
    cleanup();
    fetchProjects.mockReset().mockResolvedValue([{ id: "project-1", name: "Project 1" }]);
    createGoal.mockReset().mockResolvedValue({});
  });

  it("calls onDirtyChange with false when a dirty dialog is closed", async () => {
    const handleDirty = vi.fn();

    render(<GoalCreateForm onDirtyChange={handleDirty} />);
    fireEvent.click(await screen.findByRole("button", { name: "form.goal.action.new" }));
    fireEvent.change(await screen.findByLabelText("form.goal.title.label"), {
      target: { value: "Draft goal" },
    });

    await waitFor(() => expect(handleDirty).toHaveBeenCalledWith(true));
    fireEvent.click(screen.getByRole("button", { name: "form.goal.cancel" }));

    await waitFor(() => expect(handleDirty).toHaveBeenLastCalledWith(false));
  });

  it("calls onCreated after submitting the dialog form", async () => {
    const onCreated = vi.fn();

    render(<GoalCreateForm onCreated={onCreated} />);
    fireEvent.click(await screen.findByRole("button", { name: "form.goal.action.new" }));
    fireEvent.change(await screen.findByLabelText("form.goal.project.label"), {
      target: { value: "project-1" },
    });
    fireEvent.change(await screen.findByLabelText("form.goal.title.label"), {
      target: { value: "Created goal" },
    });
    fireEvent.submit(screen.getByRole("button", { name: "form.goal.submit" }).closest("form")!);

    await waitFor(() => expect(createGoal).toHaveBeenCalledWith({
      project_id: "project-1",
      title: "Created goal",
      description: "",
    }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
  });
});
