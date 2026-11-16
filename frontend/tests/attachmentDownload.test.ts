import { describe, expect, it } from 'vitest'
import { legacyAttachmentBlob } from '../src/attachmentDownload'

describe('legacy attachment download', () => {
  it('preserves binary bytes across Base64 chunk boundaries and padding', async () => {
    const bytes = new Uint8Array(400001)
    for (let i = 0; i < bytes.length; i++) bytes[i] = i % 256
    const data = Buffer.from(bytes).toString('base64')
    const blob = await legacyAttachmentBlob(data)
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(bytes)
  })
  it('rejects corrupt data', async () => {
    await expect(legacyAttachmentBlob('%%%')).rejects.toThrow()
  })
})
