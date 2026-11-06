export type TaskStatus = 'queued' | 'running' | 'waiting_for_user' | 'completed' | 'failed' | 'cancelled'
export interface Provider { id: string; name: string; available: boolean; reason: string; capabilities: { cancellation: boolean; approval: boolean } }
export interface Project { id: number; name: string; path: string; queueState: 'ready' | 'blocked'; blockedReason?: string }
export interface Task { id: number; projectId: number; provider: string; prompt: string; status: TaskStatus; createdAt: string; updatedAt: string; result?: string; error?: string }
export interface Approval { id: string; taskId: number; kind: string; details: Record<string, unknown>; status: string; createdAt: string }
export interface TaskEvent { sequence: number; taskId: number; type: string; payload: Record<string, unknown>; timestamp: string }
export interface TaskSnapshot { tasks: Task[]; sequence: number }
export interface TaskDetail { task: Task; events: TaskEvent[]; sequence: number; pendingApproval?: Approval }
export interface WorkflowStepInput { provider: string; prompt: string }
export interface WorkflowSummary { id: number; projectId: number; status: 'running' | 'completed' | 'failed' | 'cancelled'; cancelRequested: boolean; error?: string; createdAt: string; updatedAt: string }
export interface WorkflowStep extends WorkflowStepInput { index: number; taskId?: number; status?: TaskStatus; result?: string; error?: string }
export interface WorkflowDetail extends WorkflowSummary { steps: WorkflowStep[]; sequence: number }
export interface WorkflowList { workflows: WorkflowSummary[]; sequence: number }

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) { super(message); this.status = status }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...init })
  if (!response.ok) throw new ApiError(response.status, await response.text())
  return response.json() as Promise<T>
}

export function session() { return request<{csrfToken: string}>('/api/session') }
export async function sessionExpired() {
  try { await session(); return false }
  catch (error) { return error instanceof ApiError && error.status === 401 }
}
export function bootstrap(secret: string) {
  return request<{csrfToken: string}>('/api/bootstrap', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({secret})})
}
function post<T>(path: string, csrf: string, body: unknown) {
  return request<T>(path, {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf}, body: JSON.stringify(body)})
}
export function providers() { return request<Provider[]>('/api/providers') }
export function projects() { return request<Project[]>('/api/projects') }
export function addProject(path: string, csrf: string) { return post<Project>('/api/projects', csrf, {path}) }
export function tasks() { return request<TaskSnapshot>('/api/tasks') }
export function task(id: number) { return request<TaskDetail>(`/api/tasks/${id}`) }
export function workflows() { return request<WorkflowList>('/api/workflows') }
export function workflow(id: number) { return request<WorkflowDetail>(`/api/workflows/${id}`) }
export function createWorkflow(projectId: number, steps: WorkflowStepInput[], csrf: string) { return post<{id: number; firstTaskId: number}>('/api/workflows', csrf, {projectId, steps}) }
export function cancelWorkflow(id: number, csrf: string) { return post<WorkflowDetail>(`/api/workflows/${id}/cancel`, csrf, {}) }
export function createTask(projectId: number, prompt: string, provider: string, csrf: string) { return post<Task>('/api/tasks', csrf, {projectId, prompt, provider}) }
export function cancelTask(id: number, csrf: string) { return post<Task>(`/api/tasks/${id}/cancel`, csrf, {}) }
export function answerApproval(taskId: number, approvalId: string, decision: 'approve' | 'deny', csrf: string) {
  return post<TaskDetail>(`/api/tasks/${taskId}/approvals/${encodeURIComponent(approvalId)}`, csrf, {decision})
}
