import { afterEach, describe, expect, it, vi } from "vitest";
import { approveCompletion, rejectCompletion } from "./api";

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
