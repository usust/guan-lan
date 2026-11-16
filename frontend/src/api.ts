import { readVerificationStream, VerificationConnectionError, type VerificationEvent } from './runtimeVerification'
import type { CreateUserInput, Session, SystemInfo, User, UserStatus, ChallengeSet, EnvironmentManifest, SetResponse, Ranking, Submission, RuntimeInstance } from './types'

const API_BASE = '/api/v1'
let csrfToken: string | null = null
let onUnauthorized: (() => void) | undefined

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly requestId?: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

// Session credentials remain in the browser's HttpOnly cookie; CSRF stays in memory.
export function setCsrfToken(token: string | null): void {
  csrfToken = token
}

export function setUnauthorizedHandler(handler: () => void): void {
  onUnauthorized = handler
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.requestId ? `${error.message}（请求编号：${error.requestId}）` : error.message
  }
  return error instanceof Error ? error.message : '操作失败，请稍后重试。'
}

export type UploadListener = (loaded: number, total: number) => void

export async function request<T>(path: string, options: RequestInit & { onUploadProgress?: UploadListener } = {}): Promise<T> {
  const method = (options.method ?? 'GET').toUpperCase()
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  if (options.body !== undefined && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }

  let response: Response
  try {
    if (options.onUploadProgress) {
      response = await new Promise<Response>((resolve, reject) => {
        const xhr = new XMLHttpRequest()
        xhr.open(method, `${API_BASE}${path}`)
        xhr.withCredentials = true
        headers.forEach((value, key) => xhr.setRequestHeader(key, value))
        const abort = () => xhr.abort()
        const cleanup = () => options.signal?.removeEventListener('abort', abort)
        xhr.upload.onprogress = event => {
          if (event.lengthComputable) options.onUploadProgress?.(event.loaded, event.total)
        }
        xhr.onload = () => {
          cleanup()
          resolve(new Response(xhr.responseText || null, { status: xhr.status }))
        }
        xhr.onerror = () => { cleanup(); reject(new Error('Network error')) }
        xhr.onabort = () => { cleanup(); reject(new DOMException('Aborted', 'AbortError')) }
        if (options.signal?.aborted) { reject(new DOMException('Aborted', 'AbortError')); return }
        options.signal?.addEventListener('abort', abort, { once: true })
        xhr.send(options.body as XMLHttpRequestBodyInit | null | undefined)
      })
    } else {
      response = await fetch(`${API_BASE}${path}`, { ...options, method, headers, credentials: 'include' })
    }
  } catch {
    throw new ApiError('无法连接服务器，请检查网络后重试。', 0, 'NETWORK_ERROR')
  }

  if (response.status === 401 && path !== '/auth/login') onUnauthorized?.()
  if (response.status === 204 && response.ok) return undefined as T

  let payload: { data?: T; error?: { code?: string; message?: string }; request_id?: string }
  try {
    payload = await response.json()
  } catch {
    throw new ApiError('服务器返回了无法识别的响应，请稍后重试。', response.status, 'INVALID_RESPONSE')
  }
  if (payload === null || typeof payload !== 'object') {
    throw new ApiError('服务器返回了无法识别的响应，请稍后重试。', response.status, 'INVALID_RESPONSE')
  }
  if (!response.ok || payload.error) {
    throw new ApiError(
      payload.error?.message || `请求失败（HTTP ${response.status}）。`,
      response.status,
      payload.error?.code || 'REQUEST_FAILED',
      payload.request_id,
    )
  }
  if (!('data' in payload)) {
    throw new ApiError('服务器响应缺少数据。', response.status, 'INVALID_RESPONSE', payload.request_id)
  }
  return payload.data as T
}

export interface StagedPackage {
  package_token: string
  environment: EnvironmentManifest
  package_sha256: string
}
export interface PackageUploadSession<T = StagedPackage> {
  state: 'uploading' | 'validating' | 'error' | 'done'
  error?: string
  id: string
  name: string
  size: number
  offset: number
  chunk_size: number
  chunks: { size: number; sha256: string }[]
  result?: T
}

// XHR exposes bytes sent while a chunk is in flight; fetch only reports completion.
export function sendPackageChunk<T = StagedPackage>(id: string, offset: number, chunk: Blob, sha256: string, signal: AbortSignal, progress: (bytes: number) => void, basePath = `/admin/package-uploads`): Promise<PackageUploadSession<T>> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', `${API_BASE}${basePath}/${encodeURIComponent(id)}`)
    xhr.withCredentials = true
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.setRequestHeader('Upload-Offset', String(offset))
    xhr.setRequestHeader('X-Chunk-SHA256', sha256)
    if (csrfToken) xhr.setRequestHeader('X-CSRF-Token', csrfToken)
    const abort = () => xhr.abort()
    const cleanup = () => signal.removeEventListener('abort', abort)
    xhr.upload.onprogress = event => progress(event.loaded)
    xhr.onerror = () => { cleanup(); reject(new ApiError('网络中断，已上传的分片已保留，可继续上传。', 0, 'NETWORK_ERROR')) }
    xhr.onabort = () => { cleanup(); reject(new DOMException('上传已暂停', 'AbortError')) }
    xhr.onload = () => {
      cleanup()
      if (xhr.status === 401) onUnauthorized?.()
      try {
        const payload = JSON.parse(xhr.responseText)
        if (xhr.status < 200 || xhr.status >= 300 || payload.error) {
          reject(new ApiError(payload.error?.message || '分片上传失败', xhr.status, payload.error?.code || 'UPLOAD_FAILED', payload.request_id))
        } else if (!payload.data) {
          reject(new ApiError('服务器响应缺少数据', xhr.status, 'INVALID_RESPONSE'))
        } else resolve(payload.data)
      } catch { reject(new ApiError('服务器返回了无法识别的响应', xhr.status, 'INVALID_RESPONSE')) }
    }
    if (signal.aborted) { cleanup(); reject(new DOMException('上传已暂停', 'AbortError')); return }
    signal.addEventListener('abort', abort, { once: true })
    xhr.send(chunk)
  })
}

export const api = {
  verifyRuntime: async (set: string, question: string, version: number, report?: (event: VerificationEvent) => void) => {
    if (!report) return request<SetResponse>(`/admin/sets/${encodeURIComponent(set)}/questions/${encodeURIComponent(question)}/verify`, { method: 'POST', body: JSON.stringify({ version }) })
    const controller = new AbortController()
    let timeout: ReturnType<typeof setTimeout> | undefined
    const activity = () => { clearTimeout(timeout); timeout = setTimeout(() => controller.abort(), 90_000) }
    activity()
    try {
      const response = await fetch(`${API_BASE}/admin/sets/${encodeURIComponent(set)}/questions/${encodeURIComponent(question)}/verify`, {
        method: 'POST', credentials: 'include', signal: controller.signal,
        headers: { Accept: 'application/x-ndjson', 'Content-Type': 'application/json', ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}) },
        body: JSON.stringify({ version }),
      })
      if (response.status === 401) onUnauthorized?.()
      if (!response.ok) {
        const payload = await response.json()
        throw new Error(payload.error?.message || '运行验收请求失败')
      }
      await readVerificationStream(response, report, activity)
    } catch (e) {
      if (controller.signal.aborted) throw new VerificationConnectionError('验收进度已超过 90 秒未更新，连接可能中断；请刷新查看验收状态')
      throw e
    } finally { clearTimeout(timeout) }
  },
  runtime: (question: string) => request<RuntimeInstance>(`/subtasks/${encodeURIComponent(question)}/runtime`),
  startRuntime: async (question: string, report?: (event: VerificationEvent) => void): Promise<RuntimeInstance> => {
    if (!report) return request<RuntimeInstance>(`/subtasks/${encodeURIComponent(question)}/runtime`, { method: 'POST' })
    const controller = new AbortController()
    let timeout: ReturnType<typeof setTimeout> | undefined
    const activity = () => { clearTimeout(timeout); timeout = setTimeout(() => controller.abort(), 90_000) }
    activity()
    try {
      const response = await fetch(`${API_BASE}/subtasks/${encodeURIComponent(question)}/runtime`, {
        method: 'POST', credentials: 'include', signal: controller.signal,
        headers: { Accept: 'application/x-ndjson', ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}) },
      })
      if (response.status === 401) onUnauthorized?.()
      if (!response.ok) { const payload = await response.json(); throw new Error(payload.error?.message || '启动请求失败') }
      let instance: RuntimeInstance | undefined
      await readVerificationStream(response, event => {
        if (event.type === 'done') instance = event.data as RuntimeInstance
        report(event)
      }, activity)
      if (!instance?.id || instance.state !== 'RUNNING' || !Array.isArray(instance.endpoints)) throw new Error('服务器未返回有效的启动结果')
      return instance
    } catch (error) {
      if (controller.signal.aborted) throw new VerificationConnectionError('启动进度超过 90 秒未更新，请刷新查看环境状态')
      if (error instanceof VerificationConnectionError) throw new VerificationConnectionError('启动连接中断，请刷新查看环境状态')
      throw error
    } finally { clearTimeout(timeout) }
  },
  stopRuntime: (question: string) => request<unknown>(`/subtasks/${encodeURIComponent(question)}/runtime`, { method: 'DELETE' }),
  saveQuestionKnowledge: (id: string, version: number, tags: string[]) => request<{ version: number }>(`/admin/questions/${encodeURIComponent(id)}/knowledge`, { method: 'PUT', body: JSON.stringify({ version, tags }) }),
  knowledgePoints: () => request<{ items: import('./knowledgePoints').KnowledgePoint[] }>('/admin/knowledge-points'),
  createKnowledgePoint: (name: string, parent_name = '', owner_type = '') => request('/admin/knowledge-points', { method: 'POST', body: JSON.stringify({ name, parent_name, owner_type }) }),
  moveKnowledgePoint: (name: string, parent_name: string) => request(`/admin/knowledge-points/${encodeURIComponent(name)}/parent`, { method: 'PUT', body: JSON.stringify({ parent_name }) }),
  linkKnowledgeTypes: (name: string, types: string[]) => request(`/admin/knowledge-points/${encodeURIComponent(name)}/types`, { method: 'PUT', body: JSON.stringify({ types }) }),
  questionTypes: () => request<{ items: { name: string; parent_name?: string; question_count: number; usage_count: number }[]; tag_items: { name: string; parent_name?: string; question_count: number; usage_count: number }[]; tags: string[] }>('/admin/question-types'),
  changeTaxonomy: (kind: 'type' | 'tag', change: { source: string; target: string; mode: 'rename' | 'merge' | 'delete' }) => request<{ updated: number }>(`/admin/taxonomy/${kind}/change`, { method: 'POST', body: JSON.stringify(change) }),
  reclassifyQuestions: (category: string, items: { id: string; version: number }[]) => request<{ updated: number }>('/admin/questions/reclassify', { method: 'POST', body: JSON.stringify({ category, items }) }),
  moveQuestionType: (name: string, parent_name: string) => request(`/admin/question-types/${encodeURIComponent(name)}/parent`, { method: 'PUT', body: JSON.stringify({ parent_name }) }),
  createQuestionType: (name: string, parent_name = '') => request('/admin/question-types', { method: 'POST', body: JSON.stringify({ name, parent_name }) }),
  deleteQuestionType: (name: string) => request(`/admin/question-types/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  questions: () => request<{ items: ChallengeSet[] }>('/admin/questions'),
  sets: () => request<{ items: ChallengeSet[] }>('/sets'),
  set: (id: string) => request<SetResponse>(`/sets/${encodeURIComponent(id)}`),
  saveSet: (set: ChallengeSet, onUploadProgress?: UploadListener) => request<SetResponse>(set.id ? `/admin/sets/${encodeURIComponent(set.id)}` : '/admin/sets', {
    method: set.id ? 'PUT' : 'POST', body: JSON.stringify(set), onUploadProgress,
  }),
  saveCompetitionInfo: (set: ChallengeSet) => request<SetResponse>(`/admin/sets/${encodeURIComponent(set.id)}/info`, {
    method: 'PUT', body: JSON.stringify({ title: set.title, description: set.description, starts_at: set.starts_at, ends_at: set.ends_at, version: set.version }),
  }),
  setStatus: (id: string, status: 'PUBLISHED' | 'CLOSED', version: number) => request<SetResponse>(`/admin/sets/${encodeURIComponent(id)}/status`, {
    method: 'POST', body: JSON.stringify({ status, version }),
  }),
  deleteSet: (id: string, version: number) => request<unknown>(`/admin/sets/${encodeURIComponent(id)}?version=${version}`, { method: 'DELETE' }),
  stagePackage: (file: File) => request<{ package_token: string; environment: EnvironmentManifest; package_sha256: string }>('/admin/packages', {
    method: 'POST', headers: { 'Content-Type': 'application/x-tar' }, body: file,
  }),
  uploadPackage: (id: string, question: string, version: number, file: File) => request<SetResponse>(`/admin/sets/${encodeURIComponent(id)}/questions/${encodeURIComponent(question)}/package`, {
    method: 'POST', headers: { 'Content-Type': 'application/x-tar', 'X-Content-Version': String(version) }, body: file,
  }),
  submitFlag: (id: string, flag: string) => request<{ correct: boolean; first_solve: boolean; awarded_points: number }>(`/subtasks/${encodeURIComponent(id)}/submissions`, {
    method: 'POST', body: JSON.stringify({ flag }),
  }),
  leaderboard: (id: string) => request<{ items: Ranking[] }>(`/sets/${encodeURIComponent(id)}/leaderboard`),
  submissions: (id: string) => request<{ items: Submission[] }>(`/sets/${encodeURIComponent(id)}/submissions`),
  me: () => request<Session>('/auth/me'),
  login: (username: string, password: string) =>
    request<Session>('/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  register: (username: string, password: string) =>
    request<{ user: User }>('/auth/register', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => request<unknown>('/auth/logout', { method: 'POST' }),
  system: () => request<SystemInfo>('/admin/system'),
  users: () => request<{ items: User[] }>('/admin/users'),
  createUser: (input: CreateUserInput) =>
    request<unknown>('/admin/users', { method: 'POST', body: JSON.stringify(input) }),
  updateUserStatus: (id: User['id'], status: UserStatus) =>
    request<unknown>(`/admin/users/${encodeURIComponent(String(id))}`, {
      method: 'PATCH',
      body: JSON.stringify({ status }),
    }),
}
