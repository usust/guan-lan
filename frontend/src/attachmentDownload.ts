// Decode old, already-open pages in bounded chunks. Uint8Array.from(atob(data))
// creates an enormous intermediate array for large attachments.
export async function legacyAttachmentBlob(data: string): Promise<Blob> {
  const chunks: Uint8Array<ArrayBuffer>[] = []
  const chunkSize = 256 * 1024 // multiple of four for Base64 boundaries
  for (let offset = 0; offset < data.length; offset += chunkSize) {
    const decoded = atob(data.slice(offset, offset + chunkSize))
    const bytes = new Uint8Array(decoded.length)
    for (let i = 0; i < decoded.length; i++) bytes[i] = decoded.charCodeAt(i)
    chunks.push(bytes)
    await new Promise<void>(resolve => setTimeout(resolve, 0))
  }
  return new Blob(chunks, { type: 'application/octet-stream' })
}
