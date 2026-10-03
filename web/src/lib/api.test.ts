import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  archiveProject,
  unarchiveProject,
  approveDecision,
  createGoal,
  fetchGoalHandoffHistory,
  fetchGoalReviewExchanges,
  fetchTaskReviewExchanges,
  normalizeReviewExchanges,
  fetchProjects,
  normalizeGoal,
  rejectDecision,
  subscribeToDecisionEvents,
  updateUILocale,
} from "./api";
import { DECISION_EVENT_NAMES } from "./ui";

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];

  private readonly listeners = new Map<string, Set<(event: Event) => void>>();
  readonly close = vi.fn();

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: (event: Event) => void) {
    const listeners = this.listeners.get(type) ?? new Set<(event: Event) => void>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: (event: Event) => void) {
    this.listeners.get(type)?.delete(listener);
  }

  emitMessage(payload: string) {
    for (const listener of this.listeners.get("message") ?? []) {
      listener({ type: "message", data: payload } as MessageEvent<string>);
    }
  }

  emitClose() {
    for (const listener of this.listeners.get("close") ?? []) {
      listener(new Event("close"));
    }
  }
}

describe("decision API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("posts approval to the generic decision endpoint", async () => {
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

  it("posts a rejection reason to the generic decision endpoint", async () => {
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

describe("locale API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("puts the selected locale using the exact JSON contract", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('{"locale":"ja"}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(updateUILocale("ja")).resolves.toEqual({ locale: "ja" });
    expect(fetchMock).toHaveBeenCalledWith("/api/ui-settings/locale", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ locale: "ja" }),
    });
  });

  it("keeps non-2xx responses as ApiError", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 503,
      text: () => Promise.resolve('{"error":"locale service unavailable"}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(updateUILocale("ja")).rejects.toBeInstanceOf(ApiError);
  });
});

describe("handoff history API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches an after_id page for a goal handoff", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: () => Promise.resolve('{"id":"handoff-1","entries":[{"id":4,"handoff_id":"handoff-1","kind":"review_requested","body":"review","author_session_id":8,"in_reply_to_id":2,"created_at":"2026-08-20T00:00:00Z"}],"has_more":false,"next_after_id":4}'),
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(fetchGoalHandoffHistory("goal/1", "handoff/1", 2, 20)).resolves.toMatchObject({
      id: "handoff-1",
      next_after_id: 4,
      entries: [{ id: 4, kind: "review_requested", author_session_id: 8, in_reply_to_id: 2 }],
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/goals/goal%2F1/handoffs/handoff%2F1?after_id=2&limit=20",
      undefined,
    );
  });

  it("normalizes embedded handoff pages without changing older goal payloads", () => {
    const response = normalizeGoal({
      goal: { id: "goal-1" },
      handoffs: [{ id: "handoff-1", entries: [{ id: 1, handoff_id: "handoff-1", kind: "progress", body: "removed", author_session_id: 8, created_at: "2026-08-20T00:00:00Z" }, { id: 2, handoff_id: "handoff-1", kind: "received", body: "received", author_session_id: 8, created_at: "2026-08-20T00:00:00Z" }] }],
    });

    expect(response.handoffs).toHaveLength(1);
    expect(response.handoffs?.[0]).toMatchObject({
      id: "handoff-1",
      entries: [{ id: 2, kind: "received" }],
    });
    expect(normalizeGoal({ goal: { id: "goal-2" } }).handoffs).toEqual([]);
  });
});

describe("goal detail API", () => {
  it("normalizes missing successor summaries to an empty array", () => {
    const response = normalizeGoal({ goal: { id: "goal-1" } });

    expect((response.goal as { next_goals?: unknown }).next_goals).toEqual([]);
  });
});

describe("decision event subscription", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    FakeWebSocket.instances = [];
  });

  it("connects to the WebSocket push endpoint", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    // location.host carries the port, which the daemon's origin check requires.
    expect(FakeWebSocket.instances[0]?.url).toBe(`ws://${location.host}/api/ws`);
    unsubscribe();
  });

  it("notifies once after a decision frame is debounced", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeWebSocket.instances[0]!;

    source.emitMessage('{"name":"decision.created","data":{}}');
    expect(onEvent).not.toHaveBeenCalled();
    vi.advanceTimersByTime(100);

    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledWith("decision.created");
    unsubscribe();
  });

  it("notifies for every decision event name", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeWebSocket.instances[0]!;

    for (const name of DECISION_EVENT_NAMES) {
      source.emitMessage(JSON.stringify({ name, data: {} }));
      vi.advanceTimersByTime(100);
    }

    expect(onEvent.mock.calls.map(([name]) => name)).toEqual([...DECISION_EVENT_NAMES]);
    unsubscribe();
  });

  it("reconnects five seconds after a close event", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeWebSocket.instances[0]!;

    source.emitClose();
    vi.advanceTimersByTime(4_999);
    expect(FakeWebSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);

    expect(FakeWebSocket.instances).toHaveLength(2);
    unsubscribe();
  });

  it("does not notify for keepalive frames", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeWebSocket.instances[0]!;

    source.emitMessage('{"name":"keepalive"}');
    vi.advanceTimersByTime(100);

    expect(onEvent).not.toHaveBeenCalled();
    unsubscribe();
  });

  it("does not reconnect while keepalive frames arrive every 60 seconds", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeWebSocket.instances[0]!;

    vi.advanceTimersByTime(60_000);
    source.emitMessage('{"name":"keepalive"}');
    vi.advanceTimersByTime(60_000);
    source.emitMessage('{"name":"keepalive"}');
    expect(FakeWebSocket.instances).toHaveLength(1);

    vi.advanceTimersByTime(89_999);
    expect(FakeWebSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);

    expect(FakeWebSocket.instances).toHaveLength(2);
    unsubscribe();
  });

  it("debounces four consecutive frames into the last event", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeWebSocket.instances[0]!;

    for (const name of [
      "decision.created",
      "goal.created",
      "detection.commits_missing",
      "decision.created",
    ]) {
      source.emitMessage(JSON.stringify({ name, data: {} }));
    }
    vi.advanceTimersByTime(100);

    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledWith("decision.created");
    unsubscribe();
  });

  it("does not reconnect after unsubscribe even if close is emitted", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const onEvent = vi.fn();
    const unsubscribe = subscribeToDecisionEvents(onEvent);
    const source = FakeWebSocket.instances[0]!;

    unsubscribe();
    expect(source.close).toHaveBeenCalledTimes(1);
    source.emitClose();
    vi.advanceTimersByTime(5_000);

    expect(FakeWebSocket.instances).toHaveLength(1);
  });
});

describe("review exchange API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("turns missing or null arrays into empty ones", () => {
    expect(normalizeReviewExchanges({})).toEqual({ exchanges: [], gaps: [] });
    expect(normalizeReviewExchanges({ exchanges: null, gaps: null })).toEqual({ exchanges: [], gaps: [] });
    expect(normalizeReviewExchanges(null)).toEqual({ exchanges: [], gaps: [] });
  });

  it("keeps valid pairs, null responses and gaps, and drops malformed ones", () => {
    const history = normalizeReviewExchanges({
      exchanges: [
        {
          scope: "task", task_id: 7, handoff_id: "h1",
          rejection: { source: "handoff", at: "2026-10-01T00:00:01Z", actor_session_id: 3, reason: "fix" },
          response: { at: "2026-10-01T00:00:02Z", author_session_id: 4, handoff_id: "h2", report: "fixed" },
        },
        { scope: "goal", rejection: { source: "human", at: "2026-10-01T00:00:03Z", decision_id: 9, reason: "no" }, response: null },
        { scope: "bogus", rejection: { source: "human", at: "x", reason: "" }, response: null },
        { scope: "plan", rejection: { source: "unknown", at: "x", reason: "" }, response: null },
        "not an object",
      ],
      gaps: [
        { scope: "goal", handoff_id: "g1", before_at: "2026-10-01T00:00:00Z" },
        { scope: "goal", handoff_id: "", before_at: "2026-10-01T00:00:00Z" },
      ],
    });

    expect(history.exchanges).toHaveLength(2);
    expect(history.exchanges[0]).toMatchObject({ scope: "task", task_id: 7, handoff_id: "h1", response: { author_session_id: 4, report: "fixed" } });
    expect(history.exchanges[1]).toMatchObject({ scope: "goal", rejection: { source: "human", decision_id: 9 }, response: null });
    expect(history.exchanges[1].handoff_id).toBeUndefined();
    expect(history.gaps).toEqual([{ scope: "goal", handoff_id: "g1", before_at: "2026-10-01T00:00:00Z" }]);
  });

  it("fetches the goal and task endpoints", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, text: () => Promise.resolve('{"exchanges":[],"gaps":[]}') });
    vi.stubGlobal("fetch", fetchMock);

    await expect(fetchGoalReviewExchanges("goal/1")).resolves.toEqual({ exchanges: [], gaps: [] });
    await expect(fetchTaskReviewExchanges("task/1")).resolves.toEqual({ exchanges: [], gaps: [] });
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/goals/goal%2F1/review-exchanges", undefined);
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/tasks/task%2F1/review-exchanges", undefined);
  });
});

describe("project archive requests", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each([
    ["archive", archiveProject],
    ["unarchive", unarchiveProject],
  ] as const)("posts to /api/projects/{id}/%s", async (action, call) => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, text: () => Promise.resolve('{"id":"p 1","name":"x"}') });
    vi.stubGlobal("fetch", fetchMock);
    await call("p 1");
    expect(fetchMock).toHaveBeenCalledWith(`/api/projects/p%201/${action}`, { method: "POST" });
  });

  it("surfaces the reason of a refused request", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      text: () => Promise.resolve('{"error":"project \\"x\\" is archived"}'),
    }));
    await expect(archiveProject("1")).rejects.toThrow('project "x" is archived');
  });
});
