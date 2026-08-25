import { afterEach, describe, expect, it, vi } from "vitest";
import {
  approveDecision,
  createGoal,
  fetchProjects,
  rejectDecision,
  subscribeToDecisionEvents,
} from "./api";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  private readonly listeners = new Map<string, Set<(event: Event) => void>>();
  readonly close = vi.fn();

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }

  addEventListener(name: string, listener: (event: Event) => void) {
    const listeners = this.listeners.get(name) ?? new Set<(event: Event) => void>();
    listeners.add(listener);
    this.listeners.set(name, listeners);
  }

  removeEventListener(name: string, listener: (event: Event) => void) {
    this.listeners.get(name)?.delete(listener);
  }

  registeredNames() {
    return [...this.listeners.keys()];
  }

  emit(name: string) {
    for (const listener of this.listeners.get(name) ?? []) {
      listener(new Event(name));
    }
  }
}

describe("completion API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("posts completion approval to the completion endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('{"id":"goal-1","status":"done"}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await approveDecision("decision/1");

    expect(fetchMock).toHaveBeenCalledWith("/api/decisions/decision%2F1/approve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
    });
  });

  it("posts the rejection reason to the completion endpoint", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('{"id":"decision-1","status":"answered"}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await rejectDecision("decision-1", "Needs more evidence");

    expect(fetchMock).toHaveBeenCalledWith("/api/decisions/decision-1/reject", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ reason: "Needs more evidence" }),
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

    await createGoal({ project_id: "project-1", content: "Ship it\n\nDetails", creator: "human" });

    expect(fetchMock).toHaveBeenCalledWith("/api/goals", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project_id: "project-1", content: "Ship it\n\nDetails", creator: "human" }),
    });
  });
});

describe("decision event subscription", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    FakeEventSource.instances = [];
  });

  it("registers every event and does not refresh for keepalive", () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", FakeEventSource);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeEventSource.instances[0]!;

    expect(source.registeredNames()).toEqual([
      "decision.created",
      "decision.answered",
      "decision.withdrawn",
      "decision.applied",
      "decision.approved",
      "decision.rejected",
      "goal.created",
      "detection.completion_report_missing",
      "detection.commits_missing",
      "detection.undeclared_goal",
      "detection.all_tasks_dropped",
      "detection.unclaimed_doing",
      "detection.handoff_unreceived",
      "detection.handoff_unreported",
      "handoff_reported",
      "detection.claim_undelegated",
      "detection.decision_answered_unapplied",
      "detection.decision_default_unapplied",
      "detection.claim_stale",
      "keepalive",
    ]);

    source.emit("keepalive");
    source.emit("goal.created");
    source.emit("detection.commits_missing");
    source.emit("decision.created");

    expect(onEvent).not.toHaveBeenCalled();
    vi.advanceTimersByTime(100);
    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledWith("decision.created");
    unsubscribe();
  });

  it("reconnects after 90 seconds since the last event, including keepalive", () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", FakeEventSource);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const firstSource = FakeEventSource.instances[0];

    vi.advanceTimersByTime(60_000);
    firstSource.emit("keepalive");
    vi.advanceTimersByTime(60_000);
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.advanceTimersByTime(30_000);

    expect(FakeEventSource.instances).toHaveLength(2);
    expect(firstSource.close).toHaveBeenCalledTimes(1);
    expect(onEvent).not.toHaveBeenCalled();
    unsubscribe();
  });
});
