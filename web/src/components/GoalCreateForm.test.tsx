import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    fetchProjects.mockReset().mockResolvedValue([{ id: "project-1", name: "Project 1" }]);
    createGoal.mockReset().mockResolvedValue({});
  });

  it("emits clean when a dirty dialog is closed", async () => {
    const dirtyStates: boolean[] = [];
    const handleDirty = (event: Event) => {
      dirtyStates.push((event as CustomEvent<boolean>).detail);
    };
    window.addEventListener("atct:form-dirty", handleDirty);

    render(<GoalCreateForm />);
    fireEvent.click(await screen.findByRole("button", { name: "form.goal.action.new" }));
    fireEvent.change(await screen.findByLabelText("form.goal.title.label"), {
      target: { value: "Draft goal" },
    });

    await waitFor(() => expect(dirtyStates).toContain(true));
    fireEvent.click(screen.getByRole("button", { name: "form.goal.cancel" }));

    await waitFor(() => expect(dirtyStates[dirtyStates.length - 1]).toBe(false));
    window.removeEventListener("atct:form-dirty", handleDirty);
  });

  it("emits goal-created after submitting the dialog form", async () => {
    const goalCreated = vi.fn();
    window.addEventListener("atct:goal-created", goalCreated);

    render(<GoalCreateForm />);
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
    await waitFor(() => expect(goalCreated).toHaveBeenCalledTimes(1));
    window.removeEventListener("atct:goal-created", goalCreated);
  });
});
