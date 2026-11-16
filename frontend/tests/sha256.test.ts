import { createHash } from 'node:crypto'
import { afterEach, expect, it, vi } from 'vitest'
import { sha256 } from '../src/sha256'
afterEach(() => vi.unstubAllGlobals())
it('hashes correctly without WebCrypto on HTTP origins, including SHA padding boundaries', async () => {
  vi.stubGlobal('crypto', undefined)
  for (const size of [0, 3, 55, 56, 63, 64, 65, 1024, 300000]) {
    const bytes = Uint8Array.from({ length: size }, (_, i) => i % 251)
    expect(await sha256(bytes.buffer)).toBe(createHash('sha256').update(bytes).digest('hex'))
  }
})
it('allows pausing while the HTTP fallback hashes a large chunk', async () => {
  vi.stubGlobal('crypto', undefined)
  const controller = new AbortController()
  const hashing = sha256(new ArrayBuffer(1024 * 1024), controller.signal)
  controller.abort()
  await expect(hashing).rejects.toMatchObject({ name: 'AbortError' })
})
