import {
  isDecisionEventName,
  KEEPALIVE_EVENT_NAME,
  type DecisionEventName,
} from "./ui";

export interface Option {
  label: string;
  description: string;
  consequence: string;
}

export interface Goal {
  id: string;
  project_id: string;
  project_name?: string;
  content: string;
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

export interface RelatedGoal {
  id: string;
  headline: string;
  project_name: string;
}

export interface Project {
  id: string;
  name: string;
  root_path: string;
  created_at: string;
}

export interface CreateGoalPayload {
  project_id: string;
  content: string;
  creator: string;
}

export interface Task {
  id: string;
  goal_id: string;
  title: string;
  description: string;
  status: string;
  agent: string;
  files?: string[];
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
  goal_headline: string;
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
  agent_session_id: string;
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

export interface ProposedGoal {
  id: string;
  project_id: string;
  content: string;
  created_at: string;
  project_name: string;
}

export interface InboxResponse {
  open_decisions: Decision[];
  unapplied_decisions: Decision[];
  active_goals: Goal[];
  attention_tasks: TaskView[];
  proposed_goals: ProposedGoal[];
}

export interface GoalResponse {
  goal: Goal;
  now: TaskView[];
  needs_decision: TaskView[];
  unattached_decisions: Decision[];
  next: TaskView[];
  decision_history: DecisionHistoryEntry[];
  decision_history_omitted: number;
  task_commits: GoalTaskCommits[];
  derived_from: RelatedGoal | null;
  derived_goals: RelatedGoal[];
}

export interface TaskGoalSummary {
  id: string;
  title: string;
  project_name?: string;
}

export interface TaskCommit {
  sha: string;
  short_sha: string;
  subject: string;
  files_changed: number;
  insertions: number;
  deletions: number;
  in_history: boolean;
  created_at: string;
}

export interface GoalTaskCommits {
  task_id: string;
  task_title: string;
  commits: TaskCommit[];
}

export interface TaskCommitDiffFile {
  path: string;
  insertions: number;
  deletions: number;
  binary: boolean;
}

export interface TaskCommitDiff {
  sha: string;
  in_history: boolean;
  files: TaskCommitDiffFile[];
  body: string;
  omitted_lines: number;
}

export interface TaskDetailResponse {
  commits?: TaskCommit[];
  task: Task;
  goal: TaskGoalSummary;
  open_decisions: Decision[];
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
    proposed_goals: arrayOrEmpty<ProposedGoal>(source.proposed_goals),
  };
}

export function normalizeGoal(value: unknown): GoalResponse {
  const source = isRecord(value) ? value : {};
  const omitted = source.decision_history_omitted;
  const derivedFrom = source.derived_from;
  const response: GoalResponse = {
    goal: source.goal as Goal,
    now: arrayOrEmpty<TaskView>(source.now),
    needs_decision: arrayOrEmpty<TaskView>(source.needs_decision),
    unattached_decisions: arrayOrEmpty<Decision>(source.unattached_decisions),
    next: arrayOrEmpty<TaskView>(source.next),
    decision_history: arrayOrEmpty<DecisionHistoryEntry>(source.decision_history),
    decision_history_omitted: typeof omitted === "number" && Number.isFinite(omitted) && omitted > 0 ? Math.floor(omitted) : 0,
    task_commits: arrayOrEmpty<GoalTaskCommits>(source.task_commits),
    derived_from: isRecord(derivedFrom) ? derivedFrom as unknown as RelatedGoal : null,
    derived_goals: arrayOrEmpty<RelatedGoal>(source.derived_goals),
  };
  return response;
}

export function normalizeTaskDetail(value: unknown): TaskDetailResponse {
  const source = isRecord(value) ? value : {};
  const omitted = source.decision_history_omitted;
  return {
    task: source.task as Task,
    goal: source.goal as TaskGoalSummary,
    commits: arrayOrEmpty<TaskCommit>(source.commits),
    open_decisions: arrayOrEmpty<Decision>(source.open_decisions),
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

export async function fetchTask(id: string): Promise<TaskDetailResponse> {
  return normalizeTaskDetail(await requestJson<unknown>(`/api/tasks/${encodeURIComponent(id)}`));
}

export async function fetchTaskCommitDiff(taskID: string, sha: string): Promise<TaskCommitDiff> {
  return requestJson<TaskCommitDiff>(
    `/api/tasks/${encodeURIComponent(taskID)}/commits/${encodeURIComponent(sha)}/diff`,
  );
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

export async function approveDecision(id: string): Promise<Goal> {
  return requestJson<Goal>(`/api/decisions/${encodeURIComponent(id)}/approve`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  });
}

export async function rejectDecision(id: string, reason: string): Promise<Decision> {
  return requestJson<Decision>(`/api/decisions/${encodeURIComponent(id)}/reject`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ reason }),
  });
}

export async function withdrawGoal(id: string, reason: string): Promise<Goal> {
  return requestJson<Goal>(`/api/goals/${encodeURIComponent(id)}/withdraw`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ reason }),
  });
}

export async function updateGoalContent(id: string, content: string): Promise<Goal> {
  return requestJson<Goal>(`/api/goals/${encodeURIComponent(id)}/content`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
}

export async function releaseTask(id: string): Promise<TaskView> {
  return requestJson<TaskView>(`/api/tasks/${encodeURIComponent(id)}/release`, {
    method: "POST",
  });
}

export function subscribeToDecisionEvents(onEvent: (name: DecisionEventName) => void): () => void {
  if (typeof WebSocket === "undefined") return () => undefined;

  const watchKeepaliveTimeout = 90_000;
  const watchKeepaliveInterval = 30_000;
  const refreshDebounce = 100;
  const reconnectDelay = 5_000;
  const url = `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/ws`;
  let source: WebSocket | undefined;
  let lastEventAt = Date.now();
  let refreshTimer: ReturnType<typeof setTimeout> | undefined;
  let livenessTimer: ReturnType<typeof setInterval> | undefined;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  let closed = false;

  const handler = (event: MessageEvent) => {
    lastEventAt = Date.now();

    let payload: unknown;
    try {
      payload = JSON.parse(event.data);
    } catch {
      return;
    }
    if (typeof payload !== "object" || payload === null || !("name" in payload)) return;
    const name = payload.name;
    if (typeof name !== "string") return;
    if (name === KEEPALIVE_EVENT_NAME) return;
    if (!isDecisionEventName(name)) return;

    if (refreshTimer !== undefined) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshTimer = undefined;
      onEvent(name);
    }, refreshDebounce);
  };

  const closeSource = () => {
    if (source === undefined) return;
    source.removeEventListener("message", handler);
    source.removeEventListener("close", handleClose);
    source.close();
    source = undefined;
  };

  const openSource = () => {
    if (closed) return;
    source = new WebSocket(url);
    source.addEventListener("message", handler);
    source.addEventListener("close", handleClose);
  };

  const reconnect = () => {
    if (reconnectTimer !== undefined) {
      clearTimeout(reconnectTimer);
      reconnectTimer = undefined;
    }
    closeSource();
    lastEventAt = Date.now();
    openSource();
  };

  const handleClose = () => {
    if (closed || reconnectTimer !== undefined) return;
    reconnectTimer = setTimeout(() => {
      reconnectTimer = undefined;
      reconnect();
    }, reconnectDelay);
  };

  openSource();
  livenessTimer = setInterval(() => {
    if (reconnectTimer !== undefined) return;
    if (Date.now() - lastEventAt >= watchKeepaliveTimeout) reconnect();
  }, watchKeepaliveInterval);

  return () => {
    closed = true;
    if (refreshTimer !== undefined) clearTimeout(refreshTimer);
    if (livenessTimer !== undefined) clearInterval(livenessTimer);
    if (reconnectTimer !== undefined) clearTimeout(reconnectTimer);
    closeSource();
  };
}
