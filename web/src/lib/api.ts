import { DECISION_EVENT_NAMES, isDecisionEventName, type DecisionEventName } from "./ui";

export interface Option {
  label: string;
  description: string;
  consequence: string;
}

export interface Goal {
  id: string;
  project_id: string;
  project_name?: string;
  title: string;
  description: string;
  status: string;
  awaiting_decision: boolean;
  result_summary: string;
  work_done: string;
  now_possible: string;
  how_to_verify: string;
  surprises: string;
  needs_review: string;
  next_steps: string;
  created_at: string;
  updated_at: string;
  tasks: TaskView[] | null;
}

export interface Project {
  id: string;
  name: string;
  root_path: string;
  created_at: string;
}

export interface CreateGoalPayload {
  project_id: string;
  title: string;
  description: string;
}

export interface Task {
  id: string;
  goal_id: string;
  title: string;
  description: string;
  status: string;
  agent: string;
  order: number;
  declare_key: string;
  claimed_by: string;
  claimed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface Decision {
  id: string;
  goal_id: string;
  goal_title: string;
  project_name?: string;
  task_id?: string;
  kind: string;
  question: string;
  options: Option[];
  status: string;
  default_option?: string;
  default_after_ms?: number;
  settled_by_default?: boolean;
  answer_label?: string;
  answer_text?: string;
  answered_at?: string;
  applied_at?: string;
  run_id: string;
  created_at: string;
}

export interface TaskView extends Task {
  held_for_seconds: number;
  open_decisions: Decision[];
  project_id: string;
  project_name?: string;
}

export interface DecisionHistoryEntry {
  decision_id: string;
  task_id: string;
  question: string;
  answer_label: string;
  answer_text: string;
  settled_by_default: boolean;
  default_applied_at?: string;
  answered_at: string;
  applied_at: string;
}

export interface InboxResponse {
  open_decisions: Decision[];
  unapplied_decisions: Decision[];
  active_goals: Goal[];
  attention_tasks: TaskView[];
}

export interface GoalResponse {
  goal: Goal;
  now: TaskView[];
  needs_decision: TaskView[];
  unattached_decisions: Decision[];
  next: TaskView[];
  decision_history: DecisionHistoryEntry[];
  decision_history_omitted: number;
}

export interface AnswerPayload {
  answer_label: string;
  answer_text: string;
}

export interface ReviseDecisionPayload {
  options: Option[];
}

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function arrayOrEmpty<T>(value: unknown): T[] {
  return Array.isArray(value) ? value as T[] : [];
}

async function readResponseBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return {};

  try {
    return JSON.parse(text) as unknown;
  } catch {
    return {};
  }
}

export async function requestJson<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> {
  const response = await fetch(input, init);
  const body = await readResponseBody(response);
  if (!response.ok) {
    const message = isRecord(body) && typeof body.error === "string" ? body.error : `HTTP ${response.status}`;
    throw new ApiError(response.status, message);
  }
  return body as T;
}

export function normalizeInbox(value: unknown): InboxResponse {
  const source = isRecord(value) ? value : {};
  return {
    open_decisions: arrayOrEmpty<Decision>(source.open_decisions),
    unapplied_decisions: arrayOrEmpty<Decision>(source.unapplied_decisions),
    active_goals: arrayOrEmpty<Goal>(source.active_goals),
    attention_tasks: arrayOrEmpty<TaskView>(source.attention_tasks),
  };
}

export function normalizeGoal(value: unknown): GoalResponse {
  const source = isRecord(value) ? value : {};
  const omitted = source.decision_history_omitted;
  return {
    goal: source.goal as Goal,
    now: arrayOrEmpty<TaskView>(source.now),
    needs_decision: arrayOrEmpty<TaskView>(source.needs_decision),
    unattached_decisions: arrayOrEmpty<Decision>(source.unattached_decisions),
    next: arrayOrEmpty<TaskView>(source.next),
    decision_history: arrayOrEmpty<DecisionHistoryEntry>(source.decision_history),
    decision_history_omitted: typeof omitted === "number" && Number.isFinite(omitted) && omitted > 0 ? Math.floor(omitted) : 0,
  };
}

export async function fetchInbox(): Promise<InboxResponse> {
  return normalizeInbox(await requestJson<unknown>("/api/inbox"));
}

export async function fetchProjects(): Promise<Project[]> {
  const value = await requestJson<unknown>("/api/projects");
  return Array.isArray(value) ? (value as Project[]) : [];
}

export async function createGoal(payload: CreateGoalPayload): Promise<Goal> {
  return requestJson<Goal>("/api/goals", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

export async function fetchGoal(id: string): Promise<GoalResponse> {
  return normalizeGoal(await requestJson<unknown>(`/api/goals/${encodeURIComponent(id)}`));
}

export async function answerDecision(id: string, payload: AnswerPayload): Promise<Decision> {
  return requestJson<Decision>(`/api/decisions/${encodeURIComponent(id)}/answer`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

export async function reviseDecision(id: string, payload: ReviseDecisionPayload): Promise<Decision> {
  return requestJson<Decision>(`/api/decisions/${encodeURIComponent(id)}/revise`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

export async function approveCompletion(id: string): Promise<Goal> {
  return requestJson<Goal>(`/api/decisions/${encodeURIComponent(id)}/approve`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  });
}

export async function rejectCompletion(id: string, reason: string): Promise<Decision> {
  return requestJson<Decision>(`/api/decisions/${encodeURIComponent(id)}/reject`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ reason }),
  });
}

export async function releaseTask(id: string): Promise<TaskView> {
  return requestJson<TaskView>(`/api/tasks/${encodeURIComponent(id)}/release`, {
    method: "POST",
  });
}

export function subscribeToDecisionEvents(onEvent: (name: DecisionEventName) => void): () => void {
  if (typeof EventSource === "undefined") return () => undefined;

  const source = new EventSource("/api/events");
  const handler = (event: Event) => {
    if (isDecisionEventName(event.type)) onEvent(event.type);
  };

  DECISION_EVENT_NAMES.forEach((name) => source.addEventListener(name, handler));
  return () => {
    DECISION_EVENT_NAMES.forEach((name) => source.removeEventListener(name, handler));
    source.close();
  };
}
