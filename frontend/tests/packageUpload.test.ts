import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { uploadPackageResumable, formatUploadBytes, formatUploadTime, type UploadProgress } from '../src/packageUpload'
import { ApiError, request, sendPackageChunk } from '../src/api'

vi.mock('../src/api', async importOriginal => {
  const original = await importOriginal<typeof import('../src/api')>()
  return { ...original, request: vi.fn(), sendPackageChunk: vi.fn() }
})
const req = vi.mocked(request)
const send = vi.mocked(sendPackageChunk)
const data = new TextEncoder().encode('abcdef')
const result = { package_token: 'token', environment: { services: [] }, package_sha256: 'sha' }
async function sha(value: Uint8Array) {
  return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', new Uint8Array(value).buffer)), b => b.toString(16).padStart(2, '0')).join('')
}
function file() { return new File([data], 'test.tar', { lastModified: 1 }) }
beforeEach(() => {
  vi.resetAllMocks()
  const storage = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key) })
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('resumable TAR upload', () => {
  it('keeps speed across chunk boundaries and limits frequent progress updates', async () => {
    let now = 0
    vi.spyOn(performance, 'now').mockImplementation(() => now)
    const session = { id: 'steady', name: 'test.tar', size: 6, offset: 0, chunk_size: 3, state: 'uploading' as const, chunks: [] }
    req.mockResolvedValueOnce(session).mockResolvedValueOnce({ state: 'done', result })
    send.mockImplementation(async (_id, offset, _blob, _hash, _signal, progress) => {
      now += 1000
      progress(1)
      now += 100
      progress(2)
      now += 400
      progress(3)
      return { ...session, offset: offset + 3 }
    })
    const updates: UploadProgress[] = []
    await uploadPackageResumable(file(), 'admin', new AbortController().signal, value => updates.push(value))
    const uploading = updates.filter(value => value.phase === 'uploading')
    expect(uploading.map(value => value.loaded)).toEqual([0, 1, 3, 4, 6])
    expect(uploading.slice(1).every(value => value.speed > 0 && value.remaining !== null)).toBe(true)
    expect(uploading.at(-1)?.speed).toBe(2)
    expect(updates.at(-1)).toMatchObject({ phase: 'validating', loaded: 6 })
  })

  it('verifies received chunks and sends only the remainder after reopening', async () => {
    req.mockResolvedValueOnce({ id: 'resume', size: 6, offset: 3, chunk_size: 3, state: 'uploading', chunks: [{ size: 3, sha256: await sha(data.slice(0, 3)) }] })
    send.mockImplementationOnce(async (id, offset, blob, hash, _signal, progress) => {
      expect(id).toBe('resume')
      expect(offset).toBe(3)
      expect(await blob.text()).toBe('def')
      expect(hash).toBe(await sha(data.slice(3)))
      progress(2)
      return { id, name: 'test.tar', size: 6, offset: 6, chunk_size: 3, state: 'uploading', chunks: [] }
    })
    req.mockResolvedValueOnce({ state: 'done', result })
    const updates: UploadProgress[] = []
    expect(await uploadPackageResumable(file(), 'admin', new AbortController().signal, value => updates.push(value))).toEqual(result)
    expect(send).toHaveBeenCalledTimes(1)
    expect(updates.some(p => p.phase === 'checking')).toBe(true)
    expect(updates.some(p => p.phase === 'uploading' && p.loaded === 5 && p.speed > 0)).toBe(true)
    expect(updates.at(-1)).toMatchObject({ phase: 'validating', loaded: 6, total: 6 })
  })

  it('rejects a different prefix before appending any bytes', async () => {
    req.mockResolvedValueOnce({ id: 'resume', size: 6, offset: 3, chunk_size: 3, state: 'uploading', chunks: [{ size: 3, sha256: 'incorrect' }] })
    await expect(uploadPackageResumable(file(), 'admin', new AbortController().signal, () => {})).rejects.toThrow('内容不一致')
    expect(send).not.toHaveBeenCalled()
  })

  it('retains the session when paused and discovers it again on continue', async () => {
    const session = { id: 'paused', size: 6, offset: 0, chunk_size: 8, state: 'uploading', chunks: [] }
    req.mockResolvedValue(session)
    const controller = new AbortController()
    send.mockImplementationOnce(async () => {
      controller.abort()
      throw new DOMException('Paused', 'AbortError')
    })
    await expect(uploadPackageResumable(file(), 'admin', controller.signal, () => {})).rejects.toMatchObject({ name: 'AbortError' })
    send.mockResolvedValueOnce({ ...session, name: 'test.tar', state: 'uploading', offset: 6 })
    req.mockResolvedValueOnce(session).mockResolvedValueOnce({ state: 'done', result })
    expect(await uploadPackageResumable(file(), 'admin', new AbortController().signal, () => {})).toEqual(result)
    expect(req.mock.calls[1]?.[0]).toBe('/admin/package-uploads/paused')
  })

  it('retries completion after a lost response without sending the file again', async () => {
    vi.useFakeTimers()
    req.mockResolvedValueOnce({ id: 'complete', size: 6, offset: 6, chunk_size: 8, state: 'uploading', chunks: [{ size: 6, sha256: await sha(data) }] })
      .mockRejectedValueOnce(new ApiError('offline', 0, 'NETWORK_ERROR'))
      .mockResolvedValueOnce({ state: 'done', result })
    const pending = uploadPackageResumable(file(), 'admin', new AbortController().signal, () => {})
    // Hashing uses asynchronous work outside fake timers.
    await vi.waitFor(() => expect(req).toHaveBeenCalledTimes(2))
    await vi.runAllTimersAsync()
    expect(await pending).toEqual(result)
    expect(send).not.toHaveBeenCalled()
    expect(req.mock.calls[2]?.[0]).toBe('/admin/package-uploads/complete/complete?retry=1')
  })

  it('formats large sizes and remaining time', () => {
    expect(formatUploadBytes(20 * 1024 ** 3)).toBe('20.0 GiB')
    expect(formatUploadTime(null)).toBe('计算中')
    expect(formatUploadTime(61)).toBe('2 分钟')
  })
})
