import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ProjectList } from "./ProjectList";

const { archiveProject, fetchProjects, unarchiveProject, t } = vi.hoisted(() => ({
  archiveProject: vi.fn(),
  fetchProjects: vi.fn(),
  unarchiveProject: vi.fn(),
  t: (key: string, options?: { name?: string }) => (options?.name ? `${key}:${options.name}` : key),
}));

vi.mock("../lib/api", () => ({ archiveProject, fetchProjects, unarchiveProject }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));

const projects = [
  { id: "1", name: "alpha", root_path: "/a", created_at: "2026-01-01T00:00:00Z" },
  { id: "2", name: "beta", root_path: "/b", created_at: "2026-01-01T00:00:00Z", archived_at: "2026-02-01T00:00:00Z" },
];

describe("ProjectList", () => {
  beforeEach(() => {
    fetchProjects.mockReset().mockResolvedValue(projects);
    archiveProject.mockReset().mockResolvedValue({});
    unarchiveProject.mockReset().mockResolvedValue({});
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows each project's state and the matching action", async () => {
    render(<ProjectList />);
    expect(await screen.findByText("alpha")).toBeTruthy();
    expect(screen.getByText("project.status.active")).toBeTruthy();
    expect(screen.getByText("project.status.archived")).toBeTruthy();
    expect(screen.getByRole("button", { name: "project.action.archive" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "project.action.unarchive" })).toBeTruthy();
  });

  it("confirms, archives, reloads and notifies the parent", async () => {
    const onChanged = vi.fn();
    render(<ProjectList onChanged={onChanged} />);
    fireEvent.click(await screen.findByRole("button", { name: "project.action.archive" }));

    expect(window.confirm).toHaveBeenCalledWith("project.confirm.archive:alpha");
    await waitFor(() => expect(archiveProject).toHaveBeenCalledWith("1"));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(fetchProjects).toHaveBeenCalledTimes(2);
  });

  it("unarchives an archived project", async () => {
    render(<ProjectList />);
    fireEvent.click(await screen.findByRole("button", { name: "project.action.unarchive" }));
    await waitFor(() => expect(unarchiveProject).toHaveBeenCalledWith("2"));
  });

  it("does not call the API when the confirmation is cancelled", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<ProjectList />);
    fireEvent.click(await screen.findByRole("button", { name: "project.action.archive" }));
    expect(archiveProject).not.toHaveBeenCalled();
    expect(fetchProjects).toHaveBeenCalledTimes(1);
  });

  it("shows the reason when the update fails", async () => {
    archiveProject.mockRejectedValue(new Error("project is archived; run atct project unarchive alpha"));
    const onChanged = vi.fn();
    render(<ProjectList onChanged={onChanged} />);
    fireEvent.click(await screen.findByRole("button", { name: "project.action.archive" }));
    expect(await screen.findByText(/run atct project unarchive alpha/)).toBeTruthy();
    expect(onChanged).not.toHaveBeenCalled();
  });
});
