import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, errorMessage, request, setCsrfToken, setUnauthorizedHandler } from '../src/api'

const fetchMock = vi.fn<typeof fetch>()
function reply(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  fetchMock.mockReset()
  setCsrfToken(null)
  setUnauthorizedHandler(() => undefined)
})
afterEach(() => vi.unstubAllGlobals())

describe('API transport', () => {
  it('reports real upload bytes and preserves credentials and CSRF on progress requests', async () => {
    const headers = new Map<string, string>()
    const progress = vi.fn()
    const open = vi.fn()
    const xhr = {
      open, withCredentials: false, upload: {} as { onprogress?: (event: { lengthComputable: boolean; loaded: number; total: number }) => void },
      setRequestHeader: (key: string, value: string) => headers.set(key, value),
      status: 200, responseText: JSON.stringify({ data: { saved: true } }),
      onload: () => {}, onerror: () => {}, onabort: () => {}, abort: vi.fn(),
      send: vi.fn(() => {
        xhr.upload.onprogress?.({ lengthComputable: true, loaded: 50, total: 100 })
        xhr.onload()
      }),
    }
    vi.stubGlobal('XMLHttpRequest', class { constructor() { return xhr } })
    setCsrfToken('upload-csrf')
    expect(await request('/admin/sets', { method: 'POST', body: '{}', onUploadProgress: progress })).toEqual({ saved: true })
    expect(open).toHaveBeenCalledWith('POST', '/api/v1/admin/sets')
    expect(xhr.withCredentials).toBe(true)
    expect(headers.get('x-csrf-token')).toBe('upload-csrf')
    expect(headers.get('content-type')).toBe('application/json')
    expect(progress).toHaveBeenCalledWith(50, 100)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('registers through the public endpoint without requesting a role', async () => {
    const user = { id: 'new-user', username: 'player', display_name: 'player', role: 'PLAYER', status: 'ACTIVE' }
    fetchMock.mockResolvedValue(reply({ data: { user } }, 201))
    expect(await api.register('player', 'local-password-123')).toEqual({ user })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/register', expect.objectContaining({
      method: 'POST', credentials: 'include', body: JSON.stringify({ username: 'player', password: 'local-password-123' }),
    }))
  })

  it('reports a duplicate registration so the user can choose another name', async () => {
    fetchMock.mockResolvedValue(reply({ error: { code: 'USERNAME_TAKEN', message: '用户名已存在' } }, 409))
    await expect(api.register('player', 'local-password-123')).rejects.toMatchObject({ status: 409, code: 'USERNAME_TAKEN' })
  })

  it('unwraps data and always includes cookie credentials', async () => {
    fetchMock.mockResolvedValue(reply({ data: { items: [] }, request_id: 'r1' }))
    expect(await api.users()).toEqual({ items: [] })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/users', expect.objectContaining({ credentials: 'include', method: 'GET' }))
    const headers = fetchMock.mock.calls[0]![1]!.headers as Headers
    expect(headers.has('X-CSRF-Token')).toBe(false)
  })

  it('attaches in-memory CSRF to mutations but not GET', async () => {
    setCsrfToken('csrf-example')
    fetchMock.mockImplementation(async () => reply({ data: {}, request_id: 'r2' }))
    await api.updateUserStatus('user/1', 'DISABLED')
    const [url, init] = fetchMock.mock.calls[0]!
    expect(url).toBe('/api/v1/admin/users/user%2F1')
    expect(init?.method).toBe('PATCH')
    expect(init?.body).toBe('{"status":"DISABLED"}')
    expect((init?.headers as Headers).get('X-CSRF-Token')).toBe('csrf-example')
    await api.me()
    expect((fetchMock.mock.calls[1]![1]!.headers as Headers).has('X-CSRF-Token')).toBe(false)
  })

  it('does not retain CSRF after clearing the session', async () => {
    setCsrfToken('old-token')
    setCsrfToken(null)
    fetchMock.mockResolvedValue(reply({ data: {} }))
    await api.login('player', 'secret')
    expect((fetchMock.mock.calls[0]![1]!.headers as Headers).has('X-CSRF-Token')).toBe(false)
  })

  it('sends CSRF on logout and supports an empty successful response', async () => {
    setCsrfToken('logout-token')
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
    await expect(api.logout()).resolves.toBeUndefined()
    expect((fetchMock.mock.calls[0]![1]!.headers as Headers).get('X-CSRF-Token')).toBe('logout-token')
  })

  it('exposes server errors and request IDs', async () => {
    fetchMock.mockResolvedValue(reply({ error: { code: 'FORBIDDEN', message: '无权访问' }, request_id: 'req-123' }, 403))
    await expect(api.users()).rejects.toMatchObject({ status: 403, code: 'FORBIDDEN', requestId: 'req-123', message: '无权访问' })
    expect(errorMessage(new ApiError('无权访问', 403, 'FORBIDDEN', 'req-123'))).toContain('req-123')
  })

  it('invalidates the session for 401 except a failed login', async () => {
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)
    fetchMock.mockResolvedValue(reply({ error: { code: 'UNAUTHORIZED', message: '请登录' } }, 401))
    await expect(api.login('bad', 'bad')).rejects.toBeInstanceOf(ApiError)
    expect(unauthorized).not.toHaveBeenCalled()
    await expect(api.users()).rejects.toBeInstanceOf(ApiError)
    expect(unauthorized).toHaveBeenCalledOnce()
  })

  it('reports malformed and network responses instead of crashing', async () => {
    fetchMock.mockResolvedValueOnce(reply(null)).mockResolvedValueOnce(reply({ request_id: 'r3' })).mockRejectedValueOnce(new TypeError('offline'))
    await expect(request('/test')).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
    await expect(request('/test')).rejects.toMatchObject({ code: 'INVALID_RESPONSE', requestId: 'r3' })
    await expect(request('/test')).rejects.toMatchObject({ code: 'NETWORK_ERROR' })
  })
})

it('uploads raw TAR bytes with CSRF, content version and the original content type', async () => {
  setCsrfToken('upload-csrf')
  fetchMock.mockResolvedValue(reply({ data: { set: {} } }))
  const file = new File(['archive'], 'environment.tar', { type: 'application/x-tar' })
  await api.uploadPackage('set-1', 'question-1', 7, file)
  const [url, init] = fetchMock.mock.calls[0]!
  expect(url).toBe('/api/v1/admin/sets/set-1/questions/question-1/package')
  expect(init?.body).toBe(file)
  const headers = init?.headers as Headers
  expect(headers.get('Content-Type')).toBe('application/x-tar')
  expect(headers.get('X-Content-Version')).toBe('7')
  expect(headers.get('X-CSRF-Token')).toBe('upload-csrf')
})

it('runs versioned acceptance and scopes lifecycle operations to the selected question', async () => {
  setCsrfToken('runtime-csrf')
  fetchMock.mockImplementation(async () => reply({ data: { state: 'RUNNING', endpoints: [] } }))
  await api.verifyRuntime('set/1', 'question/1', 9)
  let [url, init] = fetchMock.mock.calls[0]!
  expect(url).toBe('/api/v1/admin/sets/set%2F1/questions/question%2F1/verify')
  expect(init?.body).toBe(JSON.stringify({ version: 9 }))
  expect((init?.headers as Headers).get('X-CSRF-Token')).toBe('runtime-csrf')
  await api.startRuntime('question/1')
  expect(fetchMock.mock.calls[1]![0]).toBe('/api/v1/subtasks/question%2F1/runtime')
  expect(fetchMock.mock.calls[1]![1]?.method).toBe('POST')
  await api.stopRuntime('question/1')
  expect(fetchMock.mock.calls[2]![1]?.method).toBe('DELETE')
})

it('keeps multi-container verification alive past 115 seconds while heartbeats arrive', async () => {
  vi.useFakeTimers()
  try {
    fetchMock.mockImplementation(async (_url, init) => new Response(new ReadableStream({
      start(controller) {
        init?.signal?.addEventListener('abort', () => controller.error(new DOMException('aborted', 'AbortError')))
        const encode = (type: string) => new TextEncoder().encode(JSON.stringify({ type, message: type }) + '\n')
        setTimeout(() => controller.enqueue(encode('heartbeat')), 60_000)
        setTimeout(() => controller.enqueue(encode('heartbeat')), 120_000)
        setTimeout(() => { controller.enqueue(encode('done')); controller.close() }, 180_000)
      },
    })))
    const report = vi.fn()
    const result = api.verifyRuntime('set', 'question', 1, report)
    await vi.advanceTimersByTimeAsync(180_001)
    await result
    expect(report.mock.calls.map(([event]) => event.type)).toEqual(['done'])
    expect(fetchMock.mock.calls[0]![1]?.signal?.aborted).toBe(false)
  } finally { vi.useRealTimers() }
})

it('ends verification when the server stops sending all progress and heartbeats', async () => {
  vi.useFakeTimers()
  try {
    fetchMock.mockImplementation(async (_url, init) => new Response(new ReadableStream({
      start(controller) { init?.signal?.addEventListener('abort', () => controller.error(new DOMException('aborted', 'AbortError'))) },
    })))
    const result = api.verifyRuntime('set', 'question', 1, () => {}).catch(error => error)
    await vi.advanceTimersByTimeAsync(90_001)
    expect((await result).message).toContain('90 秒未更新')
  } finally { vi.useRealTimers() }
})

it('streams startup stages and returns the ready instance from the completion event', async () => {
  setCsrfToken('startup-csrf')
  const instance = { id: 'instance', state: 'RUNNING', expires_at: 1000, endpoints: [{ service: 'web', port: 80, protocol: 'tcp', address: 'localhost:30001' }] }
  fetchMock.mockResolvedValue(new Response([
    { type: 'progress', message: '检查启动条件' },
    { type: 'heartbeat', message: 'alive' },
    { type: 'progress', message: '检查公开端口映射和 TCP 连通性' },
    { type: 'done', message: '系统已就绪', data: instance },
  ].map(event => JSON.stringify(event)).join('\n') + '\n'))
  const report = vi.fn()
  expect(await api.startRuntime('task/1', report)).toEqual(instance)
  expect(fetchMock.mock.calls[0]![0]).toBe('/api/v1/subtasks/task%2F1/runtime')
  const headers = fetchMock.mock.calls[0]![1]?.headers as Record<string, string>
  expect(headers.Accept).toBe('application/x-ndjson')
  expect(headers['X-CSRF-Token']).toBe('startup-csrf')
  expect(report.mock.calls.map(([event]) => event.type)).toEqual(['progress', 'progress', 'done'])
})


describe('taxonomy management', () => {
 it('sends only classification metadata with CSRF', async () => {
  setCsrfToken('taxonomy-token')
  fetchMock.mockResolvedValue(reply({ data: { updated: 2 } }))
  expect(await api.changeTaxonomy('type', { source: '数据分析题', target: '数据分析', mode: 'merge' })).toEqual({ updated: 2 })
  const [path, options] = fetchMock.mock.calls[0]!
  expect(path).toBe('/api/v1/admin/taxonomy/type/change')
  expect(options?.method).toBe('POST')
  expect(JSON.parse(options?.body as string)).toEqual({ source: '数据分析题', target: '数据分析', mode: 'merge' })
  expect(new Headers(options?.headers).get('X-CSRF-Token')).toBe('taxonomy-token')
 })
 it('includes versions for atomic batch classification', async () => {
  fetchMock.mockResolvedValue(reply({ data: { updated: 1 } }))
  await api.reclassifyQuestions('PWN', [{ id: 'library-question', version: 4 }])
  const [path, options] = fetchMock.mock.calls[0]!
  expect(path).toBe('/api/v1/admin/questions/reclassify')
  expect(JSON.parse(options?.body as string)).toEqual({ category: 'PWN', items: [{ id: 'library-question', version: 4 }] })
 })
})


it('sends a single knowledge owner without changing question selections', async () => {
 setCsrfToken('knowledge-token')
 fetchMock.mockResolvedValue(reply({ data: { updated: true } }))
 await api.linkKnowledgeTypes('JWT', ['WEB'])
 const [path, options] = fetchMock.mock.calls[0]!
 expect(path).toBe('/api/v1/admin/knowledge-points/JWT/types')
 expect(options?.method).toBe('PUT')
 expect(JSON.parse(options?.body as string)).toEqual({ types: ['WEB'] })
 expect(new Headers(options?.headers).get('X-CSRF-Token')).toBe('knowledge-token')
})


it('updates library knowledge with version without sending flags or attachments', async () => {
 fetchMock.mockResolvedValue(reply({ data: { version: 8 } }))
 await api.saveQuestionKnowledge('library-id', 7, ['SQL 注入'])
 const [path, options] = fetchMock.mock.calls[0]!
 expect(path).toBe('/api/v1/admin/questions/library-id/knowledge')
 expect(options?.method).toBe('PUT')
 expect(JSON.parse(options?.body as string)).toEqual({ version: 7, tags: ['SQL 注入'] })
})
