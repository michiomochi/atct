import { afterEach, describe, expect, it, vi } from "vitest";
import { approveCompletion, createGoal, fetchProjects, rejectCompletion } from "./api";

describe("completion API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("posts the approver to the completion endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('{"id":"goal-1","status":"done"}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await approveCompletion("decision/1", "michio");

    expect(fetchMock).toHaveBeenCalledWith("/api/decisions/decision%2F1/approve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answered_by: "michio" }),
    });
  });

  it("posts the rejector and optional reason to the completion endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('{"id":"decision-1","status":"answered"}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await rejectCompletion("decision-1", "michio", "Needs more evidence");

    expect(fetchMock).toHaveBeenCalledWith("/api/decisions/decision-1/reject", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answered_by: "michio", reason: "Needs more evidence" }),
    });
  });
});

describe("goal creation API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches the registered projects", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('[{"id":"project-1","name":"atct","root_path":"/repo","created_at":"2026-08-16T00:00:00Z"}]'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(fetchProjects()).resolves.toEqual([
      { id: "project-1", name: "atct", root_path: "/repo", created_at: "2026-08-16T00:00:00Z" },
    ]);
    expect(fetchMock).toHaveBeenCalledWith("/api/projects", undefined);
  });

  it("posts a new goal to the selected project", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('{"id":"goal-1","project_id":"project-1","title":"Ship it"}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await createGoal({ project_id: "project-1", title: "Ship it", description: "Details" });

    expect(fetchMock).toHaveBeenCalledWith("/api/goals", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project_id: "project-1", title: "Ship it", description: "Details" }),
    });
  });
});
