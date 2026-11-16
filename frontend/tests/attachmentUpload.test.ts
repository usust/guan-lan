import { afterEach, describe, expect, it, vi } from 'vitest'
import { uploadFileResumable } from '../src/packageUpload'
import { request, sendPackageChunk } from '../src/api'
import type { Attachment } from '../src/types'
import { copyEditorValue, equalEditorValue } from '../src/editorSnapshot'
vi.mock('../src/sha256', () => ({ sha256: vi.fn(async () => 'a'.repeat(64)) }))
vi.mock('../src/api', async importOriginal => ({
  ...await importOriginal<typeof import('../src/api')>(),
  request: vi.fn(), sendPackageChunk: vi.fn(),
}))
afterEach(() => { vi.clearAllMocks(); vi.unstubAllGlobals() })
describe('large binary attachments', () => {
  it('sends 450 MiB as bounded chunks and returns only a file reference', async () => {
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} })
    const size = 450 * 1024 * 1024
    const chunkSize = 8 * 1024 * 1024
    // Simulate file slices without allocating a 450 MiB test buffer.
    const file = {
      name: 'large.zip', size, lastModified: 1,
      slice: (start: number, end: number) => ({
        size: end - start, arrayBuffer: async () => new ArrayBuffer(1),
      }),
    } as File
    const session = { id: 'large', name: file.name, size, offset: 0, chunk_size: chunkSize, state: 'uploading' as const, chunks: [] }
    const attachment: Attachment = { name: file.name, data: '', file_id: 'large', size }
    vi.mocked(request).mockResolvedValueOnce(session).mockResolvedValueOnce({ ...session, offset: size, state: 'done', result: attachment })
    vi.mocked(sendPackageChunk).mockImplementation(async (_id, offset, chunk, _hash, _signal, progress, path) => {
      expect(path).toBe('/admin/attachment-uploads')
      expect(chunk.size).toBeLessThanOrEqual(chunkSize)
      progress(chunk.size)
      return { ...session, offset: offset + chunk.size }
    })
    const updates: number[] = []
    const result = await uploadFileResumable<Attachment>(file, 'admin', new AbortController().signal,
      value => updates.push(value.loaded), '/admin/attachment-uploads')
    expect(result).toEqual(attachment)
    expect(sendPackageChunk).toHaveBeenCalledTimes(57)
    expect(request).toHaveBeenCalledWith('/admin/attachment-uploads', expect.objectContaining({
      method: 'POST', body: expect.not.stringContaining('base64'),
    }))
    expect(updates.at(-1)).toBe(size)
    expect(JSON.stringify(result).length).toBeLessThan(200)
  })
  it('snapshots metadata and detects replacements without serializing attachment content', () => {
    const model = { attachments: [{ name: 'a.zip', data: '', file_id: 'file-a', size: 450 * 1024 * 1024 }] }
    const snapshot = copyEditorValue(model)
    expect(equalEditorValue(model, snapshot)).toBe(true)
    model.attachments[0]!.file_id = 'file-b'
    expect(equalEditorValue(model, snapshot)).toBe(false)
    expect(snapshot.attachments[0]!.file_id).toBe('file-a')
  })
})
