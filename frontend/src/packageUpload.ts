import { sha256 } from './sha256'
import { ApiError, request, sendPackageChunk, type PackageUploadSession, type StagedPackage } from './api'

export interface UploadProgress {
  phase: 'preparing' | 'checking' | 'uploading' | 'validating'
  loaded: number
  total: number
  speed: number
  remaining: number | null
}
async function digest(blob: Blob, signal: AbortSignal) {
  signal.throwIfAborted()
  const result = await sha256(await blob.arrayBuffer(), signal)
  signal.throwIfAborted()
  return result
}
function readSaved(key: string) { try { return localStorage.getItem(key) } catch { return null } }
function saveID(key: string, id: string | null) {
  try { if (id) localStorage.setItem(key, id); else localStorage.removeItem(key) } catch { /* Server-side discovery still allows resuming. */ }
}

function waitForRetry(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(new DOMException('上传已暂停', 'AbortError')) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, ms)
    if (signal.aborted) { abort(); return }
    signal.addEventListener('abort', abort, { once: true })
  })
}

export function uploadPackageResumable(file: File, owner: string, signal: AbortSignal, progress: (value: UploadProgress) => void): Promise<StagedPackage> {
  return uploadFileResumable<StagedPackage>(file, owner, signal, progress, '/admin/package-uploads')
}
export async function uploadFileResumable<T>(file: File, owner: string, signal: AbortSignal, progress: (value: UploadProgress) => void, basePath: string): Promise<T> {
  const emit = (phase: UploadProgress['phase'], loaded = 0, speed = 0) =>
    progress({ phase, loaded, total: file.size, speed, remaining: speed > 0 ? (file.size - loaded) / speed : null })
  emit('preparing')
  // Sampled identity locates a session cheaply. Every stored chunk is then hashed
  // against the selected file before resuming, so mismatched content cannot be spliced.
  const sample = 64 * 1024
  const fingerprint = await digest(new Blob([
    JSON.stringify([file.name, file.size, file.lastModified]),
    file.slice(0, sample), file.slice(Math.max(0, Math.floor(file.size / 2) - sample / 2), Math.floor(file.size / 2) + sample / 2),
    file.slice(Math.max(0, file.size - sample)),
  ]), signal)
  const key = `huashan:${basePath}:${owner}:${fingerprint}`
  const saved = readSaved(key)
  let session: PackageUploadSession<T> | undefined
  if (saved) {
    try { session = await request<PackageUploadSession<T>>(`${basePath}/${encodeURIComponent(saved)}`, { signal }) }
    catch (error) { if (!(error instanceof ApiError) || ![404, 410].includes(error.status)) throw error; saveID(key, null) }
  }
  if (!session) session = await request<PackageUploadSession<T>>(basePath, {
    method: 'POST', body: JSON.stringify({ name: file.name, size: file.size, fingerprint }), signal,
  })
  saveID(key, session.id)
  if (session.size !== file.size || session.offset < 0 || session.offset > file.size || session.chunk_size <= 0) throw new Error('上传记录不匹配，请重新选择文件')
  let checked = 0
  for (const chunk of session.chunks) {
    emit('checking', checked)
    if (chunk.size <= 0 || checked + chunk.size > session.offset ||
        await digest(file.slice(checked, checked + chunk.size), signal) !== chunk.sha256) {
      throw new Error('所选文件与断点记录内容不一致，请选择原文件')
    }
    checked += chunk.size
  }
  if (checked !== session.offset) throw new Error('上传断点记录不完整')
  if (session.result) { saveID(key, null); return session.result }
  // Keep one clock across chunks, including hashing and server acknowledgement.
  // Restarting it for each request makes speed and ETA flash back to zero.
  const initialOffset = session.offset
  const uploadBegan = performance.now()
  let highestLoaded = initialOffset
  let lastUpdate = uploadBegan
  let publishedSpeed = 0
  emit('uploading', initialOffset)
  const reportUpload = (loaded: number) => {
    highestLoaded = Math.max(highestLoaded, loaded)
    const now = performance.now()
    const seconds = (now - uploadBegan) / 1000
    // Limit reactive text updates while allowing the first transfer sample through.
    if (now - lastUpdate < 500 && (publishedSpeed > 0 || highestLoaded === initialOffset)) return
    publishedSpeed = seconds > 0 ? (highestLoaded - initialOffset) / seconds : 0
    lastUpdate = now
    emit('uploading', loaded, publishedSpeed)
  }
  let failures = 0
  while (session.offset < file.size) {
    signal.throwIfAborted()
    const start: number = session.offset
    const chunk = file.slice(start, Math.min(start + session.chunk_size, file.size))
    const sha256 = await digest(chunk, signal)
    try {
      session = await sendPackageChunk<T>(session.id, start, chunk, sha256, signal, bytes => {
        reportUpload(start + bytes)
      }, basePath)
      reportUpload(session.offset)
      failures = 0
    } catch (error) {
      signal.throwIfAborted()
      if (!(error instanceof ApiError) || ![0, 409, 429, 500, 502, 503, 504].includes(error.status) || ++failures > 3) throw error
      // A response can be lost after commit; always ask the server before retrying.
      await waitForRetry(failures * 1000, signal)
      session = await request<PackageUploadSession<T>>(`${basePath}/${encodeURIComponent(session.id)}`, { signal })
      if (session.offset !== start && session.offset !== start + chunk.size) throw new Error('上传位置已变化，请重新选择文件继续上传')
    }
  }
  emit('validating', file.size)
  // POST is idempotent and also restarts validation after a server restart.
  let firstCompletion = true
  let completionFailures = 0
  for (;;) {
    signal.throwIfAborted()
    try {
      const completion = await request<PackageUploadSession<T>>(`${basePath}/${encodeURIComponent(session.id)}/complete${firstCompletion ? '?retry=1' : ''}`, { method: 'POST', signal })
      firstCompletion = false
      completionFailures = 0
      if (completion.result) { saveID(key, null); return completion.result }
      await waitForRetry(1500, signal)
    } catch (error) {
      signal.throwIfAborted()
      if (!(error instanceof ApiError) || ![0, 429, 500, 502, 503, 504].includes(error.status) || ++completionFailures > 3) throw error
      // Completion is idempotent, including when validation started but its response was lost.
      await waitForRetry(completionFailures * 1000, signal)
    }
  }
}
export function formatUploadBytes(bytes: number) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let unit = 0
  while (bytes >= 1024 && unit < units.length - 1) { bytes /= 1024; unit++ }
  return `${bytes.toFixed(unit ? 1 : 0)} ${units[unit]}`
}
export function formatUploadTime(seconds: number | null) {
  if (seconds === null || !Number.isFinite(seconds)) return '计算中'
  if (seconds < 60) return `${Math.ceil(seconds)} 秒`
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分钟`
  return `${Math.floor(seconds / 3600)} 小时 ${Math.ceil(seconds % 3600 / 60)} 分钟`
}
